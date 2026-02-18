package terminal_bench

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"h2/benchmarks/runner"
)

const (
	defaultTimeout     = 15 * time.Minute
	defaultConcurrency = 1
	defaultMaxTurns    = 50
)

// NewCommand creates the Terminal-Bench benchmark command.
func NewCommand() *cobra.Command {
	var (
		datasetPath string
		mode        string
		preset      string
		podTemplate string
		sandboxName string
		tasks       []string
		category    string
		timeout     time.Duration
		concurrency int
		maxTurns    int
	)

	cmd := &cobra.Command{
		Use:   "terminal-bench",
		Short: "Run Terminal-Bench benchmark",
		Long: `Run the Terminal-Bench 2.0 benchmark against h2 agent configurations.

Terminal-Bench contains 89 terminal-based tasks covering file manipulation,
process management, and system administration. Evaluation checks task
completion via file state and output matching. As h2 is a terminal
multiplexer, this benchmark is a natural fit.

Examples:
  # Run baseline on all tasks
  h2 benchmark terminal-bench --sandbox bench-1

  # Run specific category
  h2 benchmark terminal-bench --sandbox bench-1 --category file-ops

  # Run specific tasks
  h2 benchmark terminal-bench --sandbox bench-1 --tasks task-01,task-15`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if datasetPath == "" {
				datasetPath = DefaultDatasetPath()
			}
			dataset, err := LoadDataset(datasetPath)
			if err != nil {
				return fmt.Errorf("load dataset: %w", err)
			}

			// Filter by category.
			tbTasks := dataset.Tasks
			if category != "" {
				tbTasks = dataset.FilterByCategory(category)
			}

			// Filter by task IDs.
			if len(tasks) > 0 {
				filtered := &Dataset{Tasks: tbTasks}
				tbTasks = filtered.Filter(tasks)
			}

			if len(tbTasks) == 0 {
				return fmt.Errorf("no tasks match the filter (dataset has %d tasks)", len(dataset.Tasks))
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Terminal-Bench: %d tasks selected\n", len(tbTasks))

			runMode := runner.ModeBaseline
			if mode == "h2" {
				runMode = runner.ModeH2
			}

			config := runner.BenchmarkConfig{
				Name:        "terminal_bench",
				Mode:        runMode,
				Preset:      preset,
				PodTemplate: podTemplate,
				TaskFilter:  tasks,
				Timeout:     timeout,
				Concurrency: concurrency,
				MaxTurns:    maxTurns,
			}

			benchTasks := ToBenchmarkTasks(tbTasks)

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
	cmd.Flags().StringVar(&category, "category", "", "Filter by category")
	cmd.Flags().DurationVar(&timeout, "timeout", defaultTimeout, "Per-task timeout")
	cmd.Flags().IntVar(&concurrency, "concurrency", defaultConcurrency, "Parallel task count")
	cmd.Flags().IntVar(&maxTurns, "max-turns", defaultMaxTurns, "Max agent turns per task")
	_ = cmd.MarkFlagRequired("sandbox")

	return cmd
}
