package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RunSummary aggregates results across all tasks in a benchmark run.
type RunSummary struct {
	Benchmark    string        `json:"benchmark"`
	Mode         RunMode       `json:"mode"`
	Preset       string        `json:"preset"`
	TotalTasks   int           `json:"total_tasks"`
	Resolved     int           `json:"resolved"`
	Evaluated    int           `json:"evaluated"`
	Errored      int           `json:"errored"`
	ResolveRate  float64       `json:"resolve_rate"` // Resolved / Evaluated.
	AvgDuration  time.Duration `json:"avg_duration"`
	TotalCost    float64       `json:"total_cost"`
	TotalTokens  int64         `json:"total_tokens"`
	Timestamp    time.Time     `json:"timestamp"`
}

// Summarize computes aggregate stats from a set of task results.
func Summarize(config BenchmarkConfig, results []TaskResult) RunSummary {
	s := RunSummary{
		Benchmark:  config.Name,
		Mode:       config.Mode,
		Preset:     config.Preset,
		TotalTasks: len(results),
		Timestamp:  time.Now().UTC(),
	}

	var totalDuration time.Duration
	for _, r := range results {
		totalDuration += r.Duration
		s.TotalCost += r.Cost
		s.TotalTokens += r.TokensUsed

		if r.Error != "" {
			s.Errored++
		}
		if r.Evaluated {
			s.Evaluated++
			if r.Resolved {
				s.Resolved++
			}
		}
	}

	if len(results) > 0 {
		s.AvgDuration = totalDuration / time.Duration(len(results))
	}
	if s.Evaluated > 0 {
		s.ResolveRate = float64(s.Resolved) / float64(s.Evaluated)
	}

	return s
}

// FormatSummary returns a human-readable summary string.
func FormatSummary(s RunSummary) string {
	return fmt.Sprintf(
		"%s Results (%s mode, %s preset)\n"+
			"  Tasks: %d total, %d evaluated, %d resolved, %d errored\n"+
			"  Resolve rate: %.1f%%\n"+
			"  Avg duration: %s\n"+
			"  Total cost: $%.2f\n"+
			"  Total tokens: %d",
		s.Benchmark, s.Mode, s.Preset,
		s.TotalTasks, s.Evaluated, s.Resolved, s.Errored,
		s.ResolveRate*100,
		s.AvgDuration.Round(time.Second),
		s.TotalCost,
		s.TotalTokens,
	)
}

func saveRunConfig(resultsDir string, config BenchmarkConfig) error {
	return writeJSON(filepath.Join(resultsDir, "config.json"), config)
}

func saveTaskResult(resultsDir string, result TaskResult) error {
	tasksDir := filepath.Join(resultsDir, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		return err
	}
	return writeJSON(filepath.Join(tasksDir, result.TaskID+".json"), result)
}

func saveSummary(resultsDir string, summary RunSummary) error {
	return writeJSON(filepath.Join(resultsDir, "summary.json"), summary)
}

// LoadSummary reads a run summary from a results directory.
func LoadSummary(resultsDir string) (*RunSummary, error) {
	var s RunSummary
	if err := readJSON(filepath.Join(resultsDir, "summary.json"), &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// LoadTaskResult reads a single task result from a results directory.
func LoadTaskResult(resultsDir, taskID string) (*TaskResult, error) {
	var r TaskResult
	if err := readJSON(filepath.Join(resultsDir, "tasks", taskID+".json"), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// LoadRunConfig reads the benchmark config from a results directory.
func LoadRunConfig(resultsDir string) (*BenchmarkConfig, error) {
	var c BenchmarkConfig
	if err := readJSON(filepath.Join(resultsDir, "config.json"), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
