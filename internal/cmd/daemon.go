package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"h2/internal/session"
)

func newDaemonCmd() *cobra.Command {
	var name string
	var sessionID string
	var roleName string
	var sessionDir string
	var claudeConfigDir string
	var keepaliveIdleTimeout string
	var keepaliveMessage string
	var keepaliveCondition string

	cmd := &cobra.Command{
		Use:    "_daemon --name=<name> -- <command> [args...]",
		Short:  "Run as a daemon (internal)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}

			var keepalive session.DaemonKeepalive
			if keepaliveIdleTimeout != "" {
				d, err := time.ParseDuration(keepaliveIdleTimeout)
				if err != nil {
					return fmt.Errorf("invalid --keepalive-idle-timeout: %w", err)
				}
				keepalive = session.DaemonKeepalive{
					IdleTimeout: d,
					Message:     keepaliveMessage,
					Condition:   keepaliveCondition,
				}
			}

			err := session.RunDaemon(name, sessionID, args[0], args[1:], roleName, sessionDir, claudeConfigDir, keepalive)
			if err != nil {
				if _, ok := err.(*exec.ExitError); ok {
					os.Exit(1)
				}
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Agent name")
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Claude Code session ID")
	cmd.Flags().StringVar(&roleName, "role", "", "Role name")
	cmd.Flags().StringVar(&sessionDir, "session-dir", "", "Session directory path")
	cmd.Flags().StringVar(&claudeConfigDir, "claude-config-dir", "", "Claude config directory")
	cmd.Flags().StringVar(&keepaliveIdleTimeout, "keepalive-idle-timeout", "", "Keepalive idle timeout duration")
	cmd.Flags().StringVar(&keepaliveMessage, "keepalive-message", "", "Keepalive nudge message")
	cmd.Flags().StringVar(&keepaliveCondition, "keepalive-condition", "", "Keepalive condition command")

	return cmd
}
