package cmd

import (
	"github.com/spf13/cobra"

	"h2/internal/config"
)

// NewRootCmd creates the root cobra command with all subcommands.
func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "h2",
		Short: "Terminal wrapper with inter-agent messaging",
		Long:  "h2 wraps a TUI application with a persistent input bar and supports inter-agent messaging via Unix domain sockets.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			refreshTerminalColorHintsCache()

			switch cmd.Name() {
			case "init", "version", "help", "completion":
				return nil
			}
			_, err := config.ResolveDir()
			return err
		},
	}

	listCmd := newLsCmd()
	rootCmd.AddCommand(
		newRunCmd(),
		newAttachCmd(),
		newSendCmd(),
		listCmd,
		newLsAlias(listCmd),
		newShowCmd(),
		newStatusCmd(),
		newDaemonCmd(),
		newWhoamiCmd(),
		newBridgeCmd(),
		newBridgeDaemonCmd(),
		newHandleHookCmd(),
		newRoleCmd(),
		newPodCmd(),
		newSessionCmd(),
		newAuthCmd(),
		newPeekCmd(),
		newStopCmd(),
		newVersionCmd(),
		newInitCmd(),
		newQACmd(),
	)

	return rootCmd
}
