package cmd

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2/internal/config"
	"h2/internal/session"
	"h2/internal/session/agent/harness"
)

// writeTestSessionMetadata creates a session dir with metadata for testing.
// Uses the real config.SessionDir() since ConfigDir is cached.
func writeTestSessionMetadata(t *testing.T, name string, meta config.SessionMetadata) string {
	t.Helper()
	sessionDir := config.SessionDir(name)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("create session dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(sessionDir) })
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "session.metadata.json"), data, 0o644); err != nil {
		t.Fatalf("write metadata: %v", err)
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

	writeTestSessionMetadata(t, name, config.SessionMetadata{
		AgentName:       name,
		SessionID:       "dry-run-session-uuid",
		Command:         "claude",
		HarnessType:     "claude_code",
		ClaudeConfigDir: claudeConfigDir,
		CWD:             tmpDir,
		Pod:             "test-pod",
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

func TestRunResume_EmptySessionID(t *testing.T) {
	t.Setenv("CLAUDECODE", "")

	name := "resume-test-empty-sid"
	writeTestSessionMetadata(t, name, config.SessionMetadata{
		AgentName: name,
		Command:   "claude",
	})

	cmd := newRunCmd()
	cmd.SetArgs([]string{name, "--resume", "--detach"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for empty session ID")
	}
	if !strings.Contains(err.Error(), "no session ID") {
		t.Errorf("error = %q, want containing 'no session ID'", err.Error())
	}
}

func TestRunResume_UnsupportedHarness(t *testing.T) {
	t.Setenv("CLAUDECODE", "")

	name := "resume-test-codex"
	writeTestSessionMetadata(t, name, config.SessionMetadata{
		AgentName:   name,
		SessionID:   "old-session-id",
		Command:     "codex",
		HarnessType: "codex",
		CWD:         t.TempDir(),
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

func TestRunResume_ForksDaemonWithResumeSessionID(t *testing.T) {
	t.Setenv("CLAUDECODE", "")

	name := "resume-test-fork"
	tmpDir := t.TempDir()
	claudeConfigDir := filepath.Join(tmpDir, "claude-config")
	os.MkdirAll(claudeConfigDir, 0o755)

	sessionDir := writeTestSessionMetadata(t, name, config.SessionMetadata{
		AgentName:       name,
		SessionID:       "previous-session-uuid",
		Command:         "claude",
		HarnessType:     "claude_code",
		ClaudeConfigDir: claudeConfigDir,
		CWD:             tmpDir,
		Pod:             "my-pod",
	})

	// Capture ForkDaemon call.
	var captured session.ForkDaemonOpts
	origFork := forkDaemonFunc
	forkDaemonFunc = func(opts session.ForkDaemonOpts) error {
		captured = opts
		return nil
	}
	defer func() { forkDaemonFunc = origFork }()

	cmd := newRunCmd()
	cmd.SetArgs([]string{name, "--resume", "--detach"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if captured.Name != name {
		t.Errorf("Name = %q, want %q", captured.Name, name)
	}
	if captured.ResumeSessionID != "previous-session-uuid" {
		t.Errorf("ResumeSessionID = %q, want %q", captured.ResumeSessionID, "previous-session-uuid")
	}
	if captured.SessionID == "" {
		t.Error("SessionID should be a new UUID, got empty")
	}
	if captured.SessionID == "previous-session-uuid" {
		t.Error("SessionID should be a NEW UUID, not the old one")
	}
	if captured.Command != "claude" {
		t.Errorf("Command = %q, want %q", captured.Command, "claude")
	}
	if captured.HarnessType != "claude_code" {
		t.Errorf("HarnessType = %q, want %q", captured.HarnessType, "claude_code")
	}
	if captured.Pod != "my-pod" {
		t.Errorf("Pod = %q, want %q", captured.Pod, "my-pod")
	}
	if captured.CWD != tmpDir {
		t.Errorf("CWD = %q, want %q", captured.CWD, tmpDir)
	}
	if captured.SessionDir != sessionDir {
		t.Errorf("SessionDir = %q, want %q", captured.SessionDir, sessionDir)
	}
}

func TestRunResume_InfersHarnessTypeFromCommand(t *testing.T) {
	t.Setenv("CLAUDECODE", "")

	name := "resume-test-infer"
	tmpDir := t.TempDir()
	claudeConfigDir := filepath.Join(tmpDir, "claude-config")
	os.MkdirAll(claudeConfigDir, 0o755)

	// Create metadata without harness_type (simulates old metadata).
	writeTestSessionMetadata(t, name, config.SessionMetadata{
		AgentName:       name,
		SessionID:       "old-uuid",
		Command:         "claude",
		ClaudeConfigDir: claudeConfigDir,
		CWD:             tmpDir,
	})

	var captured session.ForkDaemonOpts
	origFork := forkDaemonFunc
	forkDaemonFunc = func(opts session.ForkDaemonOpts) error {
		captured = opts
		return nil
	}
	defer func() { forkDaemonFunc = origFork }()

	cmd := newRunCmd()
	cmd.SetArgs([]string{name, "--resume", "--detach"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if captured.HarnessType != "claude_code" {
		t.Errorf("HarnessType = %q, want %q (inferred from command)", captured.HarnessType, "claude_code")
	}
}

func TestInferHarnessType(t *testing.T) {
	tests := []struct {
		command string
		want    string
	}{
		{"claude", "claude_code"},
		{"codex", "codex"},
		{"vim", "generic"},
		{"", "generic"},
	}
	for _, tt := range tests {
		if got := inferHarnessType(tt.command); got != tt.want {
			t.Errorf("inferHarnessType(%q) = %q, want %q", tt.command, got, tt.want)
		}
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

func TestSessionMetadata_NewFields(t *testing.T) {
	dir := t.TempDir()
	meta := config.SessionMetadata{
		AgentName:   "test",
		SessionID:   "uuid",
		HarnessType: "claude_code",
		Pod:         "test-pod",
	}
	if err := config.WriteSessionMetadata(dir, meta); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := config.ReadSessionMetadata(dir)
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
