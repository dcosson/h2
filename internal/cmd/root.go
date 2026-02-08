package cmd

import (
	"github.com/spf13/cobra"
)

// NewRootCmd creates the root cobra command with all subcommands.
func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "h2",
		Short: "Terminal wrapper with inter-agent messaging",
		Long:  "h2 wraps a TUI application with a persistent input bar and supports inter-agent messaging via Unix domain sockets.",
	}

	listCmd := newLsCmd()
	rootCmd.AddCommand(
		newRunCmd(),
		newAttachCmd(),
		newSendCmd(),
		listCmd,
		newLsAlias(listCmd),
		newShowCmd(),
		newDaemonCmd(),
		newWhoamiCmd(),
		newBridgeCmd(),
		newBridgeDaemonCmd(),
		newHookCmd(),
		newRoleCmd(),
		newPermissionRequestCmd(),
		newSessionCmd(),
	)

	return rootCmd
}
