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

func TestExecCommand_Truncation(t *testing.T) {
	// Generate a string longer than maxOutputLen via repeated echo.
	// Use printf to avoid newline issues; repeat 'A' 5000 times.
	got := ExecCommand("python3", "-c \"print('A' * 5000)\"")
	if !strings.HasSuffix(got, "\n... (truncated)") {
		t.Errorf("ExecCommand(long output) should be truncated, got suffix: %q",
			got[max(0, len(got)-30):])
	}
	// The truncated output should have maxOutputLen runes + suffix.
	runes := []rune(got)
	expectedLen := maxOutputLen + len([]rune("\n... (truncated)"))
	if len(runes) != expectedLen {
		t.Errorf("truncated output length = %d runes, want %d", len(runes), expectedLen)
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

func TestTruncateOutput(t *testing.T) {
	short := "hello"
	if got := truncateOutput(short); got != short {
		t.Errorf("truncateOutput(%q) = %q, want unchanged", short, got)
	}

	long := strings.Repeat("x", maxOutputLen+100)
	got := truncateOutput(long)
	if !strings.HasSuffix(got, "\n... (truncated)") {
		t.Errorf("truncateOutput(long) should end with truncation suffix")
	}
	runes := []rune(got)
	expectedLen := maxOutputLen + len([]rune("\n... (truncated)"))
	if len(runes) != expectedLen {
		t.Errorf("truncateOutput length = %d runes, want %d", len(runes), expectedLen)
	}
}
