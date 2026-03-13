package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2/internal/config"
	"h2/internal/session"
)

func TestRotate_NoSession(t *testing.T) {
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

func TestRotate_GenericHarness_NoLogMove(t *testing.T) {
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

func TestRotate_LiveFlag_StopsRotatesResumes(t *testing.T) {
	name := "rotate-test-live"
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

	// Mock forkDaemonFunc to capture calls.
	var capturedSessionDir string
	var capturedResume bool
	origFork := forkDaemonFunc
	forkDaemonFunc = func(sd string, hints session.TerminalHints, resume bool) error {
		capturedSessionDir = sd
		capturedResume = resume
		return nil
	}
	defer func() { forkDaemonFunc = origFork }()

	// Agent is not running (no socket), so --live should just rotate and resume.
	cmd := newRotateCmd()
	cmd.SetArgs([]string{name, "staging", "--live"})
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
	if capturedSessionDir != sessionDir {
		t.Errorf("fork sessionDir = %q, want %q", capturedSessionDir, sessionDir)
	}
	if !capturedResume {
		t.Error("fork should be called with resume=true")
	}
}

func TestRotate_EmptyProfileDefaultsToDefault(t *testing.T) {
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

func TestRotate_NoExistingLog_SucceedsWithoutMove(t *testing.T) {
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
	cmd := newRotateCmd()
	cmd.SetArgs([]string{"only-one-arg"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for wrong arg count")
	}

	cmd2 := newRotateCmd()
	cmd2.SetArgs([]string{"one", "two", "three"})
	err = cmd2.Execute()
	if err == nil {
		t.Fatal("expected error for too many args")
	}
}

func TestRotate_NoHarnessConfigPrefix(t *testing.T) {
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
