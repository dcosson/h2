// Package commit0 implements the Commit0 benchmark runner.
//
// Commit0 is a benchmark of 54 Python library generation tasks (16 lite).
// Each task provides a library specification and test suite, and the agent
// must generate the library code from scratch. Evaluation measures unit test
// pass rate, code coverage, and static analysis quality.
//
// Dataset: https://github.com/commit-0/commit0
package commit0

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"h2/benchmarks/runner"
)

// Library represents a single Commit0 library task.
type Library struct {
	LibraryID   string   `json:"library_id"`    // e.g. "simpy", "click".
	RepoURL     string   `json:"repo_url"`      // Starter repo with specs + test suite.
	Branch      string   `json:"branch"`        // Branch to check out.
	Spec        string   `json:"spec"`          // Library specification / requirements.
	TestSuite   string   `json:"test_suite"`    // Path to test suite within repo.
	Language    string   `json:"language"`       // Programming language (always "python").
	Category    string   `json:"category"`      // Library category (e.g. "web", "cli", "data").
	Difficulty  string   `json:"difficulty"`     // "lite" or "full".
	SetupCmd    string   `json:"setup_cmd"`     // Command to set up the environment.
	TestCmd     string   `json:"test_cmd"`      // Command to run tests.
	CoverageCmd string   `json:"coverage_cmd"`  // Command to measure coverage.
	LintCmd     string   `json:"lint_cmd"`      // Command to run static analysis.
}

// Dataset holds loaded Commit0 libraries.
type Dataset struct {
	Libraries []Library
}

// LoadDataset loads Commit0 libraries from a JSON file.
func LoadDataset(path string) (*Dataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset: %w", err)
	}

	var libs []Library
	if err := json.Unmarshal(data, &libs); err != nil {
		return nil, fmt.Errorf("parse dataset: %w", err)
	}

	return &Dataset{Libraries: libs}, nil
}

// SaveDataset writes libraries to a JSON file.
func SaveDataset(path string, libs []Library) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dataset dir: %w", err)
	}
	data, err := json.MarshalIndent(libs, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal dataset: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// Filter returns libraries matching the given IDs. If ids is empty, returns all.
func (d *Dataset) Filter(ids []string) []Library {
	if len(ids) == 0 {
		return d.Libraries
	}
	allowed := make(map[string]bool, len(ids))
	for _, id := range ids {
		allowed[id] = true
	}
	var filtered []Library
	for _, lib := range d.Libraries {
		if allowed[lib.LibraryID] {
			filtered = append(filtered, lib)
		}
	}
	return filtered
}

// FilterByDifficulty returns libraries matching the given difficulty level.
func (d *Dataset) FilterByDifficulty(difficulty string) []Library {
	var filtered []Library
	for _, lib := range d.Libraries {
		if lib.Difficulty == difficulty {
			filtered = append(filtered, lib)
		}
	}
	return filtered
}

// ToBenchmarkTasks converts libraries to runner.BenchmarkTask format.
func ToBenchmarkTasks(libs []Library) []runner.BenchmarkTask {
	tasks := make([]runner.BenchmarkTask, len(libs))
	for i, lib := range libs {
		lib := lib // capture
		tasks[i] = runner.BenchmarkTask{
			ID:         lib.LibraryID,
			RepoURL:    lib.RepoURL,
			BaseCommit: lib.Branch,
			Issue:      formatPrompt(lib),
			EvalFunc:   MakeEvalFunc(lib),
		}
	}
	return tasks
}

func formatPrompt(lib Library) string {
	return fmt.Sprintf(
		"Implement the following Python library from scratch based on the specification below.\n\n"+
			"Library: %s\nCategory: %s\n\nSpecification:\n%s\n\n"+
			"The repository already contains a test suite at %s. "+
			"Your implementation should pass as many tests as possible. "+
			"Focus on correctness, good code coverage, and clean code.",
		lib.LibraryID, lib.Category, lib.Spec, lib.TestSuite,
	)
}

// DatasetDir returns the default directory for storing downloaded datasets.
func DatasetDir() string {
	return filepath.Join("benchmarks", "commit0", "data")
}

// DefaultDatasetPath returns the default path for the dataset JSON file.
func DefaultDatasetPath() string {
	return filepath.Join(DatasetDir(), "commit0.json")
}
