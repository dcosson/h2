package cmd

import (
	"context"
	"fmt"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"h2/internal/bridgeservice"
	"h2/internal/config"
	"h2/internal/socketdir"
)

func newBridgeDaemonCmd() *cobra.Command {
	var bridgeName string
	var concierge string
	var pod string

	cmd := &cobra.Command{
		Use:    "_bridge-service",
		Short:  "Run the bridge service daemon (internal)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if bridgeName == "" {
				return fmt.Errorf("--bridge is required")
			}

			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			bc, err := cfg.LookupBridge(bridgeName)
			if err != nil {
				return err
			}

			bridges := bridgeservice.FromConfig(bc, filepath.Join(config.ConfigDir(), "attachments", bridgeName))
			if len(bridges) == 0 {
				return fmt.Errorf("no bridges configured for %q", bridgeName)
			}

			var allowedCommands []string
			var opts bridgeservice.ServiceOpts
			if bc.Telegram != nil {
				allowedCommands = bc.Telegram.AllowedCommands
				opts.ExpectsResponse = bc.Telegram.ExpectsResponse
			}

			svc := bridgeservice.New(bridges, bridgeName, concierge, pod, socketdir.Dir(), allowedCommands, opts)

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			return svc.Run(ctx)
		},
	}

	cmd.Flags().StringVar(&bridgeName, "bridge", "", "Named bridge config to load")
	cmd.Flags().StringVar(&concierge, "concierge", "", "Concierge session name")
	cmd.Flags().StringVar(&pod, "pod", "", "Pod name this bridge belongs to")

	return cmd
}
