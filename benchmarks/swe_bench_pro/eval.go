package swe_bench_pro

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EvalResult contains detailed evaluation results for a single instance.
type EvalResult struct {
	InstanceID     string   `json:"instance_id"`
	Resolved       bool     `json:"resolved"`
	FailToPassAll  bool     `json:"fail_to_pass_all"`  // All fail_to_pass tests now pass.
	PassToPassAll  bool     `json:"pass_to_pass_all"`  // All pass_to_pass tests still pass.
	FailedTests    []string `json:"failed_tests"`      // Tests that failed.
	TestPatchError string   `json:"test_patch_error"`  // Error applying test patch (if any).
	TestRunError   string   `json:"test_run_error"`    // Error running tests (if any).
}

// MakeEvalFunc creates an evaluation function for a single SWE-bench Pro instance.
// The returned function applies the test patch, runs the specified tests, and checks
// both fail-to-pass and pass-to-pass criteria.
func MakeEvalFunc(inst Instance) func(workDir string) (bool, error) {
	return func(workDir string) (bool, error) {
		result, err := Evaluate(workDir, inst)
		if err != nil {
			return false, err
		}
		return result.Resolved, nil
	}
}

// Evaluate runs the full evaluation for an instance on the given working directory.
func Evaluate(workDir string, inst Instance) (*EvalResult, error) {
	result := &EvalResult{
		InstanceID: inst.InstanceID,
	}

	// 1. Apply test patch.
	if inst.TestPatch != "" {
		if err := ApplyPatch(workDir, inst.TestPatch); err != nil {
			result.TestPatchError = err.Error()
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

	// 3. Parse test files to run.
	testFiles, err := inst.ParseSelectedTestFiles()
	if err != nil {
		return result, fmt.Errorf("parse selected test files: %w", err)
	}

	// 4. Run tests and collect results.
	testResults, runErr := RunTests(workDir, testFiles, inst.RepoLanguage)
	if runErr != nil {
		result.TestRunError = runErr.Error()
		// Continue — some tests may have passed even if runner errored.
	}

	// 5. Check fail-to-pass criteria.
	result.FailToPassAll = checkTestsPassed(failToPass, testResults)

	// 6. Check pass-to-pass criteria.
	result.PassToPassAll = checkTestsPassed(passToPass, testResults)

	// Collect failed tests for reporting.
	for testID, passed := range testResults {
		if !passed {
			result.FailedTests = append(result.FailedTests, testID)
		}
	}

	// 7. Resolved = all fail-to-pass pass AND all pass-to-pass pass.
	result.Resolved = result.FailToPassAll && result.PassToPassAll
	return result, nil
}

// ApplyPatch applies a unified diff patch to the working directory.
// Tries multiple strategies following the SWE-bench harness approach.
func ApplyPatch(workDir, patch string) error {
	if patch == "" {
		return nil
	}

	// Write patch to a temp file.
	patchFile := filepath.Join(workDir, ".swebench_test_patch.diff")
	if err := os.WriteFile(patchFile, []byte(patch), 0o644); err != nil {
		return fmt.Errorf("write patch file: %w", err)
	}
	defer os.Remove(patchFile)

	// Try git apply strategies in order (matching SWE-bench harness).
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

// RunTests runs the specified test files in the working directory and returns
// a map of test ID -> passed (true/false).
func RunTests(workDir string, testFiles []string, repoLanguage string) (map[string]bool, error) {
	if len(testFiles) == 0 {
		return nil, nil
	}

	testRunner := detectTestRunner(workDir, repoLanguage)
	results := make(map[string]bool)

	for _, testFile := range testFiles {
		args := buildTestCommand(testRunner, testFile)
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()

		// Parse test output to determine which tests passed/failed.
		fileResults := parseTestOutput(string(out), testRunner, err)
		for k, v := range fileResults {
			results[k] = v
		}
	}

	return results, nil
}

// TestRunner identifies the test framework used by a repository.
type TestRunner string

const (
	RunnerPytest TestRunner = "pytest"
	RunnerTox    TestRunner = "tox"
	RunnerUnknown TestRunner = "unknown"
)

// detectTestRunner determines which test framework to use.
func detectTestRunner(workDir string, repoLanguage string) TestRunner {
	// Check for pytest configuration.
	for _, cfg := range []string{"pytest.ini", "pyproject.toml", "setup.cfg", "conftest.py"} {
		if _, err := os.Stat(filepath.Join(workDir, cfg)); err == nil {
			return RunnerPytest
		}
	}

	// Check for tox.
	if _, err := os.Stat(filepath.Join(workDir, "tox.ini")); err == nil {
		return RunnerTox
	}

	// Default to pytest for Python repos.
	if strings.EqualFold(repoLanguage, "python") || repoLanguage == "" {
		return RunnerPytest
	}

	return RunnerUnknown
}

// buildTestCommand creates the command to run a single test file.
func buildTestCommand(testRunner TestRunner, testFile string) []string {
	switch testRunner {
	case RunnerPytest:
		// Don't use -x (stop on first failure) — we need all tests to run
		// for complete fail-to-pass/pass-to-pass evaluation.
		return []string{"python", "-m", "pytest", "-vs", testFile}
	case RunnerTox:
		return []string{"tox", "-e", "py", "--", testFile}
	default:
		return []string{"python", "-m", "pytest", "-vs", testFile}
	}
}

// parseTestOutput extracts pass/fail results from test runner output.
func parseTestOutput(output string, testRunner TestRunner, runErr error) map[string]bool {
	results := make(map[string]bool)

	switch testRunner {
	case RunnerPytest, RunnerTox, RunnerUnknown:
		results = parsePytestOutput(output)
	}

	return results
}

// parsePytestOutput parses pytest output to extract test results.
// Pytest outputs lines like:
//
//	test_file.py::TestClass::test_method PASSED
//	test_file.py::TestClass::test_method FAILED
func parsePytestOutput(output string) map[string]bool {
	results := make(map[string]bool)

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)

		// Look for PASSED/FAILED markers.
		if strings.HasSuffix(line, " PASSED") {
			testID := strings.TrimSuffix(line, " PASSED")
			testID = strings.TrimSpace(testID)
			if testID != "" {
				results[testID] = true
			}
		} else if strings.HasSuffix(line, " FAILED") {
			testID := strings.TrimSuffix(line, " FAILED")
			testID = strings.TrimSpace(testID)
			if testID != "" {
				results[testID] = false
			}
		} else if strings.HasSuffix(line, " ERROR") {
			testID := strings.TrimSuffix(line, " ERROR")
			testID = strings.TrimSpace(testID)
			if testID != "" {
				results[testID] = false
			}
		}
	}

	return results
}

// checkTestsPassed verifies that all specified tests are in the results and passed.
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
