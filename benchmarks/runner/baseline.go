package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"h2/internal/sandbox"
	"h2/internal/session"
)

const defaultMaxTurns = 100

// presetModel maps sandbox preset names to Claude --model flag values.
var presetModel = map[string]string{
	"haiku": "haiku",
	"opus":  "opus",
}

// RunBaseline runs a single Claude Code agent non-interactively via h2 run --print.
// This gives the agent the same env setup as a regular h2 run (CLAUDE_CONFIG_DIR,
// OTEL telemetry, hooks) while running headlessly.
func RunBaseline(ctx context.Context, sb *sandbox.Sandbox, workDir, issue string, maxTurns int) error {
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}

	// Build the args that will be passed through to claude after --print.
	claudeArgs := []string{
		"--print",
		"--max-turns", fmt.Sprintf("%d", maxTurns),
		"--dangerously-skip-permissions",
	}

	// Set model from preset if known.
	if model, ok := presetModel[sb.Preset]; ok {
		claudeArgs = append(claudeArgs, "--model", model)
	}

	// Use a temp file for metrics.
	metricsFile := filepath.Join(workDir, ".h2_metrics.json")
	defer os.Remove(metricsFile)

	var stderr bytes.Buffer
	err := session.RunPrint(session.PrintOpts{
		Command:     "claude",
		Args:        claudeArgs,
		CWD:         workDir,
		MetricsFile: metricsFile,
		Stdin:       strings.NewReader(issue),
		Stderr:      &stderr,
	})
	if err != nil {
		return fmt.Errorf("h2 run --print exited with error: %w\nstderr: %s", err, truncateOutput(stderr.Bytes(), 2000))
	}
	return nil
}

// LoadPrintMetrics reads a metrics file written by h2 run --print.
func LoadPrintMetrics(path string) (*session.PrintMetrics, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m session.PrintMetrics
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse metrics: %w", err)
	}
	return &m, nil
}

// RunBaselineDirect runs claude --print directly without h2 run.
// Used as a fallback or when h2 binary is not available.
func RunBaselineDirect(ctx context.Context, sb *sandbox.Sandbox, workDir, issue string, maxTurns int) error {
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}

	args := []string{
		"--print",
		"--max-turns", fmt.Sprintf("%d", maxTurns),
		"--dangerously-skip-permissions",
	}

	if model, ok := presetModel[sb.Preset]; ok {
		args = append(args, "--model", model)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = workDir
	cmd.Env = filterEnv(cmd.Environ(), "CLAUDECODE")
	cmd.Stdin = strings.NewReader(issue)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("claude exited with error: %w\noutput: %s", err, truncateOutput(out, 2000))
	}
	return nil
}

func filterEnv(env []string, prefix string) []string {
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, prefix+"=") {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

func truncateOutput(out []byte, max int) string {
	if len(out) <= max {
		return string(out)
	}
	return string(out[:max]) + "\n... (truncated)"
}
