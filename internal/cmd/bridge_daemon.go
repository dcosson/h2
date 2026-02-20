package cmd

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"h2/internal/bridgeservice"
	"h2/internal/config"
	"h2/internal/socketdir"
)

func newBridgeDaemonCmd() *cobra.Command {
	var forUser string
	var concierge string

	cmd := &cobra.Command{
		Use:    "_bridge-service",
		Short:  "Run the bridge service daemon (internal)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			user, userCfg, err := resolveUser(cfg, forUser)
			if err != nil {
				return err
			}

			bridges := bridgeservice.FromConfig(&userCfg.Bridges)
			if len(bridges) == 0 {
				return fmt.Errorf("no bridges configured for user %q", user)
			}

			var allowedCommands []string
			if userCfg.Bridges.Telegram != nil {
				allowedCommands = userCfg.Bridges.Telegram.AllowedCommands
			}

			svc := bridgeservice.New(bridges, concierge, socketdir.Dir(), user, allowedCommands)

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			return svc.Run(ctx)
		},
	}

	cmd.Flags().StringVar(&forUser, "for", "", "Which user's bridge config to load")
	cmd.Flags().StringVar(&concierge, "concierge", "", "Concierge session name")

	return cmd
}
