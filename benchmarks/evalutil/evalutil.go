// Package evalutil provides shared utilities for benchmark evaluation pipelines.
// Functions here are used by multiple benchmark packages to avoid duplication.
package evalutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ApplyPatch applies a unified diff patch to the working directory.
// Tries multiple git apply strategies, then falls back to patch(1).
func ApplyPatch(workDir, patch string) error {
	if patch == "" {
		return nil
	}

	patchFile := filepath.Join(workDir, ".bench_test_patch.diff")
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
			lastErr = fmt.Errorf("%s: %s: %w", args[0], Truncate(string(out), 500), err)
		}
	}

	return fmt.Errorf("all patch strategies failed: %w", lastErr)
}

// ParsePytestOutput parses verbose pytest output to extract per-test results.
// Looks for lines ending with PASSED, FAILED, or ERROR.
//
// Example pytest output:
//
//	test_file.py::TestClass::test_method PASSED
//	test_file.py::TestClass::test_other FAILED
func ParsePytestOutput(output string) map[string]bool {
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

// CheckTestsPassed verifies that all specified test IDs are in the results and passed.
func CheckTestsPassed(testIDs []string, results map[string]bool) bool {
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

// ParsePytestSummary extracts passed/total from pytest summary output.
// Looks for patterns like "5 passed, 2 failed" or "10 passed".
func ParsePytestSummary(output string) (passed, total int) {
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

// RunShellCmd splits a command string on whitespace and runs it in the given directory.
func RunShellCmd(workDir, command string) (string, error) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "", fmt.Errorf("empty command")
	}
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Truncate returns s truncated to max characters with "..." appended if truncated.
func Truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
