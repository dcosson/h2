// Package swe_bench_pro implements the SWE-bench Pro benchmark runner.
//
// SWE-bench Pro is a benchmark of 731 software engineering tasks from 11 open-source
// repositories. Each task requires fixing a real issue by modifying the codebase,
// with evaluation via test patches that verify the fix works without regressions.
//
// Dataset: https://huggingface.co/datasets/ScaleAI/SWE-bench_Pro
// GitHub: https://github.com/scaleapi/SWE-bench_Pro-os
package swe_bench_pro

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"h2/benchmarks/runner"
)

// Instance represents a single SWE-bench Pro task instance.
// This mirrors the HuggingFace dataset schema.
type Instance struct {
	InstanceID              string `json:"instance_id"`
	Repo                    string `json:"repo"`
	BaseCommit              string `json:"base_commit"`
	ProblemStatement        string `json:"problem_statement"`
	Patch                   string `json:"patch"`                          // Gold solution (not used during solving).
	TestPatch               string `json:"test_patch"`                     // Test definitions to apply for evaluation.
	FailToPass              string `json:"fail_to_pass"`                   // JSON array of test IDs that should go FAIL→PASS.
	PassToPass              string `json:"pass_to_pass"`                   // JSON array of test IDs that must stay passing.
	Requirements            string `json:"requirements,omitempty"`         // Dependencies.
	Interface               string `json:"interface,omitempty"`            // API/interface spec.
	RepoLanguage            string `json:"repo_language,omitempty"`        // Programming language.
	IssueSpecificity        string `json:"issue_specificity,omitempty"`    // Category of specificity.
	IssueCategories         string `json:"issue_categories,omitempty"`     // Category labels.
	BeforeRepoSetCmd        string `json:"before_repo_set_cmd,omitempty"`  // Setup commands before repo init.
	SelectedTestFilesToRun  string `json:"selected_test_files_to_run"`     // Which test files to execute.
	DockerhubTag            string `json:"dockerhub_tag,omitempty"`        // Docker image tag for environment.
}

// Dataset holds the loaded SWE-bench Pro instances.
type Dataset struct {
	Instances []Instance
}

// RepoURL returns the full GitHub clone URL for an instance.
func (inst *Instance) RepoURL() string {
	return "https://github.com/" + inst.Repo + ".git"
}

// ParseFailToPass parses the fail_to_pass JSON field into a list of test IDs.
func (inst *Instance) ParseFailToPass() ([]string, error) {
	return parseTestList(inst.FailToPass)
}

// ParsePassToPass parses the pass_to_pass JSON field into a list of test IDs.
func (inst *Instance) ParsePassToPass() ([]string, error) {
	return parseTestList(inst.PassToPass)
}

// ParseSelectedTestFiles parses the selected_test_files_to_run field.
func (inst *Instance) ParseSelectedTestFiles() ([]string, error) {
	return parseTestList(inst.SelectedTestFilesToRun)
}

// parseTestList parses a JSON string that contains a list of test identifiers.
// SWE-bench stores these as JSON-encoded arrays within string fields.
func parseTestList(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return nil, nil
	}

	var tests []string
	if err := json.Unmarshal([]byte(s), &tests); err != nil {
		return nil, fmt.Errorf("parse test list: %w", err)
	}
	return tests, nil
}

// LoadDataset loads SWE-bench Pro instances from a JSON file.
// The file should contain an array of Instance objects.
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
		inst := inst // capture loop variable
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
	return filepath.Join("benchmarks", "swe_bench_pro", "data")
}

// DefaultDatasetPath returns the default path for the dataset JSON file.
func DefaultDatasetPath() string {
	return filepath.Join(DatasetDir(), "swe_bench_pro.json")
}
