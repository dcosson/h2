package cmd

import (
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"h2/internal/config"
	"h2/internal/session"
)

func newRunCmd() *cobra.Command {
	var name string
	var detach bool
	var roleName string

	cmd := &cobra.Command{
		Use:   "run [--name=<name>] [--role=<role>] [--detach] [-- <command> [args...]]",
		Short: "Start a new agent",
		Long: `Fork a daemon process running the given command, then attach to it.

If --role is specified, the agent is configured from ~/.h2/roles/<role>.yaml
and the command defaults to 'claude'. If --name is omitted, it defaults to
the role name.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var command string
			var cmdArgs []string
			var sessionDir string

			if roleName != "" {
				// Load role and set up session directory.
				role, err := config.LoadRole(roleName)
				if err != nil {
					return fmt.Errorf("load role %q: %w", roleName, err)
				}

				if name == "" {
					name = role.Name
				}

				dir, err := config.SetupSessionDir(name, role)
				if err != nil {
					return fmt.Errorf("setup session dir: %w", err)
				}
				sessionDir = dir

				// Default command to claude when using a role.
				if len(args) > 0 {
					command = args[0]
					cmdArgs = args[1:]
				} else {
					command = "claude"
				}
			} else {
				// No role — require command args.
				if len(args) == 0 {
					return fmt.Errorf("command is required (or use --role)")
				}
				command = args[0]
				cmdArgs = args[1:]
			}

			if name == "" {
				name = session.GenerateName()
			}

			sessionID := uuid.New().String()

			// Fork a daemon process.
			if err := session.ForkDaemon(name, sessionID, command, cmdArgs, roleName, sessionDir); err != nil {
				return err
			}

			if detach {
				fmt.Fprintf(os.Stderr, "Agent %q started (detached). Use 'h2 attach %s' to connect.\n", name, name)
				return nil
			}

			fmt.Fprintf(os.Stderr, "Agent %q started. Attaching...\n", name)
			return doAttach(name)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Agent name (auto-generated if omitted)")
	cmd.Flags().BoolVar(&detach, "detach", false, "Don't auto-attach after starting")
	cmd.Flags().StringVar(&roleName, "role", "", "Role to use from ~/.h2/roles/")

	return cmd
}
