package e2etests

import (
	"path/filepath"
	"strings"
	"testing"
)

// §6.1 Override a simple string field (root_dir)
func TestOverride_SimpleStringField(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	overrideDir := t.TempDir()

	createRole(t, h2Dir, "override-str", `
name: override-str
agent_type: "true"
instructions: test override string field
root_dir: /original/path
`)

	result := runH2(t, h2Dir, "run", "--role", "override-str",
		"--name", "test-override-str", "--detach",
		"--override", "root_dir="+overrideDir)
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

// §6.2 Override a nested bool field (worktree.enabled)
func TestOverride_NestedBoolField(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	createGitRepo(t, h2Dir, "projects/myrepo")

	// Role does NOT have worktree enabled — override will enable it.
	createRole(t, h2Dir, "override-wt", `
name: override-wt
agent_type: "true"
instructions: test override nested bool
root_dir: projects/myrepo
`)

	result := runH2(t, h2Dir, "run", "--role", "override-wt",
		"--name", "test-override-wt", "--detach",
		"--override", "worktree.enabled=true",
		"--override", "worktree.branch_from=main")
	if result.ExitCode != 0 {
		t.Fatalf("h2 run failed: exit=%d stderr=%s stdout=%s", result.ExitCode, result.Stderr, result.Stdout)
	}
	t.Cleanup(func() { stopAgent(t, h2Dir, "test-override-wt") })

	// Verify worktree was created (override took effect).
	worktreePath := filepath.Join(h2Dir, "worktrees", "test-override-wt")
	meta := readSessionMetadata(t, h2Dir, "test-override-wt")
	wantCWD, _ := filepath.EvalSymlinks(worktreePath)
	gotCWD, _ := filepath.EvalSymlinks(meta.CWD)
	if gotCWD != wantCWD {
		t.Errorf("session CWD = %q, want worktree %q", meta.CWD, worktreePath)
	}
}

// §6.3 Override with invalid key
func TestOverride_InvalidKey(t *testing.T) {
	h2Dir := createTestH2Dir(t)

	createRole(t, h2Dir, "override-bad-key", `
name: override-bad-key
agent_type: "true"
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
agent_type: "true"
instructions: test type mismatch
`)

	result := runH2(t, h2Dir, "run", "--role", "override-type",
		"--name", "test-type-err", "--detach",
		"--override", "worktree.enabled=notabool")
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
agent_type: "true"
instructions: test metadata recording
`)

	result := runH2(t, h2Dir, "run", "--role", "override-meta",
		"--name", "test-override-meta", "--detach",
		"--override", "root_dir="+overrideDir,
		"--override", "model=opus")
	if result.ExitCode != 0 {
		t.Fatalf("h2 run failed: exit=%d stderr=%s stdout=%s", result.ExitCode, result.Stderr, result.Stdout)
	}
	t.Cleanup(func() { stopAgent(t, h2Dir, "test-override-meta") })

	meta := readSessionMetadata(t, h2Dir, "test-override-meta")
	if meta.Overrides == nil {
		t.Fatal("Overrides should not be nil in session metadata")
	}
	if meta.Overrides["root_dir"] != overrideDir {
		t.Errorf("Overrides[root_dir] = %q, want %q", meta.Overrides["root_dir"], overrideDir)
	}
	if meta.Overrides["model"] != "opus" {
		t.Errorf("Overrides[model] = %q, want %q", meta.Overrides["model"], "opus")
	}
}
