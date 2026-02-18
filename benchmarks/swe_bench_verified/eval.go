package swe_bench_verified

import (
	"fmt"
	"os/exec"

	"h2/benchmarks/evalutil"
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
	result.FailToPassAll = evalutil.CheckTestsPassed(failToPass, testResults)
	result.PassToPassAll = evalutil.CheckTestsPassed(passToPass, testResults)

	for testID, passed := range testResults {
		if !passed {
			result.FailedTests = append(result.FailedTests, testID)
		}
	}

	result.Resolved = result.FailToPassAll && result.PassToPassAll
	return result, nil
}

// RunTests runs the specified test files using pytest.
func RunTests(workDir string, testFiles []string) (map[string]bool, error) {
	if len(testFiles) == 0 {
		return nil, nil
	}

	results := make(map[string]bool)

	for _, testFile := range testFiles {
		cmd := exec.Command("python", "-m", "pytest", "-vs", testFile)
		cmd.Dir = workDir
		out, _ := cmd.CombinedOutput()

		for k, v := range evalutil.ParsePytestOutput(string(out)) {
			results[k] = v
		}
	}

	return results, nil
}

