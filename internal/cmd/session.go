package cmd

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
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
	return cmd
}

func newSessionCleanupCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Remove stale session directories",
		Long: `Removes session directories from ~/.h2/sessions/ whose agents are no
longer running. Checks each session directory against running agent sockets.`,
		RunE: func(cmd *cobra.Command, args []string) error {
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

			var removed, skipped int
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				name := entry.Name()
				if running[name] {
					skipped++
					continue
				}

				path := filepath.Join(sessionsDir, name)
				if dryRun {
					fmt.Printf("  would remove: %s\n", path)
				} else {
					if err := os.RemoveAll(path); err != nil {
						fmt.Fprintf(os.Stderr, "  error removing %s: %v\n", path, err)
						continue
					}
					fmt.Printf("  removed: %s\n", path)
				}
				removed++
			}

			if dryRun {
				fmt.Printf("\nDry run: %d session(s) would be removed, %d still running.\n", removed, skipped)
			} else {
				fmt.Printf("\nCleaned up %d session(s), %d still running.\n", removed, skipped)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be removed without deleting")

	return cmd
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
