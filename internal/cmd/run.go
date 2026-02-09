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
	var agentType string
	var command string

	cmd := &cobra.Command{
		Use:   "run [flags]",
		Short: "Start a new agent",
		Long: `Start a new agent, optionally configured from a role.

By default, uses the "default" role from ~/.h2/roles/default.yaml.

  h2 run                        Use the default role
  h2 run --role concierge       Use a specific role
  h2 run --agent-type claude    Run an agent type without a role
  h2 run --command "vim"        Run an explicit command`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var cmdCommand string
			var cmdArgs []string
			var sessionDir string
			var claudeConfigDir string
			var keepalive session.DaemonKeepalive

			// Check mutual exclusivity of mode flags.
			modeFlags := 0
			if cmd.Flags().Changed("role") {
				modeFlags++
			}
			if cmd.Flags().Changed("agent-type") {
				modeFlags++
			}
			if cmd.Flags().Changed("command") {
				modeFlags++
			}
			if modeFlags > 1 {
				return fmt.Errorf("--role, --agent-type, and --command are mutually exclusive")
			}

			if cmd.Flags().Changed("agent-type") {
				// Run agent type without a role.
				cmdCommand = agentType
			} else if cmd.Flags().Changed("command") {
				// Run explicit command without a role.
				cmdCommand = command
				cmdArgs = args
			} else {
				// Use a role (specified or default).
				if roleName == "" {
					roleName = "default"
				}

				role, err := config.LoadRole(roleName)
				if err != nil {
					if roleName == "default" {
						return fmt.Errorf("no default role found; create one with 'h2 role init default' or specify --role, --agent-type, or --command")
					}
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

				// Get the Claude config dir and ensure it exists with h2 hooks.
				claudeConfigDir = role.GetClaudeConfigDir()
				if claudeConfigDir != "" {
					if err := config.EnsureClaudeConfigDir(claudeConfigDir); err != nil {
						return fmt.Errorf("ensure claude config dir: %w", err)
					}
				}

				cmdCommand = role.GetAgentType()

				if role.Keepalive != nil {
					d, err := role.Keepalive.ParseIdleTimeout()
					if err != nil {
						return fmt.Errorf("invalid keepalive idle_timeout: %w", err)
					}
					keepalive = session.DaemonKeepalive{
						IdleTimeout: d,
						Message:     role.Keepalive.Message,
						Condition:   role.Keepalive.Condition,
					}
				}
			}

			if name == "" {
				name = session.GenerateName()
			}

			sessionID := uuid.New().String()

			// Fork a daemon process.
			if err := session.ForkDaemon(name, sessionID, cmdCommand, cmdArgs, roleName, sessionDir, claudeConfigDir, keepalive); err != nil {
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
	cmd.Flags().StringVar(&roleName, "role", "", "Role to use (defaults to 'default')")
	cmd.Flags().StringVar(&agentType, "agent-type", "", "Agent type to run without a role (e.g. claude)")
	cmd.Flags().StringVar(&command, "command", "", "Explicit command to run without a role")

	return cmd
}
