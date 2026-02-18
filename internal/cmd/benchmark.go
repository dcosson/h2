package cmd

import (
	"github.com/spf13/cobra"

	"h2/benchmarks/swe_bench_pro"
)

func newBenchmarkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "benchmark",
		Short: "Run and manage coding benchmarks",
		Long:  "Run standardized coding benchmarks against h2 agent configurations.",
	}

	cmd.AddCommand(
		swe_bench_pro.NewCommand(),
		swe_bench_pro.NewReportCommand(),
	)

	return cmd
}
