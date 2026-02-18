package swe_evo

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
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
	testOut, testErr := runShellCmd(workDir, testCmd)
	if testErr != nil {
		result.TestError = testErr.Error()
	}

	// Parse test results.
	result.TestsPassed, result.TestsTotal = parsePytestSummary(testOut)
	if result.TestsTotal > 0 {
		result.TestPassRate = float64(result.TestsPassed) / float64(result.TestsTotal)
	}

	// Resolved if all tests pass.
	result.Resolved = result.TestsTotal > 0 && result.TestsPassed == result.TestsTotal

	return result, nil
}

// parsePytestSummary extracts passed/total from pytest summary output.
// Looks for patterns like "5 passed, 2 failed" or "10 passed".
func parsePytestSummary(output string) (passed, total int) {
	passedRe := regexp.MustCompile(`(\d+) passed`)
	if m := passedRe.FindStringSubmatch(output); len(m) > 1 {
		passed, _ = strconv.Atoi(m[1])
	}

	total = passed

	failedRe := regexp.MustCompile(`(\d+) failed`)
	if m := failedRe.FindStringSubmatch(output); len(m) > 1 {
		n, _ := strconv.Atoi(m[1])
		total += n
	}

	errorRe := regexp.MustCompile(`(\d+) error`)
	if m := errorRe.FindStringSubmatch(output); len(m) > 1 {
		n, _ := strconv.Atoi(m[1])
		total += n
	}

	return passed, total
}

func runShellCmd(workDir, command string) (string, error) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "", fmt.Errorf("empty command")
	}
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
