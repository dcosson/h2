package re_bench

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"h2/benchmarks/runner"
)

const (
	defaultTimeout     = 2 * time.Hour
	defaultConcurrency = 1
	defaultMaxTurns    = 200
)

// NewCommand creates the RE-bench benchmark command.
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
		Use:   "re-bench",
		Short: "Run RE-bench benchmark",
		Long: `Run the RE-bench benchmark against h2 agent configurations.

RE-bench contains 7 research engineering environments from METR. Each
environment requires optimizing a score over extended time budgets (2h, 8h,
32h). Tasks test sustained research effort and long-horizon problem solving.

Examples:
  # Run baseline on all environments (2h budget)
  h2 benchmark re-bench --sandbox bench-1

  # Run with extended budget
  h2 benchmark re-bench --sandbox bench-1 --timeout 8h

  # Run specific environments
  h2 benchmark re-bench --sandbox bench-1 --tasks optimize-nn,optimize-compiler`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if datasetPath == "" {
				datasetPath = DefaultDatasetPath()
			}
			dataset, err := LoadDataset(datasetPath)
			if err != nil {
				return fmt.Errorf("load dataset: %w", err)
			}

			envs := dataset.Filter(tasks)
			if len(envs) == 0 {
				return fmt.Errorf("no environments match the filter (dataset has %d environments)", len(dataset.Environments))
			}

			fmt.Fprintf(cmd.OutOrStdout(), "RE-bench: %d environments selected (timeout: %s)\n", len(envs), timeout)

			runMode := runner.ModeBaseline
			if mode == "h2" {
				runMode = runner.ModeH2
			}

			config := runner.BenchmarkConfig{
				Name:        "re_bench",
				Mode:        runMode,
				Preset:      preset,
				PodTemplate: podTemplate,
				TaskFilter:  tasks,
				Timeout:     timeout,
				Concurrency: concurrency,
				MaxTurns:    maxTurns,
			}

			benchTasks := ToBenchmarkTasks(envs)

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
	cmd.Flags().StringSliceVar(&tasks, "tasks", nil, "Run only these environment IDs (comma-separated)")
	cmd.Flags().DurationVar(&timeout, "timeout", defaultTimeout, "Per-task timeout")
	cmd.Flags().IntVar(&concurrency, "concurrency", defaultConcurrency, "Parallel task count")
	cmd.Flags().IntVar(&maxTurns, "max-turns", defaultMaxTurns, "Max agent turns per task")
	_ = cmd.MarkFlagRequired("sandbox")

	return cmd
}
