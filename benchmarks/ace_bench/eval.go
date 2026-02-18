package ace_bench

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"h2/benchmarks/evalutil"
)

// EvalResult contains detailed evaluation results for a single instance.
type EvalResult struct {
	InstanceID    string   `json:"instance_id"`
	Resolved      bool     `json:"resolved"`
	FailToPassAll bool     `json:"fail_to_pass_all"` // All fail_to_pass tests now pass.
	PassToPassAll bool     `json:"pass_to_pass_all"` // All pass_to_pass tests still pass.
	FailedTests   []string `json:"failed_tests"`     // Tests that failed.
	TestPatchErr  string   `json:"test_patch_error"`  // Error applying test patch (if any).
	TestRunErr    string   `json:"test_run_error"`    // Error running tests (if any).
}

// MakeEvalFunc creates an evaluation function for a single ACE-Bench instance.
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
		if err := evalutil.ApplyPatch(workDir, inst.TestPatch); err != nil {
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

	// 3. Parse test files to run.
	testFiles, err := inst.ParseSelectedTestFiles()
	if err != nil {
		return result, fmt.Errorf("parse selected test files: %w", err)
	}

	// 4. Run tests and collect results.
	testResults, runErr := RunTests(workDir, testFiles, inst.RepoLanguage)
	if runErr != nil {
		result.TestRunErr = runErr.Error()
	}

	// 5. Check fail-to-pass criteria.
	result.FailToPassAll = evalutil.CheckTestsPassed(failToPass, testResults)

	// 6. Check pass-to-pass criteria.
	result.PassToPassAll = evalutil.CheckTestsPassed(passToPass, testResults)

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

// RunTests runs the specified test files and returns a map of test ID -> passed.
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
	RunnerPytest   TestRunner = "pytest"
	RunnerTox      TestRunner = "tox"
	RunnerUnknown  TestRunner = "unknown"
)

func detectTestRunner(workDir string, repoLanguage string) TestRunner {
	for _, cfg := range []string{"pytest.ini", "pyproject.toml", "setup.cfg", "conftest.py"} {
		if _, err := os.Stat(filepath.Join(workDir, cfg)); err == nil {
			return RunnerPytest
		}
	}

	if _, err := os.Stat(filepath.Join(workDir, "tox.ini")); err == nil {
		return RunnerTox
	}

	if strings.EqualFold(repoLanguage, "python") || repoLanguage == "" {
		return RunnerPytest
	}

	return RunnerUnknown
}

func buildTestCommand(testRunner TestRunner, testFile string) []string {
	switch testRunner {
	case RunnerPytest:
		return []string{"python", "-m", "pytest", "-vs", testFile}
	case RunnerTox:
		return []string{"tox", "-e", "py", "--", testFile}
	default:
		return []string{"python", "-m", "pytest", "-vs", testFile}
	}
}

func parseTestOutput(output string, testRunner TestRunner, runErr error) map[string]bool {
	results := make(map[string]bool)

	switch testRunner {
	case RunnerPytest, RunnerTox, RunnerUnknown:
		results = evalutil.ParsePytestOutput(output)
	}

	return results
}

