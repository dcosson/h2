package cmd

import (
	"github.com/spf13/cobra"

	"h2/benchmarks/ace_bench"
	"h2/benchmarks/commit0"
	"h2/benchmarks/re_bench"
	"h2/benchmarks/swe_bench_pro"
	"h2/benchmarks/swe_bench_verified"
	"h2/benchmarks/swe_evo"
	"h2/benchmarks/terminal_bench"
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
		ace_bench.NewCommand(),
		swe_evo.NewCommand(),
		commit0.NewCommand(),
		re_bench.NewCommand(),
		terminal_bench.NewCommand(),
		swe_bench_verified.NewCommand(),
	)

	return cmd
}
