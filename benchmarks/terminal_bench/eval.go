package terminal_bench

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EvalResult contains detailed evaluation results for a Terminal-Bench task.
type EvalResult struct {
	TaskID       string      `json:"task_id"`
	Resolved     bool        `json:"resolved"`      // True if all checks pass.
	ChecksPassed int         `json:"checks_passed"`
	ChecksTotal  int         `json:"checks_total"`
	CheckResults []CheckResult `json:"check_results"`
}

// CheckResult holds the outcome of a single verification check.
type CheckResult struct {
	Type    string `json:"type"`
	Target  string `json:"target"`
	Passed  bool   `json:"passed"`
	Details string `json:"details"` // Explanation on failure.
}

// MakeEvalFunc creates an evaluation function for a single Terminal-Bench task.
func MakeEvalFunc(task Task) func(workDir string) (bool, error) {
	return func(workDir string) (bool, error) {
		result, err := Evaluate(workDir, task)
		if err != nil {
			return false, err
		}
		return result.Resolved, nil
	}
}

// Evaluate runs all verification checks for a task.
func Evaluate(workDir string, task Task) (*EvalResult, error) {
	result := &EvalResult{
		TaskID:      task.TaskID,
		ChecksTotal: len(task.Checks) + len(task.ExpectedFiles),
	}

	// Check expected files.
	for _, file := range task.ExpectedFiles {
		cr := checkFileExists(workDir, file)
		result.CheckResults = append(result.CheckResults, cr)
		if cr.Passed {
			result.ChecksPassed++
		}
	}

	// Run defined checks.
	for _, check := range task.Checks {
		cr := runCheck(workDir, check)
		result.CheckResults = append(result.CheckResults, cr)
		if cr.Passed {
			result.ChecksPassed++
		}
	}

	result.Resolved = result.ChecksPassed == result.ChecksTotal
	return result, nil
}

func checkFileExists(workDir, file string) CheckResult {
	path := filepath.Join(workDir, file)
	_, err := os.Stat(path)
	if err == nil {
		return CheckResult{Type: "file_exists", Target: file, Passed: true}
	}
	return CheckResult{
		Type:    "file_exists",
		Target:  file,
		Passed:  false,
		Details: fmt.Sprintf("file not found: %s", file),
	}
}

func runCheck(workDir string, check Check) CheckResult {
	switch check.Type {
	case "file_exists":
		return checkFileExists(workDir, check.Target)
	case "file_contains":
		return checkFileContains(workDir, check.Target, check.Expected)
	case "command_output":
		return checkCommandOutput(workDir, check.Target, check.Expected)
	case "exit_code":
		return checkExitCode(workDir, check.Target, check.Expected)
	default:
		return CheckResult{
			Type:    check.Type,
			Target:  check.Target,
			Passed:  false,
			Details: fmt.Sprintf("unknown check type: %s", check.Type),
		}
	}
}

func checkFileContains(workDir, file, expected string) CheckResult {
	path := filepath.Join(workDir, file)
	data, err := os.ReadFile(path)
	if err != nil {
		return CheckResult{
			Type:    "file_contains",
			Target:  file,
			Passed:  false,
			Details: fmt.Sprintf("cannot read file: %v", err),
		}
	}

	if strings.Contains(string(data), expected) {
		return CheckResult{Type: "file_contains", Target: file, Passed: true}
	}
	return CheckResult{
		Type:    "file_contains",
		Target:  file,
		Passed:  false,
		Details: fmt.Sprintf("file does not contain expected string %q", truncate(expected, 100)),
	}
}

func checkCommandOutput(workDir, command, expected string) CheckResult {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return CheckResult{Type: "command_output", Target: command, Passed: false, Details: "empty command"}
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = workDir
	out, _ := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))

	if strings.Contains(output, expected) {
		return CheckResult{Type: "command_output", Target: command, Passed: true}
	}
	return CheckResult{
		Type:    "command_output",
		Target:  command,
		Passed:  false,
		Details: fmt.Sprintf("output %q does not contain %q", truncate(output, 200), truncate(expected, 100)),
	}
}

func checkExitCode(workDir, command, expected string) CheckResult {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return CheckResult{Type: "exit_code", Target: command, Passed: false, Details: "empty command"}
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = workDir
	err := cmd.Run()

	exitCode := "0"
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = fmt.Sprintf("%d", exitErr.ExitCode())
		} else {
			return CheckResult{
				Type:    "exit_code",
				Target:  command,
				Passed:  false,
				Details: fmt.Sprintf("command error: %v", err),
			}
		}
	}

	if exitCode == expected {
		return CheckResult{Type: "exit_code", Target: command, Passed: true}
	}
	return CheckResult{
		Type:    "exit_code",
		Target:  command,
		Passed:  false,
		Details: fmt.Sprintf("exit code %s, want %s", exitCode, expected),
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
