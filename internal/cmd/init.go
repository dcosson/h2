package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"h2/internal/config"
)

const defaultConfigYAML = `# h2 configuration
# See https://github.com/dcosson/h2 for documentation.
#
# users:
#   yourname:
#     bridges:
#       telegram:
#         bot_token: "123456:ABC-DEF"
#         chat_id: 789
#       macos_notify:
#         enabled: true
`

func newInitCmd() *cobra.Command {
	var global bool
	var prefix string

	cmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Initialize an h2 directory",
		Long:  "Create an h2 directory with the standard structure. Use --global or omit dir to initialize ~/.h2/.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var dir string
			switch {
			case global || len(args) == 0:
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("cannot determine home directory: %w", err)
				}
				dir = filepath.Join(home, ".h2")
			default:
				dir = args[0]
			}

			abs, err := filepath.Abs(dir)
			if err != nil {
				return err
			}

			if config.IsH2Dir(abs) {
				return fmt.Errorf("%s is already an h2 directory", abs)
			}

			dirs := []string{
				abs,
				filepath.Join(abs, "roles"),
				filepath.Join(abs, "sessions"),
				filepath.Join(abs, "sockets"),
				filepath.Join(abs, "claude-config", "default"),
				filepath.Join(abs, "projects"),
				filepath.Join(abs, "worktrees"),
				filepath.Join(abs, "pods", "roles"),
				filepath.Join(abs, "pods", "templates"),
			}
			for _, d := range dirs {
				if err := os.MkdirAll(d, 0o755); err != nil {
					return fmt.Errorf("create directory %s: %w", d, err)
				}
			}

			if err := config.WriteMarker(abs); err != nil {
				return fmt.Errorf("write marker: %w", err)
			}

			configPath := filepath.Join(abs, "config.yaml")
			if err := os.WriteFile(configPath, []byte(defaultConfigYAML), 0o644); err != nil {
				return fmt.Errorf("write config.yaml: %w", err)
			}

			// Register this h2 directory in the routes registry.
			rootDir, err := config.RootDir()
			if err != nil {
				return fmt.Errorf("resolve root h2 dir: %w", err)
			}

			// Determine explicit prefix (if provided) and register atomically.
			var explicitPrefix string
			if cmd.Flags().Changed("prefix") {
				explicitPrefix = prefix
			}

			resolvedPrefix, err := config.RegisterRouteWithAutoPrefix(rootDir, explicitPrefix, abs)
			if err != nil {
				return fmt.Errorf("register route: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Initialized h2 directory at %s (prefix: %s)\n", abs, resolvedPrefix)
			return nil
		},
	}

	cmd.Flags().BoolVar(&global, "global", false, "Initialize ~/.h2/ as the h2 directory")
	cmd.Flags().StringVar(&prefix, "prefix", "", "Custom prefix for this h2 directory in the routes registry")
	return cmd
}
