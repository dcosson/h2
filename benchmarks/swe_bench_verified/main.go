package swe_bench_verified

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"h2/benchmarks/runner"
)

const (
	defaultTimeout     = 30 * time.Minute
	defaultConcurrency = 1
	defaultMaxTurns    = 100
)

// NewCommand creates the SWE-bench Verified benchmark command.
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
		Use:   "swe-bench-verified",
		Short: "Run SWE-bench Verified benchmark",
		Long: `Run the SWE-bench Verified benchmark against h2 agent configurations.

SWE-bench Verified contains 500 human-validated software engineering tasks
from the original SWE-bench dataset. It uses the same evaluation approach
(fail-to-pass + pass-to-pass) and serves as the industry-standard baseline
for credibility.

Examples:
  # Run baseline on all tasks
  h2 benchmark swe-bench-verified --sandbox bench-1

  # Run h2 mode on specific tasks
  h2 benchmark swe-bench-verified --mode h2 --sandbox bench-1 --tasks django__django-16379

  # Run with higher concurrency
  h2 benchmark swe-bench-verified --sandbox bench-1 --concurrency 4`,
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

			fmt.Fprintf(cmd.OutOrStdout(), "SWE-bench Verified: %d tasks selected\n", len(instances))

			runMode := runner.ModeBaseline
			if mode == "h2" {
				runMode = runner.ModeH2
			}

			config := runner.BenchmarkConfig{
				Name:        "swe_bench_verified",
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
