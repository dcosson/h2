package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"h2/internal/config"
	"h2/internal/session"
)

func newForkCmd() *cobra.Command {
	var detach bool

	cmd := &cobra.Command{
		Use:   "fork <agent-name>",
		Short: "Fork an agent's session into a new agent",
		Long: `Fork an agent's conversation into a new, independent agent.

The parent's conversation history is copied to a new session; the parent
keeps running, unaffected. The fork gets a new name derived from the parent
(e.g. fond-birch -> fond-birch-fork1), does not join the parent's pod, and
resumes from the copied history.

Equivalent to 'h2 run --fork-from <agent-name>', which additionally accepts
an explicit name for the fork. Also available from an attached terminal via
the menu (Ctrl+\, then f), which starts the fork in the background and stays
on the current session; reach the fork via the agent navigator (menu a) or
h2 attach.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFork(cmd.OutOrStderr(), args[0], "", detach)
		},
	}

	cmd.Flags().BoolVar(&detach, "detach", false, "Don't auto-attach after forking")
	return cmd
}

// runFork forks parentName's session into newName (auto-derived when empty),
// launches the fork's daemon resuming the copied conversation, and attaches
// unless detach is set (or stdin is not a terminal). Shared by
// 'h2 session fork' and 'h2 run --fork-from'.
func runFork(out io.Writer, parentName, newName string, detach bool) error {
	// Auto-detach when stdin is not a terminal (e.g. running through
	// a bridge, pipe, or inside a Claude Code session).
	if !detach && !term.IsTerminal(int(os.Stdin.Fd())) {
		detach = true
	}

	sessionDir := config.SessionDir(parentName)
	rc, err := config.ReadRuntimeConfig(sessionDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no session found for agent %q", parentName)
		}
		return fmt.Errorf("read session config for %q: %w", parentName, err)
	}

	forked, forkedDir, err := session.ForkSessionFiles(rc, newName)
	if err != nil {
		return err
	}

	colorHints := detectTerminalHints()
	if err := forkDaemonFunc(forkedDir, session.TerminalHints{
		OscFg:     colorHints.OscFg,
		OscBg:     colorHints.OscBg,
		ColorFGBG: colorHints.ColorFGBG,
		Term:      colorHints.Term,
		ColorTerm: colorHints.ColorTerm,
	}, true); err != nil {
		return fmt.Errorf("launch forked daemon: %w", err)
	}

	fmt.Fprintf(out, "Forked agent %q to %q.\n", parentName, forked.AgentName)
	if detach {
		fmt.Fprintf(out, "Use 'h2 attach %s' to connect.\n", forked.AgentName)
		return nil
	}
	fmt.Fprintf(out, "Attaching...\n")
	return doAttach(forked.AgentName)
}
