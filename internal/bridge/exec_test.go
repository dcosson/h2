package bridge

import (
	"strings"
	"testing"
	"time"
)

func TestExecCommand_Success(t *testing.T) {
	got := ExecCommand("echo", "hello")
	if got != "hello" {
		t.Errorf("ExecCommand(echo, hello) = %q, want %q", got, "hello")
	}
}

func TestExecCommand_Failure(t *testing.T) {
	got := ExecCommand("false", "")
	if !strings.HasPrefix(got, "ERROR (exit 1)") {
		t.Errorf("ExecCommand(false) = %q, want prefix %q", got, "ERROR (exit 1)")
	}
}

func TestExecCommand_NotFound(t *testing.T) {
	got := ExecCommand("nonexistent_cmd_xyz", "")
	if !strings.Contains(got, "not found") {
		t.Errorf("ExecCommand(nonexistent) = %q, want to contain 'not found'", got)
	}
}

func TestExecCommand_LongOutput(t *testing.T) {
	// ExecCommand no longer truncates — it returns the full output.
	// Paging is handled by the bridge's Send method.
	got := ExecCommand("python3", "-c \"print('A' * 5000)\"")
	if len(got) != 5000 {
		t.Errorf("ExecCommand(long output) len = %d, want 5000", len(got))
	}
}

func TestExecCommand_Timeout(t *testing.T) {
	orig := ExecCommandTimeout
	ExecCommandTimeout = 100 * time.Millisecond
	defer func() { ExecCommandTimeout = orig }()

	got := ExecCommand("sleep", "10")
	if !strings.Contains(got, "timeout after 30s") {
		t.Errorf("ExecCommand(sleep 10) = %q, want timeout message", got)
	}
}

func TestExecCommand_EmptyOutput(t *testing.T) {
	got := ExecCommand("true", "")
	if got != "(no output)" {
		t.Errorf("ExecCommand(true) = %q, want %q", got, "(no output)")
	}
}

func TestExecCommand_ArgumentQuoting(t *testing.T) {
	got := ExecCommand("echo", "'hello world'")
	if got != "hello world" {
		t.Errorf("ExecCommand(echo, 'hello world') = %q, want %q", got, "hello world")
	}
}

