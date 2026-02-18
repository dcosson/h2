// Package re_bench implements the RE-bench benchmark runner.
//
// RE-bench is a benchmark of 7 research engineering environments from METR.
// Each environment requires optimizing a score over extended time budgets
// (2h, 8h, 32h), testing sustained effort and distributed memory.
//
// Dataset: https://github.com/METR/re-bench
package re_bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"h2/benchmarks/runner"
)

// Environment represents a single RE-bench task environment.
type Environment struct {
	EnvID       string `json:"env_id"`        // e.g. "optimize-nn", "optimize-compiler".
	Name        string `json:"name"`          // Human-readable name.
	Description string `json:"description"`   // Task description and scoring criteria.
	DockerImage string `json:"docker_image"`  // Pre-configured Docker image.
	ScoreCmd    string `json:"score_cmd"`     // Command to evaluate current score.
	SetupCmd    string `json:"setup_cmd"`     // Command to initialize environment.
	WorkDir     string `json:"work_dir"`      // Working directory within container.
	MaxScore    float64 `json:"max_score"`    // Maximum achievable score.
	BaseScore   float64 `json:"base_score"`   // Score before any agent modifications.
}

// Dataset holds loaded RE-bench environments.
type Dataset struct {
	Environments []Environment
}

// LoadDataset loads RE-bench environments from a JSON file.
func LoadDataset(path string) (*Dataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset: %w", err)
	}

	var envs []Environment
	if err := json.Unmarshal(data, &envs); err != nil {
		return nil, fmt.Errorf("parse dataset: %w", err)
	}

	return &Dataset{Environments: envs}, nil
}

// SaveDataset writes environments to a JSON file.
func SaveDataset(path string, envs []Environment) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dataset dir: %w", err)
	}
	data, err := json.MarshalIndent(envs, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal dataset: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// Filter returns environments matching the given IDs. If ids is empty, returns all.
func (d *Dataset) Filter(ids []string) []Environment {
	if len(ids) == 0 {
		return d.Environments
	}
	allowed := make(map[string]bool, len(ids))
	for _, id := range ids {
		allowed[id] = true
	}
	var filtered []Environment
	for _, env := range d.Environments {
		if allowed[env.EnvID] {
			filtered = append(filtered, env)
		}
	}
	return filtered
}

// ToBenchmarkTasks converts environments to runner.BenchmarkTask format.
func ToBenchmarkTasks(envs []Environment) []runner.BenchmarkTask {
	tasks := make([]runner.BenchmarkTask, len(envs))
	for i, env := range envs {
		env := env // capture
		tasks[i] = runner.BenchmarkTask{
			ID:    env.EnvID,
			Issue: formatPrompt(env),
			EvalFunc: MakeEvalFunc(env),
		}
	}
	return tasks
}

func formatPrompt(env Environment) string {
	return fmt.Sprintf(
		"You are working in the %q research engineering environment.\n\n"+
			"Task:\n%s\n\n"+
			"The scoring command is: %s\n"+
			"Your goal is to maximize the score. The baseline score is %.2f and the maximum is %.2f.\n"+
			"Optimize iteratively — check your score frequently and try different approaches.",
		env.Name, env.Description, env.ScoreCmd, env.BaseScore, env.MaxScore,
	)
}

// DatasetDir returns the default directory for storing downloaded datasets.
func DatasetDir() string {
	return filepath.Join("benchmarks", "re_bench", "data")
}

// DefaultDatasetPath returns the default path for the dataset JSON file.
func DefaultDatasetPath() string {
	return filepath.Join(DatasetDir(), "re_bench.json")
}
