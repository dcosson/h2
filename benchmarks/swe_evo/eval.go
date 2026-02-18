package swe_evo

import (
	"h2/benchmarks/evalutil"
)

// EvalResult contains detailed evaluation results for a single SWE-EVO task.
type EvalResult struct {
	TaskID       string  `json:"task_id"`
	Resolved     bool    `json:"resolved"`        // True if test pass rate meets threshold.
	TestPassRate float64 `json:"test_pass_rate"`  // Fraction of tests passed (0.0-1.0).
	TestsPassed  int     `json:"tests_passed"`
	TestsTotal   int     `json:"tests_total"`
	TestError    string  `json:"test_error"`
}

// MakeEvalFunc creates an evaluation function for a single SWE-EVO task.
func MakeEvalFunc(task Task) func(workDir string) (bool, error) {
	return func(workDir string) (bool, error) {
		result, err := Evaluate(workDir, task)
		if err != nil {
			return false, err
		}
		return result.Resolved, nil
	}
}

// Evaluate runs the validation test suite for a task and checks the pass rate.
func Evaluate(workDir string, task Task) (*EvalResult, error) {
	result := &EvalResult{
		TaskID: task.TaskID,
	}

	// Determine test command.
	testCmd := task.TestCmd
	if testCmd == "" {
		testCmd = "python -m pytest --tb=short -q"
	}

	// Run tests.
	testOut, testErr := evalutil.RunShellCmd(workDir, testCmd)
	if testErr != nil {
		result.TestError = testErr.Error()
	}

	// Parse test results.
	result.TestsPassed, result.TestsTotal = evalutil.ParsePytestSummary(testOut)
	if result.TestsTotal > 0 {
		result.TestPassRate = float64(result.TestsPassed) / float64(result.TestsTotal)
	}

	// Resolved if all tests pass.
	result.Resolved = result.TestsTotal > 0 && result.TestsPassed == result.TestsTotal

	return result, nil
}

