package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"h2/internal/config"
	"h2/internal/session"
)

func newDaemonCmd() *cobra.Command {
	var name string
	var sessionID string
	var roleName string
	var sessionDir string
	var instructions string
	var systemPrompt string
	var model string
	var permissionMode string
	var codexSandboxMode string
	var codexAskForApproval string
	var heartbeatIdleTimeout string
	var heartbeatMessage string
	var heartbeatCondition string
	var additionalDirs []string
	var overrides []string

	cmd := &cobra.Command{
		Use:    "_daemon --name=<name> -- <command> [args...]",
		Short:  "Run as a daemon (internal)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}

			var heartbeat session.DaemonHeartbeat
			if heartbeatIdleTimeout != "" {
				d, err := time.ParseDuration(heartbeatIdleTimeout)
				if err != nil {
					return fmt.Errorf("invalid --heartbeat-idle-timeout: %w", err)
				}
				heartbeat = session.DaemonHeartbeat{
					IdleTimeout: d,
					Message:     heartbeatMessage,
					Condition:   heartbeatCondition,
				}
			}

			// Parse override key=value strings into a map for metadata.
			var overrideMap map[string]string
			if len(overrides) > 0 {
				var err error
				overrideMap, err = config.ParseOverrides(overrides)
				if err != nil {
					return fmt.Errorf("parse overrides: %w", err)
				}
			}

			err := session.RunDaemon(session.RunDaemonOpts{
				Name:                 name,
				SessionID:            sessionID,
				Command:              args[0],
				Args:                 args[1:],
				RoleName:             roleName,
				SessionDir:           sessionDir,
				Instructions:         instructions,
				SystemPrompt:         systemPrompt,
				Model:                model,
				ClaudePermissionMode: permissionMode,
				CodexSandboxMode:     codexSandboxMode,
				CodexAskForApproval:  codexAskForApproval,
				AdditionalDirs:       additionalDirs,
				Heartbeat:            heartbeat,
				Overrides:            overrideMap,
			})
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
	cmd.Flags().StringVar(&instructions, "instructions", "", "Role instructions to pass via --append-system-prompt")
	cmd.Flags().StringVar(&systemPrompt, "system-prompt", "", "System prompt to pass via --system-prompt")
	cmd.Flags().StringVar(&model, "model", "", "Model selection to pass via --model")
	cmd.Flags().StringVar(&permissionMode, "permission-mode", "", "Claude Code permission mode (--permission-mode)")
	cmd.Flags().StringVar(&codexSandboxMode, "codex-sandbox-mode", "", "Codex sandbox mode (read-only, workspace-write, danger-full-access)")
	cmd.Flags().StringVar(&codexAskForApproval, "codex-ask-for-approval", "", "Codex ask for approval (untrusted, on-request, never)")
	cmd.Flags().StringVar(&heartbeatIdleTimeout, "heartbeat-idle-timeout", "", "Heartbeat idle timeout duration")
	cmd.Flags().StringVar(&heartbeatMessage, "heartbeat-message", "", "Heartbeat nudge message")
	cmd.Flags().StringVar(&heartbeatCondition, "heartbeat-condition", "", "Heartbeat condition command")
	cmd.Flags().StringArrayVar(&additionalDirs, "additional-dir", nil, "Additional directories (--add-dir)")
	cmd.Flags().StringArrayVar(&overrides, "override", nil, "Override key=value pairs (internal)")

	return cmd
}
