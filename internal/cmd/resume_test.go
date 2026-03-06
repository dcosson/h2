package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2/internal/config"
	"h2/internal/session"
	"h2/internal/session/agent/harness"
)

// writeTestRuntimeConfig creates a session dir with a RuntimeConfig for testing.
func writeTestRuntimeConfig(t *testing.T, name string, rc *config.RuntimeConfig) string {
	t.Helper()
	sessionDir := config.SessionDir(name)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("create session dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(sessionDir) })
	if err := config.WriteRuntimeConfig(sessionDir, rc); err != nil {
		t.Fatalf("write runtime config: %v", err)
	}
	return sessionDir
}

func TestRunResume_RequiresName(t *testing.T) {
	// Unset CLAUDECODE so the safety check doesn't block.
	t.Setenv("CLAUDECODE", "")

	cmd := newRunCmd()
	cmd.SetArgs([]string{"--resume"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for --resume without name")
	}
	if !strings.Contains(err.Error(), "requires an agent name") {
		t.Errorf("error = %q, want containing 'requires an agent name'", err.Error())
	}
}

func TestRunResume_MutuallyExclusiveWithRole(t *testing.T) {
	t.Setenv("CLAUDECODE", "")

	cmd := newRunCmd()
	cmd.SetArgs([]string{"some-agent", "--resume", "--role", "concierge"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for --resume with --role")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %q, want containing 'mutually exclusive'", err.Error())
	}
}

func TestRunResume_RejectsIncompatibleFlags(t *testing.T) {
	t.Setenv("CLAUDECODE", "")

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"var", []string{"agent", "--resume", "--var", "x=1"}, "--var cannot be used with --resume"},
		{"override", []string{"agent", "--resume", "--override", "x=1"}, "--override cannot be used with --resume"},
		{"pod", []string{"agent", "--resume", "--pod", "p1"}, "--pod cannot be used with --resume"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newRunCmd()
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected error for --resume with --%s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want containing %q", err.Error(), tt.want)
			}
		})
	}
}

func TestRunResume_DryRun(t *testing.T) {
	t.Setenv("CLAUDECODE", "")

	name := "resume-test-dry-run"
	tmpDir := t.TempDir()
	claudeConfigDir := filepath.Join(tmpDir, "claude-config")
	os.MkdirAll(claudeConfigDir, 0o755)

	writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               "dry-run-session-uuid",
		HarnessSessionID:        "dry-run-session-uuid",
		Command:                 "claude",
		HarnessType:             "claude_code",
		HarnessConfigPathPrefix: tmpDir,
		Profile:                 "claude-config",
		CWD:                     tmpDir,
		Pod:                     "test-pod",
		StartedAt:               "2024-01-01T00:00:00Z",
	})

	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := newRunCmd()
	cmd.SetArgs([]string{name, "--resume", "--dry-run"})
	err := cmd.Execute()

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf strings.Builder
	io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "dry-run-session-uuid") {
		t.Errorf("dry-run output should contain session ID, got:\n%s", output)
	}
	if !strings.Contains(output, "--resume") {
		t.Errorf("dry-run output should contain --resume flag, got:\n%s", output)
	}
	if !strings.Contains(output, "claude") {
		t.Errorf("dry-run output should contain command name, got:\n%s", output)
	}
}

func TestRunResume_NoSessionMetadata(t *testing.T) {
	t.Setenv("CLAUDECODE", "")

	cmd := newRunCmd()
	cmd.SetArgs([]string{"nonexistent-resume-test-agent", "--resume", "--detach"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for --resume with no session metadata")
	}
	if !strings.Contains(err.Error(), "no session found") {
		t.Errorf("error = %q, want containing 'no session found'", err.Error())
	}
}

func TestRunResume_InvalidConfig(t *testing.T) {
	t.Setenv("CLAUDECODE", "")

	name := "resume-test-invalid"
	sessionDir := config.SessionDir(name)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("create session dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(sessionDir) })

	// Write a file missing required fields — should fail validation.
	metaPath := filepath.Join(sessionDir, "session.metadata.json")
	if err := os.WriteFile(metaPath, []byte(`{"agent_name":"test"}`), 0o644); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := newRunCmd()
	cmd.SetArgs([]string{name, "--resume", "--detach"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid config")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("error = %q, want containing 'invalid'", err.Error())
	}
}

func TestRunResume_UnsupportedHarness(t *testing.T) {
	t.Setenv("CLAUDECODE", "")

	name := "resume-test-codex"
	writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:        name,
		SessionID:        "old-session-id",
		HarnessSessionID: "old-session-id",
		Command:          "codex",
		HarnessType:      "codex",
		CWD:              t.TempDir(),
		StartedAt:        "2024-01-01T00:00:00Z",
	})

	cmd := newRunCmd()
	cmd.SetArgs([]string{name, "--resume", "--detach"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unsupported harness")
	}
	if !strings.Contains(err.Error(), "does not support --resume") {
		t.Errorf("error = %q, want containing 'does not support --resume'", err.Error())
	}
}

func TestRunResume_ForksDaemonWithResumeFlag(t *testing.T) {
	t.Setenv("CLAUDECODE", "")

	name := "resume-test-fork"
	tmpDir := t.TempDir()
	claudeConfigDir := filepath.Join(tmpDir, "claude-config")
	os.MkdirAll(claudeConfigDir, 0o755)

	sessionDir := writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               "session-uuid",
		HarnessSessionID:        "session-uuid",
		Command:                 "claude",
		HarnessType:             "claude_code",
		HarnessConfigPathPrefix: tmpDir,
		Profile:                 "claude-config",
		CWD:                     tmpDir,
		Pod:                     "my-pod",
		StartedAt:               "2024-01-01T00:00:00Z",
	})

	// Capture ForkDaemon call.
	var capturedSessionDir string
	var capturedResume bool
	origFork := forkDaemonFunc
	forkDaemonFunc = func(sd string, hints session.TerminalHints, resume bool) error {
		capturedSessionDir = sd
		capturedResume = resume
		return nil
	}
	defer func() { forkDaemonFunc = origFork }()

	cmd := newRunCmd()
	cmd.SetArgs([]string{name, "--resume", "--detach"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedSessionDir != sessionDir {
		t.Errorf("SessionDir = %q, want %q", capturedSessionDir, sessionDir)
	}
	if !capturedResume {
		t.Error("ForkDaemon should be called with resume=true")
	}

	// RuntimeConfig should be unchanged (same session, no resume field persisted).
	rc, err := config.ReadRuntimeConfig(sessionDir)
	if err != nil {
		t.Fatalf("read runtime config after fork: %v", err)
	}
	if rc.SessionID != "session-uuid" {
		t.Errorf("SessionID = %q, want %q (should be unchanged)", rc.SessionID, "session-uuid")
	}
	if rc.HarnessSessionID != "session-uuid" {
		t.Errorf("HarnessSessionID = %q, want %q", rc.HarnessSessionID, "session-uuid")
	}
	if rc.Command != "claude" {
		t.Errorf("Command = %q, want %q", rc.Command, "claude")
	}
	if rc.Pod != "my-pod" {
		t.Errorf("Pod = %q, want %q", rc.Pod, "my-pod")
	}
}

func TestClaudeHarness_BuildCommandArgs_Resume(t *testing.T) {
	cfg := harness.CommandArgsConfig{
		ResumeSessionID: "abc-123",
		SessionID:       "new-uuid",
		Model:           "opus",
		Instructions:    "Be helpful",
	}

	h, err := harness.Resolve(harness.HarnessConfig{
		HarnessType: "claude_code",
		Command:     "claude",
	}, nil)
	if err != nil {
		t.Fatalf("resolve harness: %v", err)
	}

	args := h.BuildCommandArgs(cfg)

	// Should contain --resume with old session ID.
	foundResume := false
	for i, arg := range args {
		if arg == "--resume" && i+1 < len(args) && args[i+1] == "abc-123" {
			foundResume = true
		}
	}
	if !foundResume {
		t.Errorf("args should contain '--resume abc-123', got %v", args)
	}

	// Should NOT contain --session-id, --model, or --append-system-prompt.
	for _, arg := range args {
		if arg == "--session-id" {
			t.Errorf("resume args should not contain --session-id, got %v", args)
		}
		if arg == "--model" {
			t.Errorf("resume args should not contain --model, got %v", args)
		}
		if arg == "--append-system-prompt" {
			t.Errorf("resume args should not contain --append-system-prompt, got %v", args)
		}
	}
}

func TestClaudeHarness_SupportsResume(t *testing.T) {
	h, _ := harness.Resolve(harness.HarnessConfig{HarnessType: "claude_code", Command: "claude"}, nil)
	if !h.SupportsResume() {
		t.Error("Claude Code should support resume")
	}
}

func TestCodexHarness_SupportsResume(t *testing.T) {
	h, _ := harness.Resolve(harness.HarnessConfig{HarnessType: "codex", Command: "codex"}, nil)
	if h.SupportsResume() {
		t.Error("Codex should not support resume")
	}
}

func TestGenericHarness_SupportsResume(t *testing.T) {
	h, _ := harness.Resolve(harness.HarnessConfig{HarnessType: "generic", Command: "vim"}, nil)
	if h.SupportsResume() {
		t.Error("Generic should not support resume")
	}
}

func TestRuntimeConfig_NewFields(t *testing.T) {
	dir := t.TempDir()
	rc := &config.RuntimeConfig{
		AgentName:   "test",
		SessionID:   "uuid",
		HarnessType: "claude_code",
		Pod:         "test-pod",
		Command:     "claude",
		CWD:         "/tmp",
	}
	if err := config.WriteRuntimeConfig(dir, rc); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := config.ReadRuntimeConfig(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.HarnessType != "claude_code" {
		t.Errorf("HarnessType = %q, want %q", got.HarnessType, "claude_code")
	}
	if got.Pod != "test-pod" {
		t.Errorf("Pod = %q, want %q", got.Pod, "test-pod")
	}
}
