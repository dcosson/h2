package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"h2/benchmarks/ace_bench"
	"h2/benchmarks/commit0"
	"h2/benchmarks/re_bench"
	"h2/benchmarks/runner"
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
		ace_bench.NewCommand(),
		swe_evo.NewCommand(),
		commit0.NewCommand(),
		re_bench.NewCommand(),
		terminal_bench.NewCommand(),
		swe_bench_verified.NewCommand(),
		newBenchmarkReportCmd(),
	)

	return cmd
}

func newBenchmarkReportCmd() *cobra.Command {
	var (
		resultsDir string
		outputJSON bool
	)

	cmd := &cobra.Command{
		Use:   "report [results-dir...]",
		Short: "Compare results across benchmark runs",
		Long: `Generate a cross-benchmark comparison report from results directories.

If no arguments are given, scans the default results directory (benchmarks/results/).
If specific directories are given, loads those runs directly.

Output includes:
  - Comparison table: benchmark, mode, preset, resolve rate, cost, time
  - Per-task breakdown: tasks that appear in multiple runs for direct comparison
  - Pareto analysis: cost-performance frontier showing optimal configurations

Examples:
  # Auto-discover all runs
  h2 benchmark report

  # Compare specific runs
  h2 benchmark report benchmarks/results/swe_bench_pro/20260217-120000 benchmarks/results/swe_bench_pro/20260217-130000

  # JSON output for tooling
  h2 benchmark report --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var runs []runner.RunReport

			if len(args) > 0 {
				// Load specific run directories.
				for _, dir := range args {
					run, err := runner.LoadRun(dir)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "warning: skipping %s: %v\n", dir, err)
						continue
					}
					runs = append(runs, *run)
				}
			} else {
				// Auto-discover from results dir.
				baseDir := resultsDir
				if baseDir == "" {
					baseDir = filepath.Join("benchmarks", "results")
				}
				if _, err := os.Stat(baseDir); os.IsNotExist(err) {
					return fmt.Errorf("results directory %s does not exist (run some benchmarks first)", baseDir)
				}
				discovered, err := runner.DiscoverRuns(baseDir)
				if err != nil {
					return fmt.Errorf("discover runs: %w", err)
				}
				runs = discovered
			}

			if len(runs) == 0 {
				return fmt.Errorf("no benchmark results found")
			}

			report := runner.BuildReport(runs)

			w := cmd.OutOrStdout()
			if outputJSON {
				return runner.WriteReportJSON(w, report)
			}

			runner.PrintFullReport(w, report)

			// Show mode gains if applicable.
			gains := runner.ModeGain(runs)
			if len(gains) > 0 {
				fmt.Fprintf(w, "Baseline → H2 Gains\n")
				for label, gain := range gains {
					fmt.Fprintf(w, "  %s: %+.1f%%\n", label, gain*100)
				}
				fmt.Fprintln(w)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&resultsDir, "results-dir", "", "Base results directory (default: benchmarks/results/)")
	cmd.Flags().BoolVar(&outputJSON, "json", false, "Output as JSON")

	return cmd
}
