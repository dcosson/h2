package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"h2/internal/session"
)

func newRunCmd() *cobra.Command {
	var name string
	var detach bool

	cmd := &cobra.Command{
		Use:   "run [--name=<name>] [--detach] -- <command> [args...]",
		Short: "Start a new agent",
		Long:  "Fork a daemon process running the given command, then attach to it. If --name is omitted, a random name is generated.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				name = session.GenerateName()
			}

			// Fork a daemon process.
			if err := session.ForkDaemon(name, args[0], args[1:]); err != nil {
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

	return cmd
}
