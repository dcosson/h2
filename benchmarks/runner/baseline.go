package runner

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"h2/internal/sandbox"
)

const defaultMaxTurns = 100

// RunBaseline runs a single Claude Code agent non-interactively against an issue prompt.
// It uses --print mode with the sandbox's CLAUDE_CONFIG_DIR for settings and auth.
func RunBaseline(ctx context.Context, sb *sandbox.Sandbox, workDir, issue string, maxTurns int) error {
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}

	cmd := exec.CommandContext(ctx, "claude",
		"--print",
		"--max-turns", fmt.Sprintf("%d", maxTurns),
	)
	cmd.Dir = workDir
	cmd.Env = append(cmd.Environ(),
		"CLAUDE_CONFIG_DIR="+sb.ClaudeConfigDir(),
	)
	cmd.Stdin = strings.NewReader(issue)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("claude exited with error: %w\noutput: %s", err, truncateOutput(out, 2000))
	}
	return nil
}

func truncateOutput(out []byte, max int) string {
	if len(out) <= max {
		return string(out)
	}
	return string(out[:max]) + "\n... (truncated)"
}
