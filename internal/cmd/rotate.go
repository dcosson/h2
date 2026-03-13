package cmd

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"h2/internal/config"
	"h2/internal/session"
	"h2/internal/session/message"
	"h2/internal/socketdir"
)

func newRotateCmd() *cobra.Command {
	var live bool

	cmd := &cobra.Command{
		Use:   "rotate <agent-name> <profile>",
		Short: "Rotate an agent to a different profile",
		Long: `Rotate an agent's profile by updating its session metadata and moving
its session log to the new profile's Claude config directory.

The agent must not be running unless --live is specified. The target profile
must exist for the agent's harness type.

With --live, the agent is stopped, rotated, and resumed with --detach.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName := args[0]
			newProfile := args[1]

			// Read the agent's RuntimeConfig.
			sessionDir := config.SessionDir(agentName)
			rc, err := config.ReadRuntimeConfig(sessionDir)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("no session found for agent %q", agentName)
				}
				return fmt.Errorf("read session config for %q: %w", agentName, err)
			}

			// Check if already on the target profile.
			currentProfile := rc.Profile
			if currentProfile == "" {
				currentProfile = "default"
			}
			if currentProfile == newProfile {
				return fmt.Errorf("agent %q is already using profile %q", agentName, newProfile)
			}

			// Validate the target profile exists for this harness type.
			if rc.HarnessConfigPathPrefix == "" {
				return fmt.Errorf("agent %q has no harness config path prefix; cannot rotate profile", agentName)
			}
			newProfileDir := filepath.Join(rc.HarnessConfigPathPrefix, newProfile)
			info, err := os.Stat(newProfileDir)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("profile %q not found for harness %q (expected %s); use 'h2 profile create %s' first",
						newProfile, rc.HarnessType, newProfileDir, newProfile)
				}
				return fmt.Errorf("stat profile dir: %w", err)
			}
			if !info.IsDir() {
				return fmt.Errorf("profile path is not a directory: %s", newProfileDir)
			}

			// Check if agent is running.
			running := isAgentRunning(agentName)

			if running && !live {
				return fmt.Errorf("agent %q is running; use --live to stop, rotate, and resume, or stop it first with 'h2 stop %s'",
					agentName, agentName)
			}

			// --live: stop the running agent.
			if running && live {
				fmt.Fprintf(cmd.OutOrStderr(), "Stopping agent %q...\n", agentName)
				if err := stopAgentByName(agentName); err != nil {
					return fmt.Errorf("stop agent: %w", err)
				}
				// Wait for socket cleanup.
				waitForAgentStop(agentName, 5*time.Second)
			}

			// Move the session log from old profile to new profile.
			if err := moveSessionLog(rc, currentProfile, newProfile); err != nil {
				// Non-fatal — log the error but continue with the metadata update.
				fmt.Fprintf(cmd.OutOrStderr(), "Warning: could not move session log: %v\n", err)
			}

			// Update the profile in RuntimeConfig and write it back.
			rc.Profile = newProfile
			if err := config.WriteRuntimeConfig(sessionDir, rc); err != nil {
				return fmt.Errorf("update session metadata: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStderr(), "Rotated agent %q from profile %q to %q.\n", agentName, currentProfile, newProfile)

			// --live: resume the agent.
			if live {
				fmt.Fprintf(cmd.OutOrStderr(), "Resuming agent %q...\n", agentName)
				colorHints := detectTerminalHints()
				if err := forkDaemonFunc(sessionDir, session.TerminalHints{
					OscFg:     colorHints.OscFg,
					OscBg:     colorHints.OscBg,
					ColorFGBG: colorHints.ColorFGBG,
					Term:      colorHints.Term,
					ColorTerm: colorHints.ColorTerm,
				}, true); err != nil {
					return fmt.Errorf("resume agent: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStderr(), "Agent %q resumed (detached). Use 'h2 attach %s' to connect.\n", agentName, agentName)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&live, "live", false, "Stop running agent, rotate, and resume with --detach")

	return cmd
}

// isAgentRunning checks if an agent has a live socket without cleaning up stale sockets.
func isAgentRunning(name string) bool {
	sockPath := socketdir.Path(socketdir.TypeAgent, name)
	if _, err := os.Stat(sockPath); err != nil {
		return false
	}
	conn, err := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// stopAgentByName sends a stop request to a running agent via its socket.
func stopAgentByName(name string) error {
	sockPath, err := socketdir.Find(name)
	if err != nil {
		return err
	}
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("connect to %q: %w", name, err)
	}
	defer conn.Close()

	if err := message.SendRequest(conn, &message.Request{Type: "stop"}); err != nil {
		return fmt.Errorf("send stop: %w", err)
	}
	resp, err := message.ReadResponse(conn)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("stop failed: %s", resp.Error)
	}
	return nil
}

// waitForAgentStop polls until the agent socket disappears or timeout.
func waitForAgentStop(name string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !isAgentRunning(name) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// moveSessionLog moves the harness's native session log from the old profile
// directory to the new one. Uses the RuntimeConfig's NativeLogPathSuffix to
// compute the correct path. Returns nil if the harness has no native logs.
func moveSessionLog(rc *config.RuntimeConfig, oldProfile, newProfile string) error {
	if rc.HarnessConfigPathPrefix == "" || rc.NativeLogPathSuffix == "" {
		return nil
	}

	oldConfigDir := filepath.Join(rc.HarnessConfigPathPrefix, oldProfile)
	newConfigDir := filepath.Join(rc.HarnessConfigPathPrefix, newProfile)

	oldLogPath := rc.NativeSessionLogPathWithConfigDir(oldConfigDir)
	if oldLogPath == "" {
		return nil
	}
	newLogPath := rc.NativeSessionLogPathWithConfigDir(newConfigDir)

	// Check if the old log exists.
	if _, err := os.Stat(oldLogPath); err != nil {
		return nil // no log to move
	}

	// Ensure the destination directory exists.
	if err := os.MkdirAll(filepath.Dir(newLogPath), 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	if err := os.Rename(oldLogPath, newLogPath); err != nil {
		return fmt.Errorf("move %s → %s: %w", oldLogPath, newLogPath, err)
	}

	return nil
}
