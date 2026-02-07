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

	cmd := &cobra.Command{
		Use:    "_daemon --name=<name> -- <command> [args...]",
		Short:  "Run as a daemon (internal)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}

			d := &session.Daemon{
				Name:    name,
				Command: args[0],
				Args:    args[1:],
			}

			err := d.Run()
			if err != nil {
				if d.Client != nil && d.Client.Quit {
					return nil
				}
				if _, ok := err.(*exec.ExitError); ok {
					os.Exit(1)
				}
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Agent name")

	return cmd
}
