package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"h2/internal/version"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the h2 version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(cmd.OutOrStdout(), version.DisplayVersion())
		},
	}
}
