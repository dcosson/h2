package swe_bench_pro

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"h2/benchmarks/runner"
)

const (
	defaultTimeout     = 30 * time.Minute
	defaultConcurrency = 1
	defaultMaxTurns    = 100
)

// NewCommand creates the SWE-bench Pro benchmark command.
func NewCommand() *cobra.Command {
	var (
		datasetPath string
		mode        string
		preset      string
		podTemplate string
		sandboxName string
		tasks       []string
		timeout     time.Duration
		concurrency int
		maxTurns    int
	)

	cmd := &cobra.Command{
		Use:   "swe-bench-pro",
		Short: "Run SWE-bench Pro benchmark",
		Long: `Run the SWE-bench Pro benchmark against h2 agent configurations.

SWE-bench Pro contains 731 real-world software engineering tasks from 11
open-source repositories. Each task requires fixing a GitHub issue, evaluated
by applying test patches and checking both fail-to-pass (issue fixed) and
pass-to-pass (no regressions) criteria.

Examples:
  # Run baseline (single agent) on all tasks
  h2 benchmark swe-bench-pro --sandbox bench-1

  # Run h2 mode (multi-agent) on specific tasks
  h2 benchmark swe-bench-pro --mode h2 --sandbox bench-1 --tasks django__django-16408,sympy__sympy-24152

  # Run with custom dataset file
  h2 benchmark swe-bench-pro --dataset ./my-subset.json --sandbox bench-1`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load dataset.
			if datasetPath == "" {
				datasetPath = DefaultDatasetPath()
			}
			dataset, err := LoadDataset(datasetPath)
			if err != nil {
				return fmt.Errorf("load dataset: %w", err)
			}

			// Filter instances.
			instances := dataset.Filter(tasks)
			if len(instances) == 0 {
				return fmt.Errorf("no instances match the filter (dataset has %d instances)", len(dataset.Instances))
			}

			fmt.Fprintf(cmd.OutOrStdout(), "SWE-bench Pro: %d tasks selected\n", len(instances))

			// Build runner config.
			runMode := runner.ModeBaseline
			if mode == "h2" {
				runMode = runner.ModeH2
			}

			config := runner.BenchmarkConfig{
				Name:        "swe_bench_pro",
				Mode:        runMode,
				Preset:      preset,
				PodTemplate: podTemplate,
				TaskFilter:  tasks,
				Timeout:     timeout,
				Concurrency: concurrency,
				MaxTurns:    maxTurns,
			}

			// Convert instances to benchmark tasks.
			benchTasks := ToBenchmarkTasks(instances)

			// Create and configure runner.
			r := runner.NewRunner(config, sandboxName)

			// For h2 mode, set up the pod runner.
			if runMode == runner.ModeH2 {
				podRunner := runner.NewPodRunner(podTemplate)
				r.RunAgent = podRunner.RunAgent
			}

			// Run benchmark.
			results, err := r.RunAll(context.Background(), benchTasks)
			if err != nil {
				return fmt.Errorf("benchmark run: %w", err)
			}

			// Print summary.
			summary := runner.Summarize(config, results)
			fmt.Fprintln(cmd.OutOrStdout(), runner.FormatSummary(summary))

			return nil
		},
	}

	cmd.Flags().StringVar(&datasetPath, "dataset", "", "Path to dataset JSON (default: built-in)")
	cmd.Flags().StringVar(&mode, "mode", "baseline", "Run mode: baseline or h2")
	cmd.Flags().StringVar(&preset, "preset", "opus", "Sandbox preset")
	cmd.Flags().StringVar(&podTemplate, "pod-template", "benchmark", "Pod template for h2 mode")
	cmd.Flags().StringVar(&sandboxName, "sandbox", "", "Sandbox name (required)")
	cmd.Flags().StringSliceVar(&tasks, "tasks", nil, "Run only these task IDs (comma-separated)")
	cmd.Flags().DurationVar(&timeout, "timeout", defaultTimeout, "Per-task timeout")
	cmd.Flags().IntVar(&concurrency, "concurrency", defaultConcurrency, "Parallel task count")
	cmd.Flags().IntVar(&maxTurns, "max-turns", defaultMaxTurns, "Max agent turns per task")
	_ = cmd.MarkFlagRequired("sandbox")

	return cmd
}

// NewReportCommand creates the benchmark report command.
func NewReportCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "report <results-dir> [results-dir...]",
		Short: "Display benchmark results comparison",
		Long: `Show a comparison table of benchmark results from one or more run directories.

Example:
  h2 benchmark report benchmarks/results/swe_bench_pro/20260217-143000 benchmarks/results/swe_bench_pro/20260217-150000`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var summaries []runner.RunSummary

			for _, dir := range args {
				s, err := runner.LoadSummary(dir)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: skipping %s: %v\n", dir, err)
					continue
				}
				summaries = append(summaries, *s)
			}

			if len(summaries) == 0 {
				return fmt.Errorf("no valid results found")
			}

			PrintReport(cmd.OutOrStdout(), summaries)
			return nil
		},
	}

	return cmd
}

// PrintReport outputs a comparison table of benchmark results.
func PrintReport(w io.Writer, summaries []runner.RunSummary) {
	fmt.Fprintf(w, "\n%-12s %-10s %-8s %10s %10s %12s %10s\n",
		"Benchmark", "Mode", "Preset", "Resolved", "Rate", "Avg Time", "Cost")
	fmt.Fprintf(w, "%s\n", repeatStr("-", 74))

	for _, s := range summaries {
		fmt.Fprintf(w, "%-12s %-10s %-8s %4d/%-5d %8.1f%% %12s $%8.2f\n",
			s.Benchmark,
			s.Mode,
			s.Preset,
			s.Resolved, s.Evaluated,
			s.ResolveRate*100,
			s.AvgDuration.Round(time.Second),
			s.TotalCost,
		)
	}
	fmt.Fprintln(w)
}

func repeatStr(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}
