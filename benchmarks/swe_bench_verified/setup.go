// Package swe_bench_verified implements the SWE-bench Verified benchmark runner.
//
// SWE-bench Verified is the industry-standard benchmark of 500 human-validated
// software engineering tasks. It uses the same evaluation approach as SWE-bench Pro
// (fail-to-pass + pass-to-pass test criteria) but with a curated, smaller dataset.
//
// Dataset: https://huggingface.co/datasets/princeton-nlp/SWE-bench_Verified
package swe_bench_verified

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"h2/benchmarks/runner"
)

// Instance represents a single SWE-bench Verified task instance.
// Schema matches the standard SWE-bench dataset format.
type Instance struct {
	InstanceID             string `json:"instance_id"`
	Repo                   string `json:"repo"`
	BaseCommit             string `json:"base_commit"`
	ProblemStatement       string `json:"problem_statement"`
	Patch                  string `json:"patch"`                         // Gold solution (not used during solving).
	TestPatch              string `json:"test_patch"`                    // Test definitions for evaluation.
	FailToPass             string `json:"fail_to_pass"`                  // JSON array of tests that should go FAIL→PASS.
	PassToPass             string `json:"pass_to_pass"`                  // JSON array of tests that must stay passing.
	SelectedTestFilesToRun string `json:"selected_test_files_to_run"`    // Which test files to execute.
	RepoLanguage           string `json:"repo_language,omitempty"`
	Version                string `json:"version,omitempty"`
}

// Dataset holds loaded SWE-bench Verified instances.
type Dataset struct {
	Instances []Instance
}

// RepoURL returns the full GitHub clone URL for an instance.
func (inst *Instance) RepoURL() string {
	return "https://github.com/" + inst.Repo + ".git"
}

// ParseFailToPass parses the fail_to_pass JSON field.
func (inst *Instance) ParseFailToPass() ([]string, error) {
	return parseTestList(inst.FailToPass)
}

// ParsePassToPass parses the pass_to_pass JSON field.
func (inst *Instance) ParsePassToPass() ([]string, error) {
	return parseTestList(inst.PassToPass)
}

// ParseSelectedTestFiles parses the selected_test_files_to_run field.
func (inst *Instance) ParseSelectedTestFiles() ([]string, error) {
	return parseTestList(inst.SelectedTestFilesToRun)
}

// parseTestList parses a test list string that may be JSON (double quotes) or
// Python repr() format (single quotes). The HuggingFace SWE-bench datasets
// use both formats depending on the instance.
func parseTestList(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return nil, nil
	}
	var tests []string
	if err := json.Unmarshal([]byte(s), &tests); err != nil {
		// Try converting Python single-quote format to JSON double quotes.
		converted := pythonListToJSON(s)
		if err2 := json.Unmarshal([]byte(converted), &tests); err2 != nil {
			return nil, fmt.Errorf("parse test list: %w", err)
		}
	}
	return tests, nil
}

// pythonListToJSON converts a Python repr() list with single quotes to valid JSON.
// e.g. "['foo', 'bar']" → '["foo", "bar"]'
func pythonListToJSON(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '\'' && !inString {
			b.WriteByte('"')
			inString = true
		} else if ch == '\'' && inString {
			b.WriteByte('"')
			inString = false
		} else {
			b.WriteByte(ch)
		}
	}
	return b.String()
}

// LoadDataset loads SWE-bench Verified instances from a JSON file.
func LoadDataset(path string) (*Dataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset: %w", err)
	}

	var instances []Instance
	if err := json.Unmarshal(data, &instances); err != nil {
		return nil, fmt.Errorf("parse dataset: %w", err)
	}

	return &Dataset{Instances: instances}, nil
}

// SaveDataset writes instances to a JSON file.
func SaveDataset(path string, instances []Instance) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dataset dir: %w", err)
	}
	data, err := json.MarshalIndent(instances, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal dataset: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// Filter returns instances matching the given IDs. If ids is empty, returns all.
func (d *Dataset) Filter(ids []string) []Instance {
	if len(ids) == 0 {
		return d.Instances
	}
	allowed := make(map[string]bool, len(ids))
	for _, id := range ids {
		allowed[id] = true
	}
	var filtered []Instance
	for _, inst := range d.Instances {
		if allowed[inst.InstanceID] {
			filtered = append(filtered, inst)
		}
	}
	return filtered
}

// ToBenchmarkTasks converts instances to runner.BenchmarkTask format.
func ToBenchmarkTasks(instances []Instance) []runner.BenchmarkTask {
	tasks := make([]runner.BenchmarkTask, len(instances))
	for i, inst := range instances {
		inst := inst // capture
		tasks[i] = runner.BenchmarkTask{
			ID:         inst.InstanceID,
			RepoURL:    inst.RepoURL(),
			BaseCommit: inst.BaseCommit,
			Issue:      inst.ProblemStatement,
			TestPatch:  inst.TestPatch,
			EvalFunc:   MakeEvalFunc(inst),
		}
	}
	return tasks
}

// DatasetDir returns the default directory for storing downloaded datasets.
func DatasetDir() string {
	return filepath.Join("benchmarks", "swe_bench_verified", "data")
}

// DefaultDatasetPath returns the default path for the dataset JSON file.
func DefaultDatasetPath() string {
	return filepath.Join(DatasetDir(), "swe_bench_verified.json")
}
