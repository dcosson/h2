package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"h2/internal/config"
	"h2/internal/gateway"
	"h2/internal/socketdir"
)

func newGatewayCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gateway",
		Short: "Manage the h2 gateway process",
	}
	cmd.AddCommand(
		newGatewayRunCmd(),
		newGatewayStartCmd(),
		newGatewayStatusCmd(),
	)
	return cmd
}

func newGatewayRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the h2 gateway in the foreground",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stop()

			server := gateway.NewServer(gateway.ServerOpts{
				H2Dir:      config.ConfigDir(),
				SocketPath: socketdir.GatewayPath(),
			})
			if err := server.Run(ctx); err != nil {
				return fmt.Errorf("run gateway: %w", err)
			}
			return nil
		},
	}
	return cmd
}

func newGatewayStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the h2 gateway in the background if needed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			health, err := gateway.EnsureRunning(cmd.Context(), gateway.EnsureOpts{
				H2Dir:      config.ConfigDir(),
				SocketPath: socketdir.GatewayPath(),
			})
			if err != nil {
				return fmt.Errorf("gateway start: %w", err)
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(health)
		},
	}
	return cmd
}

func newGatewayStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print gateway status as JSON",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			health, err := gateway.HealthCheck(cmd.Context(), socketdir.GatewayPath())
			if err != nil {
				return fmt.Errorf("gateway status: %w", err)
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(health)
		},
	}
	return cmd
}
