package cmd

import (
	"github.com/spf13/cobra"
)

func newQACmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "qa",
		Short: "Agent-driven QA automation",
		Long:  "Run automated QA tests using AI agents in isolated Docker containers.",
	}

	cmd.AddCommand(
		newQASetupCmd(),
		newQAAuthCmd(),
		newQARunCmd(),
		newQAReportCmd(),
	)

	return cmd
}
