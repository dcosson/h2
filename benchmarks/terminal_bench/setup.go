// Package terminal_bench implements the Terminal-Bench benchmark runner.
//
// Terminal-Bench 2.0 contains 89 terminal-based tasks requiring file
// manipulation, process management, and system administration. Evaluation
// checks task completion via file state and output matching.
//
// Dataset: https://github.com/laude-institute/terminal-bench
package terminal_bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"h2/benchmarks/runner"
)

// Task represents a single Terminal-Bench task.
type Task struct {
	TaskID       string   `json:"task_id"`       // Unique task identifier.
	Name         string   `json:"name"`          // Human-readable name.
	Description  string   `json:"description"`   // Task description and instructions.
	DockerImage  string   `json:"docker_image"`  // Pre-configured Docker image.
	SetupCmd     string   `json:"setup_cmd"`     // Command to set up the environment.
	Category     string   `json:"category"`      // e.g. "file-ops", "process", "network".
	Difficulty   string   `json:"difficulty"`     // "easy", "medium", "hard".
	Checks       []Check  `json:"checks"`        // Verification checks.
	ExpectedFiles []string `json:"expected_files"` // Files that should exist after completion.
}

// Check defines a verification check for task completion.
type Check struct {
	Type     string `json:"type"`      // "file_exists", "file_contains", "command_output", "exit_code".
	Target   string `json:"target"`    // File path or command to check.
	Expected string `json:"expected"`  // Expected content, output, or exit code.
}

// Dataset holds loaded Terminal-Bench tasks.
type Dataset struct {
	Tasks []Task
}

// LoadDataset loads Terminal-Bench tasks from a JSON file.
func LoadDataset(path string) (*Dataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset: %w", err)
	}

	var tasks []Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		return nil, fmt.Errorf("parse dataset: %w", err)
	}

	return &Dataset{Tasks: tasks}, nil
}

// SaveDataset writes tasks to a JSON file.
func SaveDataset(path string, tasks []Task) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dataset dir: %w", err)
	}
	data, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal dataset: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// Filter returns tasks matching the given IDs. If ids is empty, returns all.
func (d *Dataset) Filter(ids []string) []Task {
	if len(ids) == 0 {
		return d.Tasks
	}
	allowed := make(map[string]bool, len(ids))
	for _, id := range ids {
		allowed[id] = true
	}
	var filtered []Task
	for _, t := range d.Tasks {
		if allowed[t.TaskID] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// FilterByCategory returns tasks matching the given category.
func (d *Dataset) FilterByCategory(category string) []Task {
	var filtered []Task
	for _, t := range d.Tasks {
		if t.Category == category {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// ToBenchmarkTasks converts tasks to runner.BenchmarkTask format.
func ToBenchmarkTasks(tasks []Task) []runner.BenchmarkTask {
	benchTasks := make([]runner.BenchmarkTask, len(tasks))
	for i, t := range tasks {
		t := t // capture
		benchTasks[i] = runner.BenchmarkTask{
			ID:       t.TaskID,
			Issue:    formatPrompt(t),
			EvalFunc: MakeEvalFunc(t),
		}
	}
	return benchTasks
}

func formatPrompt(t Task) string {
	return fmt.Sprintf(
		"Complete the following terminal task.\n\n"+
			"Task: %s\nCategory: %s\nDifficulty: %s\n\n"+
			"Instructions:\n%s",
		t.Name, t.Category, t.Difficulty, t.Description,
	)
}

// DatasetDir returns the default directory for storing downloaded datasets.
func DatasetDir() string {
	return filepath.Join("benchmarks", "terminal_bench", "data")
}

// DefaultDatasetPath returns the default path for the dataset JSON file.
func DefaultDatasetPath() string {
	return filepath.Join(DatasetDir(), "terminal_bench.json")
}
