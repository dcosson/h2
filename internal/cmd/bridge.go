package cmd

import (
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"h2/internal/bridgeservice"
	"h2/internal/config"
	"h2/internal/session"
)

const conciergeSessionName = "concierge"

func newBridgeCmd() *cobra.Command {
	var forUser string
	var noConcierge bool

	cmd := &cobra.Command{
		Use:   "bridge [--no-concierge] -- <command> [args...]",
		Short: "Run the bridge service",
		Long: `Runs the bridge service that routes messages between external platforms
(Telegram, macOS notifications) and h2 agent sessions.

By default, also starts a concierge session (named "concierge") running the
given command and attaches to it interactively. Use --no-concierge to run
only the bridge service.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !noConcierge && len(args) == 0 {
				return fmt.Errorf("command is required (or use --no-concierge)")
			}
			if noConcierge && len(args) > 0 {
				return fmt.Errorf("cannot specify both --no-concierge and a command")
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

			var concierge string
			if !noConcierge {
				concierge = conciergeSessionName
			}

			// Fork the bridge service as a background daemon.
			fmt.Fprintf(os.Stderr, "Starting bridge service for user %q...\n", user)
			if err := bridgeservice.ForkBridge(user, concierge); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Bridge service started.\n")

			if noConcierge {
				return nil
			}

			// Fork the concierge session.
			if err := session.ForkDaemon(conciergeSessionName, uuid.New().String(), args[0], args[1:], "", ""); err != nil {
				return err
			}

			fmt.Fprintf(os.Stderr, "Agent %q started. Attaching...\n", conciergeSessionName)
			return doAttach(conciergeSessionName)
		},
	}

	cmd.Flags().StringVar(&forUser, "for", "", "Which user's bridge config to load")
	cmd.Flags().BoolVar(&noConcierge, "no-concierge", false, "Run without a concierge session")

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
		return "", nil, fmt.Errorf("no users configured in ~/.h2/config.yaml")
	}

	return "", nil, fmt.Errorf("multiple users configured; use --for to specify which one")
}
