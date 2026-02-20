package cmd

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"h2/internal/bridgeservice"
	"h2/internal/config"
	"h2/internal/session/message"
	"h2/internal/socketdir"
	"h2/internal/tmpl"
)

const conciergeSessionName = "concierge"

func newBridgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bridge",
		Short: "Manage bridge services",
		Long: `Manage bridge services that route messages between external platforms
(Telegram, macOS notifications) and h2 agent sessions.

Use "h2 bridge create" to start a new bridge, or use the subcommands
to manage running bridges.`,
	}

	// Create is the default subcommand: if any flags are passed to the
	// parent command, delegate to create for backward compatibility.
	createCmd := newBridgeCreateCmd()
	cmd.AddCommand(createCmd)
	cmd.AddCommand(newBridgeStopCmd())
	cmd.AddCommand(newBridgeSetConciergeCmd())
	cmd.AddCommand(newBridgeRemoveConciergeCmd())

	// Backward compat: if parent is invoked with flags but no subcommand,
	// run the create command. Without flags and no subcommand, show help.
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().NFlag() > 0 {
			return createCmd.RunE(createCmd, args)
		}
		return cmd.Help()
	}

	// Mirror create's flags on the parent for backward compat.
	cmd.Flags().AddFlagSet(createCmd.Flags())

	return cmd
}

func newBridgeCreateCmd() *cobra.Command {
	var forUser string
	var noConcierge bool
	var setConcierge string
	var conciergeRole string

	cmd := &cobra.Command{
		Use:   "create [--no-concierge | --set-concierge <name>] [--concierge-role <name>]",
		Short: "Create and start a bridge service",
		Long: `Creates and starts a bridge service that routes messages between external
platforms (Telegram, macOS notifications) and h2 agent sessions.

By default, also starts a concierge session (named "concierge") using the
"concierge" role and attaches to it interactively. Use --no-concierge to run
only the bridge service with no default routing. Use --set-concierge <name>
to route to an existing agent without spawning a new session.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if noConcierge && setConcierge != "" {
				return fmt.Errorf("cannot specify both --no-concierge and --set-concierge")
			}
			if cmd.Flags().Changed("concierge-role") && (noConcierge || setConcierge != "") {
				return fmt.Errorf("--concierge-role cannot be used with --no-concierge or --set-concierge")
			}
			if setConcierge != "" {
				// Not launching a new concierge, so no command/args needed.
			} else if !noConcierge && conciergeRole == "" {
				return fmt.Errorf("--concierge-role is required when launching a new concierge session")
			}

			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			user, userCfg, err := resolveUser(cfg, forUser)
			if err != nil {
				return err
			}

			// Validate bridges exist before forking anything.
			bridges := bridgeservice.FromConfig(&userCfg.Bridges)
			if len(bridges) == 0 {
				return fmt.Errorf("no bridges configured for user %q", user)
			}

			// Determine concierge name for routing.
			var concierge string
			if setConcierge != "" {
				concierge = setConcierge
			} else if !noConcierge {
				concierge = conciergeSessionName
			}

			// Fork the bridge service as a background daemon.
			fmt.Fprintf(os.Stderr, "Starting bridge service for user %q...\n", user)
			if err := bridgeservice.ForkBridge(user, concierge); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Bridge service started.\n")

			if noConcierge || setConcierge != "" {
				return nil
			}

			// Setup and fork the concierge session from the role.
			ctx := &tmpl.Context{
				AgentName: conciergeSessionName,
				RoleName:  conciergeRole,
				H2Dir:     config.ConfigDir(),
			}
			role, err := config.LoadRoleRendered(conciergeRole, ctx)
			if err != nil {
				return fmt.Errorf("concierge role not found; create one with: h2 role init concierge")
			}
			return setupAndForkAgent(conciergeSessionName, role, false, "", nil)
		},
	}

	cmd.Flags().StringVar(&forUser, "for", "", "Which user's bridge config to load")
	cmd.Flags().BoolVar(&noConcierge, "no-concierge", false, "Run without a concierge session")
	cmd.Flags().StringVar(&setConcierge, "set-concierge", "", "Route to an existing concierge agent by name")
	cmd.Flags().StringVar(&conciergeRole, "concierge-role", "concierge", "Role to use for the concierge session")

	return cmd
}

func newBridgeStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop [name]",
		Short: "Stop a running bridge",
		Long: `Stop a running bridge service. If name is omitted and exactly one bridge
is running, stops it. If multiple bridges are running, returns an error
listing them.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var name string
			if len(args) > 0 {
				name = args[0]
			}

			sockPath, err := findBridgeSocket(name)
			if err != nil {
				return err
			}

			conn, err := net.Dial("unix", sockPath)
			if err != nil {
				return fmt.Errorf("cannot connect to bridge: %w", err)
			}
			defer conn.Close()

			if err := message.SendRequest(conn, &message.Request{Type: "stop"}); err != nil {
				return fmt.Errorf("send stop request: %w", err)
			}

			resp, err := message.ReadResponse(conn)
			if err != nil {
				return fmt.Errorf("read response: %w", err)
			}
			if !resp.OK {
				return fmt.Errorf("stop failed: %s", resp.Error)
			}

			if name != "" {
				fmt.Printf("Stopped bridge %s.\n", name)
			} else {
				fmt.Println("Bridge stopped.")
			}
			return nil
		},
	}
}

func newBridgeSetConciergeCmd() *cobra.Command {
	var forUser string

	cmd := &cobra.Command{
		Use:   "set-concierge <agent-name>",
		Short: "Set or change the concierge agent for a running bridge",
		Long: `Set or change the concierge agent for a running bridge. If a concierge
is already assigned, it will be replaced. The named agent does not need
to be running yet.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName := args[0]

			resp, err := bridgeRequest(forUser, "set-concierge", agentName)
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("set-concierge failed: %s", resp.Error)
			}

			if resp.OldConcierge != "" {
				fmt.Printf("Concierge changed from %s to %s.\n", resp.OldConcierge, agentName)
			} else {
				fmt.Printf("Concierge set to %s.\n", agentName)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&forUser, "for", "", "Which user's bridge to target")

	return cmd
}

func newBridgeRemoveConciergeCmd() *cobra.Command {
	var forUser string

	cmd := &cobra.Command{
		Use:   "remove-concierge",
		Short: "Remove the concierge agent from a running bridge",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := bridgeRequest(forUser, "remove-concierge", "")
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("remove-concierge failed: %s", resp.Error)
			}

			fmt.Println("Concierge removed.")
			return nil
		},
	}

	cmd.Flags().StringVar(&forUser, "for", "", "Which user's bridge to target")

	return cmd
}

// bridgeRequest sends a request to a running bridge's socket and returns the response.
// If userName is empty and exactly one bridge is running, it targets that bridge.
func bridgeRequest(userName, reqType, body string) (*message.Response, error) {
	sockPath, err := findBridgeSocket(userName)
	if err != nil {
		return nil, err
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to bridge: %w", err)
	}
	defer conn.Close()

	if err := message.SendRequest(conn, &message.Request{Type: reqType, Body: body}); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	resp, err := message.ReadResponse(conn)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return resp, nil
}

// findBridgeSocket locates the bridge socket. If name is non-empty, it uses
// that directly. If empty and exactly one bridge is running, uses that.
// Returns an error if no bridges or multiple bridges are found.
func findBridgeSocket(name string) (string, error) {
	if name != "" {
		return socketdir.Path(socketdir.TypeBridge, name), nil
	}

	bridges, err := socketdir.ListByType(socketdir.TypeBridge)
	if err != nil {
		return "", fmt.Errorf("list bridges: %w", err)
	}
	if len(bridges) == 0 {
		return "", fmt.Errorf("no bridges are running")
	}
	if len(bridges) > 1 {
		var names []string
		for _, b := range bridges {
			names = append(names, b.Name)
		}
		return "", fmt.Errorf("multiple bridges are running: %s; specify which one", strings.Join(names, ", "))
	}
	return bridges[0].Path, nil
}

// resolveUser determines which user config to use.
func resolveUser(cfg *config.Config, forUser string) (string, *config.UserConfig, error) {
	if forUser != "" {
		uc, ok := cfg.Users[forUser]
		if !ok {
			return "", nil, fmt.Errorf("user %q not found in config", forUser)
		}
		return forUser, uc, nil
	}

	if len(cfg.Users) == 1 {
		for name, uc := range cfg.Users {
			return name, uc, nil
		}
	}

	if len(cfg.Users) == 0 {
		return "", nil, fmt.Errorf("no users configured in %s/config.yaml", config.ConfigDir())
	}

	return "", nil, fmt.Errorf("multiple users configured; use --for to specify which one")
}
