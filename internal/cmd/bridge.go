package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"h2/internal/bridgeservice"
	"h2/internal/config"
	"h2/internal/tmpl"
)

const conciergeSessionName = "concierge"

func newBridgeCmd() *cobra.Command {
	var forUser string
	var noConcierge bool
	var setConcierge string
	var roleName string

	cmd := &cobra.Command{
		Use:   "bridge [--no-concierge | --set-concierge <name>] [--role <name>]",
		Short: "Run the bridge service",
		Long: `Runs the bridge service that routes messages between external platforms
(Telegram, macOS notifications) and h2 agent sessions.

By default, also starts a concierge session (named "concierge") using the
"concierge" role and attaches to it interactively. Use --no-concierge to run
only the bridge service with no default routing. Use --set-concierge <name>
to route to an existing agent without spawning a new session.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if noConcierge && setConcierge != "" {
				return fmt.Errorf("cannot specify both --no-concierge and --set-concierge")
			}
			if cmd.Flags().Changed("role") && (noConcierge || setConcierge != "") {
				return fmt.Errorf("--role can only be used when launching a new concierge session")
			}
			if setConcierge != "" {
				// Not launching a new concierge, so no command/args needed.
			} else if !noConcierge && roleName == "" {
				return fmt.Errorf("--role is required when launching a new concierge session")
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
			rootDir, _ := config.RootDir()
			ctx := &tmpl.Context{
				AgentName: conciergeSessionName,
				RoleName:  roleName,
				H2Dir:     config.ConfigDir(),
				H2RootDir: rootDir,
			}
			role, err := config.LoadRoleRenderedWithFuncs(roleName, ctx, config.NameStubFuncs)
			if err != nil {
				return fmt.Errorf("concierge role not found; create one with: h2 role init concierge")
			}
			return setupAndForkAgent(conciergeSessionName, role, false, "", nil)
		},
	}

	cmd.Flags().StringVar(&forUser, "for", "", "Which user's bridge config to load")
	cmd.Flags().BoolVar(&noConcierge, "no-concierge", false, "Run without a concierge session")
	cmd.Flags().StringVar(&setConcierge, "set-concierge", "", "Route to an existing concierge agent by name")
	cmd.Flags().StringVar(&roleName, "role", "concierge", "Role to use for the concierge session")

	return cmd
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
