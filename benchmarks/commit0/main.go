package commit0

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"h2/benchmarks/runner"
)

const (
	defaultTimeout     = 60 * time.Minute
	defaultConcurrency = 1
	defaultMaxTurns    = 100
)

// NewCommand creates the Commit0 benchmark command.
func NewCommand() *cobra.Command {
	var (
		datasetPath string
		mode        string
		preset      string
		podTemplate string
		sandboxName string
		tasks       []string
		difficulty  string
		timeout     time.Duration
		concurrency int
		maxTurns    int
	)

	cmd := &cobra.Command{
		Use:   "commit0",
		Short: "Run Commit0 benchmark",
		Long: `Run the Commit0 benchmark against h2 agent configurations.

Commit0 contains 54 Python library generation tasks (16 in lite mode).
Each task provides a library specification and test suite. The agent must
generate the library code from scratch. Evaluation measures unit test
pass rate, code coverage, and static analysis quality.

Examples:
  # Run baseline on all libraries
  h2 benchmark commit0 --sandbox bench-1

  # Run lite subset only
  h2 benchmark commit0 --sandbox bench-1 --difficulty lite

  # Run specific libraries
  h2 benchmark commit0 --sandbox bench-1 --tasks simpy,click`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if datasetPath == "" {
				datasetPath = DefaultDatasetPath()
			}
			dataset, err := LoadDataset(datasetPath)
			if err != nil {
				return fmt.Errorf("load dataset: %w", err)
			}

			// Filter by difficulty if specified.
			libs := dataset.Libraries
			if difficulty != "" {
				libs = dataset.FilterByDifficulty(difficulty)
			}

			// Filter by task IDs.
			if len(tasks) > 0 {
				filtered := &Dataset{Libraries: libs}
				libs = filtered.Filter(tasks)
			}

			if len(libs) == 0 {
				return fmt.Errorf("no libraries match the filter (dataset has %d libraries)", len(dataset.Libraries))
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Commit0: %d libraries selected\n", len(libs))

			runMode := runner.ModeBaseline
			if mode == "h2" {
				runMode = runner.ModeH2
			}

			config := runner.BenchmarkConfig{
				Name:        "commit0",
				Mode:        runMode,
				Preset:      preset,
				PodTemplate: podTemplate,
				TaskFilter:  tasks,
				Timeout:     timeout,
				Concurrency: concurrency,
				MaxTurns:    maxTurns,
			}

			benchTasks := ToBenchmarkTasks(libs)

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
	cmd.Flags().StringSliceVar(&tasks, "tasks", nil, "Run only these library IDs (comma-separated)")
	cmd.Flags().StringVar(&difficulty, "difficulty", "", "Filter by difficulty: lite or full")
	cmd.Flags().DurationVar(&timeout, "timeout", defaultTimeout, "Per-task timeout")
	cmd.Flags().IntVar(&concurrency, "concurrency", defaultConcurrency, "Parallel task count")
	cmd.Flags().IntVar(&maxTurns, "max-turns", defaultMaxTurns, "Max agent turns per task")
	_ = cmd.MarkFlagRequired("sandbox")

	return cmd
}
