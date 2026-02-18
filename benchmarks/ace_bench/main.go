package ace_bench

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

// NewCommand creates the ACE-Bench benchmark command.
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
		Use:   "ace-bench",
		Short: "Run ACE-Bench benchmark",
		Long: `Run the ACE-Bench benchmark against h2 agent configurations.

ACE-Bench contains 212 feature-level software engineering tasks from
real-world open-source repositories. Each task requires implementing a
new feature, evaluated by execution-based test suites that verify the
feature works correctly without regressions.

Examples:
  # Run baseline (single agent) on all tasks
  h2 benchmark ace-bench --sandbox bench-1

  # Run h2 mode (multi-agent) on specific tasks
  h2 benchmark ace-bench --mode h2 --sandbox bench-1 --tasks task-001,task-002

  # Run with custom dataset file
  h2 benchmark ace-bench --dataset ./my-subset.json --sandbox bench-1`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if datasetPath == "" {
				datasetPath = DefaultDatasetPath()
			}
			dataset, err := LoadDataset(datasetPath)
			if err != nil {
				return fmt.Errorf("load dataset: %w", err)
			}

			instances := dataset.Filter(tasks)
			if len(instances) == 0 {
				return fmt.Errorf("no instances match the filter (dataset has %d instances)", len(dataset.Instances))
			}

			fmt.Fprintf(cmd.OutOrStdout(), "ACE-Bench: %d tasks selected\n", len(instances))

			runMode := runner.ModeBaseline
			if mode == "h2" {
				runMode = runner.ModeH2
			}

			config := runner.BenchmarkConfig{
				Name:        "ace_bench",
				Mode:        runMode,
				Preset:      preset,
				PodTemplate: podTemplate,
				TaskFilter:  tasks,
				Timeout:     timeout,
				Concurrency: concurrency,
				MaxTurns:    maxTurns,
			}

			benchTasks := ToBenchmarkTasks(instances)

			r := runner.NewRunner(config, sandboxName)

			if runMode == runner.ModeH2 {
				podRunner := runner.NewPodRunner(podTemplate)
				r.RunAgent = podRunner.RunAgent
			}

			results, err := r.RunAll(context.Background(), benchTasks)
			if err != nil {
				return fmt.Errorf("benchmark run: %w", err)
			}

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
		Short: "Display ACE-Bench benchmark results comparison",
		Args:  cobra.MinimumNArgs(1),
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
