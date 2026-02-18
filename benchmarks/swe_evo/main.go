package swe_evo

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"h2/benchmarks/runner"
)

const (
	defaultTimeout     = 45 * time.Minute
	defaultConcurrency = 1
	defaultMaxTurns    = 150
)

// NewCommand creates the SWE-EVO benchmark command.
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
		Use:   "swe-evo",
		Short: "Run SWE-EVO benchmark",
		Long: `Run the SWE-EVO benchmark against h2 agent configurations.

SWE-EVO contains 48 evolution tasks from 7 Python projects. Each task
requires evolving a codebase from one version to another, typically
spanning 21 files on average. Evaluation runs a validation test suite
of ~874 tests per task.

Examples:
  # Run baseline (single agent) on all tasks
  h2 benchmark swe-evo --sandbox bench-1

  # Run h2 mode (multi-agent) on specific tasks
  h2 benchmark swe-evo --mode h2 --sandbox bench-1 --tasks task-001,task-002

  # Run with custom dataset file
  h2 benchmark swe-evo --dataset ./my-subset.json --sandbox bench-1`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if datasetPath == "" {
				datasetPath = DefaultDatasetPath()
			}
			dataset, err := LoadDataset(datasetPath)
			if err != nil {
				return fmt.Errorf("load dataset: %w", err)
			}

			filtered := dataset.Filter(tasks)
			if len(filtered) == 0 {
				return fmt.Errorf("no tasks match the filter (dataset has %d tasks)", len(dataset.Tasks))
			}

			fmt.Fprintf(cmd.OutOrStdout(), "SWE-EVO: %d tasks selected\n", len(filtered))

			runMode := runner.ModeBaseline
			if mode == "h2" {
				runMode = runner.ModeH2
			}

			config := runner.BenchmarkConfig{
				Name:        "swe_evo",
				Mode:        runMode,
				Preset:      preset,
				PodTemplate: podTemplate,
				TaskFilter:  tasks,
				Timeout:     timeout,
				Concurrency: concurrency,
				MaxTurns:    maxTurns,
			}

			benchTasks := ToBenchmarkTasks(filtered)

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
