package e2etests

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// §6.1 Override a simple string field (working_dir)
func TestOverride_SimpleStringField(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	overrideDir := t.TempDir()

	createRole(t, h2Dir, "override-str", `
name: override-str
agent_harness:
  harness_type: generic
  command: "true"
instructions: test override string field
working_dir: /original/path
`)

	result := runH2(t, h2Dir, "run", "--role", "override-str",
		"--name", "test-override-str", "--detach",
		"--override", "working_dir="+overrideDir)
	if result.ExitCode != 0 {
		t.Fatalf("h2 run failed: exit=%d stderr=%s stdout=%s", result.ExitCode, result.Stderr, result.Stdout)
	}
	t.Cleanup(func() { stopAgent(t, h2Dir, "test-override-str") })

	meta := readSessionMetadata(t, h2Dir, "test-override-str")
	wantDir, _ := filepath.EvalSymlinks(overrideDir)
	gotDir, _ := filepath.EvalSymlinks(meta.CWD)
	if gotDir != wantDir {
		t.Errorf("session CWD = %q, want overridden %q", meta.CWD, overrideDir)
	}
}

// §6.2 Override a nested string field (worktree.project_dir)
func TestOverride_NestedStringField_Worktree(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	createGitRepo(t, h2Dir, "projects/myrepo")

	// Role has worktree with project_dir — override branch_from.
	createRole(t, h2Dir, "override-wt", `
name: override-wt
agent_harness:
  harness_type: generic
  command: "true"
instructions: test override nested string
worktree:
  project_dir: projects/myrepo
  name: test-override-wt
  branch_from: main
`)

	result := runH2(t, h2Dir, "run", "--role", "override-wt",
		"--name", "test-override-wt", "--detach",
		"--override", "worktree.use_detached_head=true")
	if result.ExitCode != 0 {
		t.Fatalf("h2 run failed: exit=%d stderr=%s stdout=%s", result.ExitCode, result.Stderr, result.Stdout)
	}
	t.Cleanup(func() { stopAgent(t, h2Dir, "test-override-wt") })

	// Verify worktree was created with detached head (override took effect).
	worktreePath := filepath.Join(h2Dir, "worktrees", "test-override-wt")
	meta := readSessionMetadata(t, h2Dir, "test-override-wt")
	wantCWD, _ := filepath.EvalSymlinks(worktreePath)
	gotCWD, _ := filepath.EvalSymlinks(meta.CWD)
	if gotCWD != wantCWD {
		t.Errorf("session CWD = %q, want worktree %q", meta.CWD, worktreePath)
	}

	// Verify HEAD is detached.
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git branch --show-current: %v", err)
	}
	branch := strings.TrimSpace(string(out))
	if branch != "" {
		t.Errorf("expected detached HEAD (empty branch), got %q", branch)
	}
}

// §6.3 Override with invalid key
func TestOverride_InvalidKey(t *testing.T) {
	h2Dir := createTestH2Dir(t)

	createRole(t, h2Dir, "override-bad-key", `
name: override-bad-key
agent_harness:
  harness_type: generic
  command: "true"
instructions: test invalid override key
`)

	result := runH2(t, h2Dir, "run", "--role", "override-bad-key",
		"--name", "test-bad-key", "--detach",
		"--override", "nonexistent_field=value")
	if result.ExitCode == 0 {
		t.Cleanup(func() { stopAgent(t, h2Dir, "test-bad-key") })
		t.Fatal("expected h2 run to fail for invalid override key")
	}

	combined := result.Stdout + result.Stderr
	if !strings.Contains(combined, "unknown") {
		t.Errorf("error = %q, want it to contain 'unknown'", combined)
	}
}

// §6.4 Override with type mismatch
func TestOverride_TypeMismatch(t *testing.T) {
	h2Dir := createTestH2Dir(t)

	createRole(t, h2Dir, "override-type", `
name: override-type
agent_harness:
  harness_type: generic
  command: "true"
instructions: test type mismatch
`)

	result := runH2(t, h2Dir, "run", "--role", "override-type",
		"--name", "test-type-err", "--detach",
		"--override", "worktree.use_detached_head=notabool")
	if result.ExitCode == 0 {
		t.Cleanup(func() { stopAgent(t, h2Dir, "test-type-err") })
		t.Fatal("expected h2 run to fail for type mismatch")
	}

	combined := result.Stdout + result.Stderr
	if !strings.Contains(combined, "bool") {
		t.Errorf("error = %q, want it to mention 'bool'", combined)
	}
}

// §6.5 Overrides recorded in session metadata
func TestOverride_RecordedInMetadata(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	overrideDir := t.TempDir()

	createRole(t, h2Dir, "override-meta", `
name: override-meta
agent_harness:
  harness_type: generic
  command: "true"
instructions: test metadata recording
`)

	result := runH2(t, h2Dir, "run", "--role", "override-meta",
		"--name", "test-override-meta", "--detach",
		"--override", "working_dir="+overrideDir,
		"--override", "model=opus")
	if result.ExitCode != 0 {
		t.Fatalf("h2 run failed: exit=%d stderr=%s stdout=%s", result.ExitCode, result.Stderr, result.Stdout)
	}
	t.Cleanup(func() { stopAgent(t, h2Dir, "test-override-meta") })

	meta := readSessionMetadata(t, h2Dir, "test-override-meta")
	if meta.Overrides == nil {
		t.Fatal("Overrides should not be nil in session metadata")
	}
	if meta.Overrides["working_dir"] != overrideDir {
		t.Errorf("Overrides[working_dir] = %q, want %q", meta.Overrides["working_dir"], overrideDir)
	}
	if meta.Overrides["model"] != "opus" {
		t.Errorf("Overrides[model] = %q, want %q", meta.Overrides["model"], "opus")
	}
}
