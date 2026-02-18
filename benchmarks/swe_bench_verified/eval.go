package swe_bench_verified

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EvalResult contains detailed evaluation results for a single instance.
type EvalResult struct {
	InstanceID    string   `json:"instance_id"`
	Resolved      bool     `json:"resolved"`
	FailToPassAll bool     `json:"fail_to_pass_all"` // All fail_to_pass tests now pass.
	PassToPassAll bool     `json:"pass_to_pass_all"` // All pass_to_pass tests still pass.
	FailedTests   []string `json:"failed_tests"`
	TestPatchErr  string   `json:"test_patch_error"`
	TestRunErr    string   `json:"test_run_error"`
}

// MakeEvalFunc creates an evaluation function for a single SWE-bench Verified instance.
func MakeEvalFunc(inst Instance) func(workDir string) (bool, error) {
	return func(workDir string) (bool, error) {
		result, err := Evaluate(workDir, inst)
		if err != nil {
			return false, err
		}
		return result.Resolved, nil
	}
}

// Evaluate runs the full evaluation for an instance.
func Evaluate(workDir string, inst Instance) (*EvalResult, error) {
	result := &EvalResult{
		InstanceID: inst.InstanceID,
	}

	// 1. Apply test patch.
	if inst.TestPatch != "" {
		if err := ApplyPatch(workDir, inst.TestPatch); err != nil {
			result.TestPatchErr = err.Error()
			return result, fmt.Errorf("apply test patch: %w", err)
		}
	}

	// 2. Parse test lists.
	failToPass, err := inst.ParseFailToPass()
	if err != nil {
		return result, fmt.Errorf("parse fail_to_pass: %w", err)
	}
	passToPass, err := inst.ParsePassToPass()
	if err != nil {
		return result, fmt.Errorf("parse pass_to_pass: %w", err)
	}

	// 3. Parse test files.
	testFiles, err := inst.ParseSelectedTestFiles()
	if err != nil {
		return result, fmt.Errorf("parse test files: %w", err)
	}

	// 4. Run tests.
	testResults, runErr := RunTests(workDir, testFiles)
	if runErr != nil {
		result.TestRunErr = runErr.Error()
	}

	// 5. Check criteria.
	result.FailToPassAll = checkTestsPassed(failToPass, testResults)
	result.PassToPassAll = checkTestsPassed(passToPass, testResults)

	for testID, passed := range testResults {
		if !passed {
			result.FailedTests = append(result.FailedTests, testID)
		}
	}

	result.Resolved = result.FailToPassAll && result.PassToPassAll
	return result, nil
}

// ApplyPatch applies a unified diff patch to the working directory.
func ApplyPatch(workDir, patch string) error {
	if patch == "" {
		return nil
	}

	patchFile := filepath.Join(workDir, ".swebench_test_patch.diff")
	if err := os.WriteFile(patchFile, []byte(patch), 0o644); err != nil {
		return fmt.Errorf("write patch file: %w", err)
	}
	defer os.Remove(patchFile)

	strategies := [][]string{
		{"git", "-C", workDir, "apply", "-v", patchFile},
		{"git", "-C", workDir, "apply", "-v", "--no-index", patchFile},
		{"patch", "-d", workDir, "-p1", "-i", patchFile},
	}

	var lastErr error
	for _, args := range strategies {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = workDir
		if out, err := cmd.CombinedOutput(); err == nil {
			return nil
		} else {
			lastErr = fmt.Errorf("%s: %s: %w", args[0], truncate(string(out), 500), err)
		}
	}

	return fmt.Errorf("all patch strategies failed: %w", lastErr)
}

// RunTests runs the specified test files using pytest.
func RunTests(workDir string, testFiles []string) (map[string]bool, error) {
	if len(testFiles) == 0 {
		return nil, nil
	}

	results := make(map[string]bool)

	for _, testFile := range testFiles {
		cmd := exec.Command("python", "-m", "pytest", "-xvs", testFile)
		cmd.Dir = workDir
		out, _ := cmd.CombinedOutput()

		for k, v := range parsePytestOutput(string(out)) {
			results[k] = v
		}
	}

	return results, nil
}

// parsePytestOutput parses pytest output to extract test results.
func parsePytestOutput(output string) map[string]bool {
	results := make(map[string]bool)

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)

		if strings.HasSuffix(line, " PASSED") {
			testID := strings.TrimSpace(strings.TrimSuffix(line, " PASSED"))
			if testID != "" {
				results[testID] = true
			}
		} else if strings.HasSuffix(line, " FAILED") {
			testID := strings.TrimSpace(strings.TrimSuffix(line, " FAILED"))
			if testID != "" {
				results[testID] = false
			}
		} else if strings.HasSuffix(line, " ERROR") {
			testID := strings.TrimSpace(strings.TrimSuffix(line, " ERROR"))
			if testID != "" {
				results[testID] = false
			}
		}
	}

	return results
}

func checkTestsPassed(testIDs []string, results map[string]bool) bool {
	if len(testIDs) == 0 {
		return true
	}
	for _, id := range testIDs {
		passed, found := results[id]
		if !found || !passed {
			return false
		}
	}
	return true
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
