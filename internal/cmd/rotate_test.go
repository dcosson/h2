package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"h2/internal/config"
	"h2/internal/session"
)

// setupRotateTestH2Dir creates an isolated fake h2 directory so rotate tests
// never touch the real config dir. Returns the h2 dir path.
func setupRotateTestH2Dir(t *testing.T) string {
	t.Helper()
	config.ResetResolveCache()
	t.Cleanup(config.ResetResolveCache)

	h2Dir := filepath.Join(t.TempDir(), "h2")
	for _, sub := range []string{"sessions", "sockets"} {
		if err := os.MkdirAll(filepath.Join(h2Dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.WriteMarker(h2Dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("H2_DIR", h2Dir)
	return h2Dir
}

func TestRotate_NoSession(t *testing.T) {
	setupRotateTestH2Dir(t)

	cmd := newRotateCmd()
	cmd.SetArgs([]string{"nonexistent-rotate-agent", "staging"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
	if !strings.Contains(err.Error(), "no session found") {
		t.Errorf("error = %q, want containing 'no session found'", err.Error())
	}
}

func TestRotate_AlreadyOnProfile(t *testing.T) {
	setupRotateTestH2Dir(t)
	name := "rotate-test-same-profile"
	tmpDir := t.TempDir()

	writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               "sid-1",
		HarnessSessionID:        "sid-1",
		HarnessType:             "claude_code",
		HarnessConfigPathPrefix: tmpDir,
		Profile:                 "default",
		Command:                 "claude",
		CWD:                     tmpDir,
		StartedAt:               "2024-01-01T00:00:00Z",
	})

	cmd := newRotateCmd()
	cmd.SetArgs([]string{name, "default"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for same profile")
	}
	if !strings.Contains(err.Error(), "already using profile") {
		t.Errorf("error = %q, want containing 'already using profile'", err.Error())
	}
}

func TestRotate_ProfileNotFound(t *testing.T) {
	setupRotateTestH2Dir(t)
	name := "rotate-test-no-profile"
	tmpDir := t.TempDir()
	// Create the current profile dir but not the target.
	os.MkdirAll(filepath.Join(tmpDir, "default"), 0o755)

	writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               "sid-1",
		HarnessSessionID:        "sid-1",
		HarnessType:             "claude_code",
		HarnessConfigPathPrefix: tmpDir,
		Profile:                 "default",
		Command:                 "claude",
		CWD:                     tmpDir,
		StartedAt:               "2024-01-01T00:00:00Z",
	})

	cmd := newRotateCmd()
	cmd.SetArgs([]string{name, "nonexistent-profile"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing profile dir")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want containing 'not found'", err.Error())
	}
}

func TestRotate_Success(t *testing.T) {
	setupRotateTestH2Dir(t)
	name := "rotate-test-success"
	tmpDir := t.TempDir()
	cwd := "/Users/testuser/projects/myapp"

	// Create old and new profile dirs.
	os.MkdirAll(filepath.Join(tmpDir, "default"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "staging"), 0o755)

	// Create a native session log in the old profile.
	sanitizedCWD := strings.ReplaceAll(cwd, string(filepath.Separator), "-")
	oldLogDir := filepath.Join(tmpDir, "default", "projects", sanitizedCWD)
	os.MkdirAll(oldLogDir, 0o755)
	oldLogPath := filepath.Join(oldLogDir, "sid-1.jsonl")
	os.WriteFile(oldLogPath, []byte(`{"test":"data"}`), 0o644)

	sessionDir := writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               "sid-1",
		HarnessSessionID:        "sid-1",
		HarnessType:             "claude_code",
		HarnessConfigPathPrefix: tmpDir,
		Profile:                 "default",
		NativeLogPathSuffix:     filepath.Join("projects", sanitizedCWD, "sid-1.jsonl"),
		Command:                 "claude",
		CWD:                     cwd,
		StartedAt:               "2024-01-01T00:00:00Z",
	})

	cmd := newRotateCmd()
	cmd.SetArgs([]string{name, "staging"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify profile updated in metadata.
	rc, err := config.ReadRuntimeConfig(sessionDir)
	if err != nil {
		t.Fatalf("read config after rotate: %v", err)
	}
	if rc.Profile != "staging" {
		t.Errorf("Profile = %q, want %q", rc.Profile, "staging")
	}

	// Verify session log moved.
	if _, err := os.Stat(oldLogPath); !os.IsNotExist(err) {
		t.Error("old session log should not exist after rotate")
	}
	newLogDir := filepath.Join(tmpDir, "staging", "projects", sanitizedCWD)
	newLogPath := filepath.Join(newLogDir, "sid-1.jsonl")
	if _, err := os.Stat(newLogPath); err != nil {
		t.Errorf("new session log should exist: %v", err)
	}
}

// TestRotate_CodexDiscoversSessionLog verifies that rotating a codex agent
// with an empty NativeLogPathSuffix (which happens when the async discovery
// didn't persist) still finds and moves the session log by globbing for the
// conversation ID in the old profile's sessions directory.
func TestRotate_CodexDiscoversSessionLog(t *testing.T) {
	setupRotateTestH2Dir(t)
	name := "rotate-test-codex-discover"
	tmpDir := t.TempDir()
	convID := "conv-abc-123"

	// Create old and new profile dirs.
	os.MkdirAll(filepath.Join(tmpDir, "profile-a"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "profile-b"), 0o755)

	// Create a codex session log in the old profile with the expected naming
	// convention: sessions/YYYY/MM/DD/rollout-<timestamp>-<convID>.jsonl
	oldLogDir := filepath.Join(tmpDir, "profile-a", "sessions", "2026", "03", "27")
	os.MkdirAll(oldLogDir, 0o755)
	logContent := `{"role":"user","content":"hello"}`
	oldLogPath := filepath.Join(oldLogDir, "rollout-1711540800-"+convID+".jsonl")
	os.WriteFile(oldLogPath, []byte(logContent), 0o644)

	// Write RuntimeConfig with empty NativeLogPathSuffix (simulating the
	// async discovery race condition where it was never persisted).
	sessionDir := writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               "internal-sid",
		HarnessSessionID:        convID,
		HarnessType:             "codex",
		HarnessConfigPathPrefix: tmpDir,
		Profile:                 "profile-a",
		NativeLogPathSuffix:     "", // not persisted — this is the bug
		Command:                 "codex",
		CWD:                     tmpDir,
		StartedAt:               "2024-01-01T00:00:00Z",
	})

	cmd := newRotateCmd()
	cmd.SetArgs([]string{name, "profile-b"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify profile updated.
	rc, err := config.ReadRuntimeConfig(sessionDir)
	if err != nil {
		t.Fatalf("read config after rotate: %v", err)
	}
	if rc.Profile != "profile-b" {
		t.Errorf("Profile = %q, want %q", rc.Profile, "profile-b")
	}

	// Verify NativeLogPathSuffix was discovered and persisted.
	wantSuffix := filepath.Join("sessions", "2026", "03", "27", "rollout-1711540800-"+convID+".jsonl")
	if rc.NativeLogPathSuffix != wantSuffix {
		t.Errorf("NativeLogPathSuffix = %q, want %q", rc.NativeLogPathSuffix, wantSuffix)
	}

	// Verify old log was moved.
	if _, err := os.Stat(oldLogPath); !os.IsNotExist(err) {
		t.Error("old session log should not exist after rotate")
	}

	// Verify new log exists with correct content.
	newLogPath := filepath.Join(tmpDir, "profile-b", wantSuffix)
	data, err := os.ReadFile(newLogPath)
	if err != nil {
		t.Fatalf("new session log should exist: %v", err)
	}
	if string(data) != logContent {
		t.Errorf("log content = %q, want %q", string(data), logContent)
	}
}

// TestRotate_CodexWithSuffix verifies that rotating a codex agent where
// NativeLogPathSuffix IS already set works correctly (no discovery needed).
func TestRotate_CodexWithSuffix(t *testing.T) {
	setupRotateTestH2Dir(t)
	name := "rotate-test-codex-suffix"
	tmpDir := t.TempDir()
	convID := "conv-def-456"

	os.MkdirAll(filepath.Join(tmpDir, "profile-a"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "profile-b"), 0o755)

	logSuffix := filepath.Join("sessions", "2026", "03", "27", "rollout-1711540800-"+convID+".jsonl")
	oldLogPath := filepath.Join(tmpDir, "profile-a", logSuffix)
	os.MkdirAll(filepath.Dir(oldLogPath), 0o755)
	logContent := `{"role":"assistant","content":"hi"}`
	os.WriteFile(oldLogPath, []byte(logContent), 0o644)

	writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               "internal-sid",
		HarnessSessionID:        convID,
		HarnessType:             "codex",
		HarnessConfigPathPrefix: tmpDir,
		Profile:                 "profile-a",
		NativeLogPathSuffix:     logSuffix,
		Command:                 "codex",
		CWD:                     tmpDir,
		StartedAt:               "2024-01-01T00:00:00Z",
	})

	cmd := newRotateCmd()
	cmd.SetArgs([]string{name, "profile-b"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify log moved.
	if _, err := os.Stat(oldLogPath); !os.IsNotExist(err) {
		t.Error("old log should not exist")
	}
	newLogPath := filepath.Join(tmpDir, "profile-b", logSuffix)
	data, err := os.ReadFile(newLogPath)
	if err != nil {
		t.Fatalf("new log should exist: %v", err)
	}
	if string(data) != logContent {
		t.Errorf("content = %q, want %q", string(data), logContent)
	}
}

func TestRotate_GenericHarness_NoLogMove(t *testing.T) {
	setupRotateTestH2Dir(t)
	name := "rotate-test-generic"
	tmpDir := t.TempDir()

	// Create profile dirs.
	os.MkdirAll(filepath.Join(tmpDir, "default"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "staging"), 0o755)

	sessionDir := writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               "sid-1",
		HarnessType:             "generic",
		HarnessConfigPathPrefix: tmpDir,
		Profile:                 "default",
		Command:                 "vim",
		CWD:                     tmpDir,
		StartedAt:               "2024-01-01T00:00:00Z",
	})

	cmd := newRotateCmd()
	cmd.SetArgs([]string{name, "staging"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rc, err := config.ReadRuntimeConfig(sessionDir)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if rc.Profile != "staging" {
		t.Errorf("Profile = %q, want %q", rc.Profile, "staging")
	}
}

func TestRotate_StoppedAgent_NoResume(t *testing.T) {
	setupRotateTestH2Dir(t)
	name := "rotate-test-stopped"
	tmpDir := t.TempDir()

	os.MkdirAll(filepath.Join(tmpDir, "default"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "staging"), 0o755)

	sessionDir := writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               "sid-1",
		HarnessSessionID:        "sid-1",
		HarnessType:             "claude_code",
		HarnessConfigPathPrefix: tmpDir,
		Profile:                 "default",
		Command:                 "claude",
		CWD:                     tmpDir,
		StartedAt:               "2024-01-01T00:00:00Z",
	})

	// Mock forkDaemonFunc to detect if it's called.
	forkCalled := false
	origFork := forkDaemonFunc
	forkDaemonFunc = func(sd string, hints session.TerminalHints, resume bool) error {
		forkCalled = true
		return nil
	}
	defer func() { forkDaemonFunc = origFork }()

	// Agent is not running (no socket) — should rotate without resuming.
	cmd := newRotateCmd()
	cmd.SetArgs([]string{name, "staging"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rc, err := config.ReadRuntimeConfig(sessionDir)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if rc.Profile != "staging" {
		t.Errorf("Profile = %q, want %q", rc.Profile, "staging")
	}
	if forkCalled {
		t.Error("fork should not be called for a stopped agent")
	}
}

func TestRotate_EmptyProfileDefaultsToDefault(t *testing.T) {
	setupRotateTestH2Dir(t)
	name := "rotate-test-empty-profile"
	tmpDir := t.TempDir()

	// Create "default" and "staging" profile dirs.
	os.MkdirAll(filepath.Join(tmpDir, "default"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "staging"), 0o755)

	// Profile is "" which defaults to "default" in the check.
	sessionDir := writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               "sid-1",
		HarnessSessionID:        "sid-1",
		HarnessType:             "claude_code",
		HarnessConfigPathPrefix: tmpDir,
		Profile:                 "", // empty → treated as "default"
		Command:                 "claude",
		CWD:                     tmpDir,
		StartedAt:               "2024-01-01T00:00:00Z",
	})

	// Should fail if target is "default" since empty defaults to it.
	cmd := newRotateCmd()
	cmd.SetArgs([]string{name, "default"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for rotating to same (default) profile")
	}
	if !strings.Contains(err.Error(), "already using profile") {
		t.Errorf("error = %q, want containing 'already using profile'", err.Error())
	}

	// Should succeed if target is "staging".
	cmd2 := newRotateCmd()
	cmd2.SetArgs([]string{name, "staging"})
	err = cmd2.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rc, err := config.ReadRuntimeConfig(sessionDir)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if rc.Profile != "staging" {
		t.Errorf("Profile = %q, want %q", rc.Profile, "staging")
	}
}

func TestRotate_SessionLogContentPreserved(t *testing.T) {
	setupRotateTestH2Dir(t)
	name := "rotate-test-content"
	tmpDir := t.TempDir()
	cwd := "/Users/testuser/myproject"

	os.MkdirAll(filepath.Join(tmpDir, "old-profile"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "new-profile"), 0o755)

	// Create a session log with specific content.
	sanitizedCWD := strings.ReplaceAll(cwd, string(filepath.Separator), "-")
	oldLogDir := filepath.Join(tmpDir, "old-profile", "projects", sanitizedCWD)
	os.MkdirAll(oldLogDir, 0o755)
	logContent := `{"role":"user","content":"hello"}
{"role":"assistant","content":"hi there"}
`
	oldLogPath := filepath.Join(oldLogDir, "sid-content.jsonl")
	os.WriteFile(oldLogPath, []byte(logContent), 0o644)

	writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               "sid-content",
		HarnessSessionID:        "sid-content",
		HarnessType:             "claude_code",
		HarnessConfigPathPrefix: tmpDir,
		Profile:                 "old-profile",
		NativeLogPathSuffix:     filepath.Join("projects", sanitizedCWD, "sid-content.jsonl"),
		Command:                 "claude",
		CWD:                     cwd,
		StartedAt:               "2024-01-01T00:00:00Z",
	})

	cmd := newRotateCmd()
	cmd.SetArgs([]string{name, "new-profile"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	newLogPath := filepath.Join(tmpDir, "new-profile", "projects", sanitizedCWD, "sid-content.jsonl")
	data, err := os.ReadFile(newLogPath)
	if err != nil {
		t.Fatalf("read new log: %v", err)
	}
	if string(data) != logContent {
		t.Errorf("log content changed after move:\ngot:  %q\nwant: %q", string(data), logContent)
	}
}

func TestRotate_ClaudeCode_MissingNativeLogSuffix_ComputesAndMoves(t *testing.T) {
	setupRotateTestH2Dir(t)
	name := "rotate-test-missing-suffix"
	tmpDir := t.TempDir()
	cwd := "/Users/testuser/myproject"

	os.MkdirAll(filepath.Join(tmpDir, "old-profile"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "new-profile"), 0o755)

	// Create a session log in the old profile (as Claude Code would).
	sanitizedCWD := strings.ReplaceAll(cwd, string(filepath.Separator), "-")
	oldLogDir := filepath.Join(tmpDir, "old-profile", "projects", sanitizedCWD)
	os.MkdirAll(oldLogDir, 0o755)
	logContent := `{"role":"user","content":"test"}`
	os.WriteFile(filepath.Join(oldLogDir, "sid-missing.jsonl"), []byte(logContent), 0o644)

	// Simulate the bug: NativeLogPathSuffix is empty (not persisted to disk).
	writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               "sid-missing",
		HarnessSessionID:        "sid-missing",
		HarnessType:             "claude_code",
		HarnessConfigPathPrefix: tmpDir,
		Profile:                 "old-profile",
		NativeLogPathSuffix:     "", // <-- the bug: not persisted
		Command:                 "claude",
		CWD:                     cwd,
		StartedAt:               "2024-01-01T00:00:00Z",
	})

	cmd := newRotateCmd()
	cmd.SetArgs([]string{name, "new-profile"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The log should have been moved to the new profile.
	newLogPath := filepath.Join(tmpDir, "new-profile", "projects", sanitizedCWD, "sid-missing.jsonl")
	data, err := os.ReadFile(newLogPath)
	if err != nil {
		t.Fatalf("session log not found in new profile after rotate: %v", err)
	}
	if string(data) != logContent {
		t.Errorf("log content = %q, want %q", string(data), logContent)
	}
}

func TestRotate_NoExistingLog_SucceedsWithoutMove(t *testing.T) {
	setupRotateTestH2Dir(t)
	name := "rotate-test-no-log"
	tmpDir := t.TempDir()

	os.MkdirAll(filepath.Join(tmpDir, "default"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "staging"), 0o755)

	// No session log file exists in the old profile dir.
	sessionDir := writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               "sid-1",
		HarnessSessionID:        "sid-1",
		HarnessType:             "claude_code",
		HarnessConfigPathPrefix: tmpDir,
		Profile:                 "default",
		Command:                 "claude",
		CWD:                     tmpDir,
		StartedAt:               "2024-01-01T00:00:00Z",
	})

	cmd := newRotateCmd()
	cmd.SetArgs([]string{name, "staging"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rc, err := config.ReadRuntimeConfig(sessionDir)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if rc.Profile != "staging" {
		t.Errorf("Profile = %q, want %q", rc.Profile, "staging")
	}
}

func TestRotate_WrongArgCount(t *testing.T) {
	// Zero args should fail (need at least agent name).
	cmd := newRotateCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for zero args")
	}
}

func TestRotate_NoHarnessConfigPrefix(t *testing.T) {
	setupRotateTestH2Dir(t)
	name := "rotate-test-no-prefix"

	writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:   name,
		SessionID:   "sid-1",
		HarnessType: "claude_code",
		Profile:     "default",
		Command:     "claude",
		CWD:         "/tmp",
		StartedAt:   "2024-01-01T00:00:00Z",
	})

	cmd := newRotateCmd()
	cmd.SetArgs([]string{name, "staging"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing harness config path prefix")
	}
	if !strings.Contains(err.Error(), "no harness config path prefix") {
		t.Errorf("error = %q, want containing 'no harness config path prefix'", err.Error())
	}
}

func TestSelectNextProfile(t *testing.T) {
	tests := []struct {
		name       string
		current    string
		candidates []string
		want       string
	}{
		{
			name:       "current in list, select next",
			current:    "b",
			candidates: []string{"a", "b", "c"},
			want:       "c",
		},
		{
			name:       "current is last, wrap around",
			current:    "c",
			candidates: []string{"a", "b", "c"},
			want:       "a",
		},
		{
			name:       "current is first, select second",
			current:    "a",
			candidates: []string{"a", "b", "c"},
			want:       "b",
		},
		{
			name:       "current not in list, select first",
			current:    "x",
			candidates: []string{"a", "b", "c"},
			want:       "a",
		},
		{
			name:       "single candidate same as current wraps to itself",
			current:    "a",
			candidates: []string{"a"},
			want:       "a",
		},
		{
			name:       "single candidate different from current",
			current:    "x",
			candidates: []string{"a"},
			want:       "a",
		},
		{
			name:       "two candidates, current is first",
			current:    "a",
			candidates: []string{"a", "b"},
			want:       "b",
		},
		{
			name:       "two candidates, current is second",
			current:    "b",
			candidates: []string{"a", "b"},
			want:       "a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectNextProfile(tt.current, tt.candidates)
			if got != tt.want {
				t.Errorf("selectNextProfile(%q, %v) = %q, want %q", tt.current, tt.candidates, got, tt.want)
			}
		})
	}
}

func TestResolveRotateCandidates(t *testing.T) {
	// Create a temp dir simulating a harness config prefix with profile subdirs.
	configPrefix := t.TempDir()
	for _, name := range []string{"default", "staging-1", "staging-2", "staging-3", "prod"} {
		os.MkdirAll(filepath.Join(configPrefix, name), 0o755)
	}

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "no args returns all sorted",
			args: nil,
			want: []string{"default", "prod", "staging-1", "staging-2", "staging-3"},
		},
		{
			name: "literal args preserve order",
			args: []string{"staging-2", "staging-1"},
			want: []string{"staging-2", "staging-1"},
		},
		{
			name: "glob expands and sorts",
			args: []string{"staging-*"},
			want: []string{"staging-1", "staging-2", "staging-3"},
		},
		{
			name: "mixed literal and glob, literals first in order",
			args: []string{"prod", "staging-*"},
			want: []string{"prod", "staging-1", "staging-2", "staging-3"},
		},
		{
			name: "dedup between literal and glob",
			args: []string{"staging-1", "staging-*"},
			want: []string{"staging-1", "staging-2", "staging-3"},
		},
		{
			name: "glob with no matches returns empty",
			args: []string{"nonexistent-*"},
			want: nil,
		},
		{
			name: "single literal",
			args: []string{"prod"},
			want: []string{"prod"},
		},
		{
			name: "question mark glob",
			args: []string{"staging-?"},
			want: []string{"staging-1", "staging-2", "staging-3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveRotateCandidates(tt.args, configPrefix)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("resolveRotateCandidates(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestRotate_AutoSelectWithCandidates(t *testing.T) {
	setupRotateTestH2Dir(t)
	name := "rotate-test-autoselect"
	tmpDir := t.TempDir()

	// Create profile dirs.
	os.MkdirAll(filepath.Join(tmpDir, "default"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "staging"), 0o755)

	sessionDir := writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               "sid-1",
		HarnessSessionID:        "sid-1",
		HarnessType:             "claude_code",
		HarnessConfigPathPrefix: tmpDir,
		Profile:                 "default",
		Command:                 "claude",
		CWD:                     tmpDir,
		StartedAt:               "2024-01-01T00:00:00Z",
	})

	// Explicit candidate list — should pick next after "default".
	cmd := newRotateCmd()
	cmd.SetArgs([]string{name, "default", "staging"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rc, err := config.ReadRuntimeConfig(sessionDir)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if rc.Profile != "staging" {
		t.Errorf("Profile = %q, want %q", rc.Profile, "staging")
	}
}

func TestRotate_GlobPattern(t *testing.T) {
	setupRotateTestH2Dir(t)
	name := "rotate-test-glob"
	tmpDir := t.TempDir()

	// Create profile dirs under the harness config prefix.
	os.MkdirAll(filepath.Join(tmpDir, "staging-1"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "staging-2"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "staging-3"), 0o755)

	sessionDir := writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               "sid-1",
		HarnessSessionID:        "sid-1",
		HarnessType:             "claude_code",
		HarnessConfigPathPrefix: tmpDir,
		Profile:                 "staging-1",
		Command:                 "claude",
		CWD:                     tmpDir,
		StartedAt:               "2024-01-01T00:00:00Z",
	})

	// Glob "staging-*" with current=staging-1 should pick staging-2.
	cmd := newRotateCmd()
	cmd.SetArgs([]string{name, "staging-*"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rc, err := config.ReadRuntimeConfig(sessionDir)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if rc.Profile != "staging-2" {
		t.Errorf("Profile = %q, want %q", rc.Profile, "staging-2")
	}
}

func TestFilterRateLimited(t *testing.T) {
	configPrefix := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		os.MkdirAll(filepath.Join(configPrefix, name), 0o755)
	}

	// Rate-limit profile "b".
	rl := &config.RateLimitInfo{
		ResetsAt:   time.Now().Add(1 * time.Hour),
		RecordedAt: time.Now(),
	}
	if err := config.WriteRateLimit(filepath.Join(configPrefix, "b"), rl); err != nil {
		t.Fatal(err)
	}

	filtered, skipped := filterRateLimited([]string{"a", "b", "c"}, configPrefix)
	if !reflect.DeepEqual(filtered, []string{"a", "c"}) {
		t.Errorf("filtered = %v, want [a c]", filtered)
	}
	if len(skipped) != 1 || skipped[0].name != "b" {
		t.Errorf("skipped = %+v, want [{name:b}]", skipped)
	}
}

func TestFilterRateLimited_ExpiredNotFiltered(t *testing.T) {
	configPrefix := t.TempDir()
	os.MkdirAll(filepath.Join(configPrefix, "a"), 0o755)

	// Write an expired rate limit.
	rl := &config.RateLimitInfo{
		ResetsAt:   time.Now().Add(-1 * time.Hour),
		RecordedAt: time.Now().Add(-2 * time.Hour),
	}
	if err := config.WriteRateLimit(filepath.Join(configPrefix, "a"), rl); err != nil {
		t.Fatal(err)
	}

	filtered, skipped := filterRateLimited([]string{"a"}, configPrefix)
	if !reflect.DeepEqual(filtered, []string{"a"}) {
		t.Errorf("filtered = %v, want [a]", filtered)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %+v, want empty", skipped)
	}
}

func TestFilterRateLimited_AllLimited(t *testing.T) {
	configPrefix := t.TempDir()
	for _, name := range []string{"a", "b"} {
		os.MkdirAll(filepath.Join(configPrefix, name), 0o755)
		rl := &config.RateLimitInfo{
			ResetsAt:   time.Now().Add(1 * time.Hour),
			RecordedAt: time.Now(),
		}
		if err := config.WriteRateLimit(filepath.Join(configPrefix, name), rl); err != nil {
			t.Fatal(err)
		}
	}

	filtered, skipped := filterRateLimited([]string{"a", "b"}, configPrefix)
	if len(filtered) != 0 {
		t.Errorf("filtered = %v, want empty", filtered)
	}
	if len(skipped) != 2 {
		t.Errorf("skipped = %+v, want 2 entries", skipped)
	}
}

func TestRotate_SkipsRateLimitedProfile(t *testing.T) {
	setupRotateTestH2Dir(t)
	name := "rotate-test-ratelimit"
	tmpDir := t.TempDir()

	os.MkdirAll(filepath.Join(tmpDir, "default"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "staging"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "prod"), 0o755)

	// Rate-limit "staging".
	rl := &config.RateLimitInfo{
		ResetsAt:   time.Now().Add(1 * time.Hour),
		RecordedAt: time.Now(),
	}
	if err := config.WriteRateLimit(filepath.Join(tmpDir, "staging"), rl); err != nil {
		t.Fatal(err)
	}

	sessionDir := writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               "sid-1",
		HarnessSessionID:        "sid-1",
		HarnessType:             "claude_code",
		HarnessConfigPathPrefix: tmpDir,
		Profile:                 "default",
		Command:                 "claude",
		CWD:                     tmpDir,
		StartedAt:               "2024-01-01T00:00:00Z",
	})

	// Auto-select should skip "staging" and pick "prod" (sorted: default, prod, staging).
	cmd := newRotateCmd()
	cmd.SetArgs([]string{name})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rc, err := config.ReadRuntimeConfig(sessionDir)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if rc.Profile != "prod" {
		t.Errorf("Profile = %q, want %q (staging should be skipped)", rc.Profile, "prod")
	}
}

func TestRotate_AllCandidatesRateLimited(t *testing.T) {
	setupRotateTestH2Dir(t)
	name := "rotate-test-all-limited"
	tmpDir := t.TempDir()

	os.MkdirAll(filepath.Join(tmpDir, "default"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "staging"), 0o755)

	// Rate-limit both non-current profiles.
	for _, p := range []string{"staging"} {
		rl := &config.RateLimitInfo{
			ResetsAt:   time.Now().Add(1 * time.Hour),
			RecordedAt: time.Now(),
		}
		if err := config.WriteRateLimit(filepath.Join(tmpDir, p), rl); err != nil {
			t.Fatal(err)
		}
	}

	writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               "sid-1",
		HarnessSessionID:        "sid-1",
		HarnessType:             "claude_code",
		HarnessConfigPathPrefix: tmpDir,
		Profile:                 "default",
		Command:                 "claude",
		CWD:                     tmpDir,
		StartedAt:               "2024-01-01T00:00:00Z",
	})

	// Only candidate besides current is "staging" which is limited.
	// After filtering, only "default" remains, but that's the current → error.
	cmd := newRotateCmd()
	cmd.SetArgs([]string{name})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when all non-current candidates are rate limited")
	}
	if !strings.Contains(err.Error(), "already using profile") {
		t.Errorf("error = %q, want containing 'already using profile'", err.Error())
	}
}

func TestRotate_VariadicCandidates(t *testing.T) {
	setupRotateTestH2Dir(t)
	name := "rotate-test-variadic"
	tmpDir := t.TempDir()

	os.MkdirAll(filepath.Join(tmpDir, "alpha"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "beta"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "gamma"), 0o755)

	sessionDir := writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               "sid-1",
		HarnessSessionID:        "sid-1",
		HarnessType:             "claude_code",
		HarnessConfigPathPrefix: tmpDir,
		Profile:                 "beta",
		Command:                 "claude",
		CWD:                     tmpDir,
		StartedAt:               "2024-01-01T00:00:00Z",
	})

	// Candidates in order: gamma, alpha, beta. Current is beta (index 2), next wraps to gamma.
	cmd := newRotateCmd()
	cmd.SetArgs([]string{name, "gamma", "alpha", "beta"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rc, err := config.ReadRuntimeConfig(sessionDir)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if rc.Profile != "gamma" {
		t.Errorf("Profile = %q, want %q", rc.Profile, "gamma")
	}
}
