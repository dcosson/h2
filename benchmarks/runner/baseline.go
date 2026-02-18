package runner

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"h2/internal/sandbox"
)

const defaultMaxTurns = 100

// presetModel maps sandbox preset names to Claude --model flag values.
var presetModel = map[string]string{
	"haiku": "haiku",
	"opus":  "opus",
}

// RunBaseline runs a single Claude Code agent non-interactively against an issue prompt.
// It uses --print mode with --model derived from the sandbox preset.
// Auth uses the native ~/.claude/ config (OAuth tokens are Keychain-bound to the config dir
// they were created in, so sandbox-copied .claude.json files don't work).
func RunBaseline(ctx context.Context, sb *sandbox.Sandbox, workDir, issue string, maxTurns int) error {
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}

	args := []string{
		"--print",
		"--max-turns", fmt.Sprintf("%d", maxTurns),
		"--dangerously-skip-permissions",
	}

	// Set model from preset if known.
	if model, ok := presetModel[sb.Preset]; ok {
		args = append(args, "--model", model)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = workDir
	// Filter CLAUDECODE to avoid "nested session" error when running inside Claude Code.
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
