package cmd

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"h2/internal/config"
	"h2/internal/socketdir"
)

func newSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage agent sessions",
	}

	cmd.AddCommand(newSessionCleanupCmd())
	cmd.AddCommand(newSessionRestartCmd())
	cmd.AddCommand(newRotateCmd())
	return cmd
}

func newSessionRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart <agent-name>",
		Short: "Restart an agent's harness process",
		Long: `Restart the underlying harness process (claude, codex, etc.) for a running
agent. The daemon stays alive and attached terminals remain connected.

This is useful when an agent gets stuck, hits an error, or you want a
fresh harness process without losing your terminal session.

The agent's RuntimeConfig is re-read from disk, so any config changes
made since the last launch will take effect.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !isAgentRunning(name) {
				return fmt.Errorf("agent %q is not running", name)
			}
			fmt.Fprintf(cmd.OutOrStderr(), "Restarting agent %q...\n", name)
			if err := relaunchAgent(name, true); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStderr(), "Agent %q restarted.\n", name)
			return nil
		},
	}
}

func newSessionCleanupCmd() *cobra.Command {
	var dryRun bool
	var olderThan string

	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Remove stale session directories",
		Long: `Removes session directories from ~/.h2/sessions/ whose agents are no
longer running. Checks each session directory against running agent sockets.

Use --older-than to only remove sessions whose last activity exceeds the
given age. Accepts units: s (seconds), m (minutes), h (hours), d (days).

Examples:
  h2 session cleanup                    Remove all stopped sessions
  h2 session cleanup --older-than 3d    Remove sessions inactive for 3+ days
  h2 session cleanup --older-than 12h   Remove sessions inactive for 12+ hours
  h2 session cleanup --dry-run          Show what would be removed`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var minAge time.Duration
			if olderThan != "" {
				d, err := parseAge(olderThan)
				if err != nil {
					return fmt.Errorf("invalid --older-than value %q: %w", olderThan, err)
				}
				minAge = d
			}

			sessionsDir := config.SessionsDir()
			entries, err := os.ReadDir(sessionsDir)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Println("No sessions directory found.")
					return nil
				}
				return fmt.Errorf("read sessions dir: %w", err)
			}

			if len(entries) == 0 {
				fmt.Println("No session directories to clean up.")
				return nil
			}

			// Get list of running agents.
			running := make(map[string]bool)
			agents, _ := socketdir.ListByType(socketdir.TypeAgent)
			for _, a := range agents {
				if isAgentAlive(a.Path) {
					running[a.Name] = true
				}
			}

			now := time.Now()
			var removed, skipped, tooRecent int
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				name := entry.Name()
				if running[name] {
					skipped++
					continue
				}

				sessionDir := filepath.Join(sessionsDir, name)

				// Check age filter against last activity time.
				if minAge > 0 {
					la := config.SessionLastActivity(sessionDir)
					if !la.IsZero() && now.Sub(la) < minAge {
						tooRecent++
						continue
					}
				}

				if dryRun {
					fmt.Printf("  would remove: %s\n", name)
				} else {
					if err := os.RemoveAll(sessionDir); err != nil {
						fmt.Fprintf(os.Stderr, "  error removing %s: %v\n", name, err)
						continue
					}
					fmt.Printf("  removed: %s\n", name)
				}
				removed++
			}

			verb := "Cleaned up"
			if dryRun {
				verb = "Would clean up"
			}
			parts := fmt.Sprintf("%s %d session(s)", verb, removed)
			if skipped > 0 {
				parts += fmt.Sprintf(", %d still running", skipped)
			}
			if tooRecent > 0 {
				parts += fmt.Sprintf(", %d too recent", tooRecent)
			}
			fmt.Printf("\n%s.\n", parts)
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be removed without deleting")
	cmd.Flags().StringVar(&olderThan, "older-than", "", "Only remove sessions inactive longer than this (e.g. 3d, 12h, 30m)")

	return cmd
}

// agePattern matches a number followed by a time unit.
var agePattern = regexp.MustCompile(`^(\d+)\s*(s|m|h|d|seconds?|minutes?|hours?|days?)$`)

// parseAge parses a human-friendly duration string like "3d", "12h", "30m", "3 days".
func parseAge(s string) (time.Duration, error) {
	m := agePattern.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("expected format like '3d', '12h', '30m', or '3 days'")
	}
	n, _ := strconv.Atoi(m[1])
	switch m[2][0] {
	case 's':
		return time.Duration(n) * time.Second, nil
	case 'm':
		return time.Duration(n) * time.Minute, nil
	case 'h':
		return time.Duration(n) * time.Hour, nil
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("unknown unit %q", m[2])
}

// isAgentAlive checks if an agent socket is responding.
func isAgentAlive(sockPath string) bool {
	conn, err := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
