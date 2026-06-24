package cmd

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"h2/internal/bridgeservice"
	"h2/internal/config"
	"h2/internal/session/message"
	"h2/internal/socketdir"
	"h2/internal/tmpl"
)

const conciergeSessionName = "concierge"

var forkBridgeFunc = bridgeservice.ForkBridge

func newBridgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bridge",
		Short: "Manage bridge services",
		Long: `Manage bridge services that route messages between external platforms
(Telegram, macOS notifications) and h2 agent sessions.

Use "h2 bridge create" to start a new bridge, or use the subcommands
to manage running bridges.`,
	}

	createCmd := newBridgeCreateCmd()
	cmd.AddCommand(createCmd)
	cmd.AddCommand(newBridgeStopCmd())
	cmd.AddCommand(newBridgeSetConciergeCmd())
	cmd.AddCommand(newBridgeRemoveConciergeCmd())

	// If parent is invoked with flags but no subcommand, run create.
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().NFlag() > 0 {
			return createCmd.RunE(createCmd, args)
		}
		return cmd.Help()
	}

	cmd.Flags().AddFlagSet(createCmd.Flags())

	return cmd
}

func newBridgeCreateCmd() *cobra.Command {
	var bridgeName string
	var noConcierge bool
	var setConcierge string
	var conciergeRole string

	cmd := &cobra.Command{
		Use:   "create --bridge <name> [--no-concierge | --set-concierge <name>] [--concierge-role <name>]",
		Short: "Create and start a bridge service",
		Long: `Creates and starts a bridge service that routes messages between external
platforms (Telegram, macOS notifications) and h2 agent sessions.

By default, also starts a concierge session (named "concierge") using the
"concierge" role and attaches to it interactively. Use --no-concierge to run
only the bridge service with no default routing. Use --set-concierge <name>
to route to an existing agent without spawning a new session.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if bridgeName == "" {
				return fmt.Errorf("--bridge is required")
			}
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

			// Validate bridge config exists.
			if _, err := cfg.LookupBridge(bridgeName); err != nil {
				return err
			}

			// Determine concierge name for routing.
			var concierge string
			if setConcierge != "" {
				concierge = setConcierge
			} else if !noConcierge {
				concierge = conciergeSessionName
			}

			// Fork the bridge service as a background daemon.
			stopped, err := stopExistingBridgeIfRunning(bridgeName)
			if err != nil {
				return err
			}
			if stopped {
				fmt.Fprintf(os.Stderr, "Stopped existing bridge %q.\n", bridgeName)
			}
			fmt.Fprintf(os.Stderr, "Starting bridge %q...\n", bridgeName)
			if err := forkBridgeFunc(bridgeName, concierge, ""); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Bridge service started.\n")

			if noConcierge || setConcierge != "" {
				return nil
			}

			// Setup and fork the concierge session from the role.
			rootDir, _ := config.RootDir()
			ctx := &tmpl.Context{
				AgentName: conciergeSessionName,
				RoleName:  conciergeRole,
				H2Dir:     config.ConfigDir(),
				H2RootDir: rootDir,
			}
			role, err := config.LoadRoleRenderedWithFuncs(conciergeRole, ctx, config.NameStubFuncs)
			if err != nil {
				return fmt.Errorf("concierge role not found; create one with: h2 role create concierge --template concierge")
			}
			return setupAndForkAgent(conciergeSessionName, role, false, "", 0, nil, "")
		},
	}

	cmd.Flags().StringVar(&bridgeName, "bridge", "", "Named bridge config from config.yaml")
	cmd.Flags().BoolVar(&noConcierge, "no-concierge", false, "Run without a concierge session")
	cmd.Flags().StringVar(&setConcierge, "set-concierge", "", "Route to an existing concierge agent by name")
	cmd.Flags().StringVar(&conciergeRole, "concierge-role", "concierge", "Role to use for the concierge session")

	return cmd
}

// stopExistingBridgeIfRunning stops an already-running bridge daemon by name.
func stopExistingBridgeIfRunning(bridgeName string) (bool, error) {
	sockPath := socketdir.Path(socketdir.TypeBridge, bridgeName)
	conn, err := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
	if err != nil {
		return false, nil
	}
	defer conn.Close()

	if err := message.SendRequest(conn, &message.Request{Type: "stop"}); err != nil {
		return false, fmt.Errorf("stop existing bridge %q: send stop request: %w", bridgeName, err)
	}
	resp, err := message.ReadResponse(conn)
	if err != nil {
		return false, fmt.Errorf("stop existing bridge %q: read response: %w", bridgeName, err)
	}
	if !resp.OK {
		return false, fmt.Errorf("stop existing bridge %q: %s", bridgeName, resp.Error)
	}

	for i := 0; i < 50; i++ {
		if _, err := os.Stat(sockPath); os.IsNotExist(err) {
			return true, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false, fmt.Errorf("stop existing bridge %q: bridge socket still present after stop", bridgeName)
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
	var bridgeName string

	cmd := &cobra.Command{
		Use:   "set-concierge <agent-name>",
		Short: "Set or change the concierge agent for a running bridge",
		Long: `Set or change the concierge agent for a running bridge. If a concierge
is already assigned, it will be replaced. The named agent does not need
to be running yet.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName := args[0]

			resp, err := bridgeRequest(bridgeName, "set-concierge", agentName)
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

	cmd.Flags().StringVar(&bridgeName, "bridge", "", "Which bridge to target")

	return cmd
}

func newBridgeRemoveConciergeCmd() *cobra.Command {
	var bridgeName string

	cmd := &cobra.Command{
		Use:   "remove-concierge",
		Short: "Remove the concierge agent from a running bridge",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := bridgeRequest(bridgeName, "remove-concierge", "")
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

	cmd.Flags().StringVar(&bridgeName, "bridge", "", "Which bridge to target")

	return cmd
}

// bridgeRequest sends a request to a running bridge's socket and returns the response.
// If bridgeName is empty and exactly one bridge is running, it targets that bridge.
func bridgeRequest(bridgeName, reqType, body string) (*message.Response, error) {
	sockPath, err := findBridgeSocket(bridgeName)
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
