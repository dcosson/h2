package cmd

import (
	"os"
	"path/filepath"
	"reflect"
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

func TestRotate_StoppedAgent_NoResume(t *testing.T) {
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
	// Zero args should fail (need at least agent name).
	cmd := newRotateCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for zero args")
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
	// Create a temp h2Dir with some profile directories.
	h2Dir := t.TempDir()
	for _, name := range []string{"default", "staging-1", "staging-2", "staging-3", "prod"} {
		os.MkdirAll(filepath.Join(h2Dir, "profiles-shared", name), 0o755)
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
			got, err := resolveRotateCandidates(tt.args, h2Dir)
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
	name := "rotate-test-glob"
	tmpDir := t.TempDir()

	// Create profile dirs.
	os.MkdirAll(filepath.Join(tmpDir, "staging-1"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "staging-2"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "staging-3"), 0o755)

	// Create profiles-shared dirs so discoverProfiles finds them, and clean up after.
	h2Dir := config.ConfigDir()
	for _, p := range []string{"staging-1", "staging-2", "staging-3"} {
		dir := filepath.Join(h2Dir, "profiles-shared", p)
		os.MkdirAll(dir, 0o755)
		defer os.Remove(dir)
	}

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

func TestRotate_VariadicCandidates(t *testing.T) {
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
