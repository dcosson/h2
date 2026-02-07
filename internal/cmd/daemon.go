package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"h2/internal/session"
)

func newDaemonCmd() *cobra.Command {
	var name string
	var sessionID string

	cmd := &cobra.Command{
		Use:    "_daemon --name=<name> -- <command> [args...]",
		Short:  "Run as a daemon (internal)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}

			err := session.RunDaemon(name, sessionID, args[0], args[1:])
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

	return cmd
}
