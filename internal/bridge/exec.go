package bridge

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/google/shlex"
)

// ExecCommandTimeout is the default timeout for command execution.
// Exposed as a variable so tests can override it.
var ExecCommandTimeout = 30 * time.Second

// ExecCommand runs a whitelisted command and returns the formatted output.
func ExecCommand(command, args string) string {
	path, err := exec.LookPath(command)
	if err != nil {
		return fmt.Sprintf("ERROR: command %q not found in PATH", command)
	}

	argv, err := shlex.Split(args)
	if err != nil {
		return fmt.Sprintf("ERROR: invalid arguments: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), ExecCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, argv...)
	output, err := cmd.CombinedOutput()

	result := strings.TrimRight(string(output), "\n")
	if err != nil {
		exitCode := 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Sprintf("ERROR (timeout after 30s):\n%s", result)
		}
		return fmt.Sprintf("ERROR (exit %d):\n%s", exitCode, result)
	}

	if result == "" {
		return "(no output)"
	}
	return result
}
