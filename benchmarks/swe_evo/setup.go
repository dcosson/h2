// Package swe_evo implements the SWE-EVO benchmark runner.
//
// SWE-EVO is a benchmark of 48 evolution tasks from 7 Python projects.
// Each task requires evolving a codebase from one version to another,
// typically spanning 21 files on average. Evaluation runs a validation
// test suite of ~874 tests per task.
//
// Paper: "SWE-EVO: Multi-File Software Evolution Benchmark"
package swe_evo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"h2/benchmarks/runner"
)

// Task represents a single SWE-EVO evolution task.
type Task struct {
	TaskID               string `json:"task_id"`
	Repo                 string `json:"repo"`
	BaseVersion          string `json:"base_version"`           // Starting version/commit.
	TargetVersion        string `json:"target_version"`         // Goal version/commit.
	EvolutionDescription string `json:"evolution_description"`  // What changes are needed.
	TestSuite            string `json:"test_suite"`             // Path to validation test suite.
	TestCmd              string `json:"test_cmd"`               // Command to run tests.
	TestCount            int    `json:"test_count"`             // Expected number of tests (~874).
	FilesChanged         int    `json:"files_changed"`          // Number of files in the evolution.
	RepoLanguage         string `json:"repo_language,omitempty"`
}

// Dataset holds loaded SWE-EVO tasks.
type Dataset struct {
	Tasks []Task
}

// RepoURL returns the full GitHub clone URL for a task.
func (t *Task) RepoURL() string {
	return "https://github.com/" + t.Repo + ".git"
}

// ParseTestSuiteFiles parses the test_suite field into a list of test file paths.
func (t *Task) ParseTestSuiteFiles() ([]string, error) {
	return parseTestList(t.TestSuite)
}

// parseTestList parses a JSON string that contains a list of identifiers.
func parseTestList(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return nil, nil
	}

	var items []string
	if err := json.Unmarshal([]byte(s), &items); err != nil {
		return nil, fmt.Errorf("parse test list: %w", err)
	}
	return items, nil
}

// LoadDataset loads SWE-EVO tasks from a JSON file.
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

// ToBenchmarkTasks converts tasks to runner.BenchmarkTask format.
func ToBenchmarkTasks(tasks []Task) []runner.BenchmarkTask {
	benchTasks := make([]runner.BenchmarkTask, len(tasks))
	for i, t := range tasks {
		t := t // capture loop variable
		benchTasks[i] = runner.BenchmarkTask{
			ID:         t.TaskID,
			RepoURL:    t.RepoURL(),
			BaseCommit: t.BaseVersion,
			Issue:      formatPrompt(t),
			EvalFunc:   MakeEvalFunc(t),
		}
	}
	return benchTasks
}

func formatPrompt(t Task) string {
	return fmt.Sprintf(
		"Evolve this codebase from version %s to version %s.\n\n"+
			"Repository: %s\n\n"+
			"Evolution Description:\n%s\n\n"+
			"This evolution typically involves changes across %d files. "+
			"The changes will be validated against a test suite of %d tests.",
		t.BaseVersion, t.TargetVersion,
		t.Repo,
		t.EvolutionDescription,
		t.FilesChanged, t.TestCount,
	)
}

// DatasetDir returns the default directory for storing downloaded datasets.
func DatasetDir() string {
	return filepath.Join("benchmarks", "swe_evo", "data")
}

// DefaultDatasetPath returns the default path for the dataset JSON file.
func DefaultDatasetPath() string {
	return filepath.Join(DatasetDir(), "swe_evo.json")
}
