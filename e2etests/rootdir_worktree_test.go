package e2etests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// §4.1 Default working_dir (CWD)
func TestWorkingDir_Default(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	createRole(t, h2Dir, "default-cwd", `
name: default-cwd
agent_type: "true"
instructions: test default working_dir
`)

	result := runH2(t, h2Dir, "run", "--role", "default-cwd", "--name", "test-default-cwd", "--detach")
	if result.ExitCode != 0 {
		t.Fatalf("h2 run failed: exit=%d stderr=%s stdout=%s", result.ExitCode, result.Stderr, result.Stdout)
	}
	t.Cleanup(func() { stopAgent(t, h2Dir, "test-default-cwd") })

	// Check session metadata for CWD — should be the test runner's CWD.
	meta := readSessionMetadata(t, h2Dir, "test-default-cwd")
	cwd, _ := os.Getwd()
	if meta.CWD != cwd {
		t.Errorf("session CWD = %q, want %q", meta.CWD, cwd)
	}
}

// §4.2 Absolute working_dir
func TestWorkingDir_Absolute(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	absDir := t.TempDir()

	createRole(t, h2Dir, "abs-dir", `
name: abs-dir
agent_type: "true"
instructions: test absolute working_dir
working_dir: "`+absDir+`"
`)

	result := runH2(t, h2Dir, "run", "--role", "abs-dir", "--name", "test-abs-dir", "--detach")
	if result.ExitCode != 0 {
		t.Fatalf("h2 run failed: exit=%d stderr=%s stdout=%s", result.ExitCode, result.Stderr, result.Stdout)
	}
	t.Cleanup(func() { stopAgent(t, h2Dir, "test-abs-dir") })

	meta := readSessionMetadata(t, h2Dir, "test-abs-dir")
	// Resolve symlinks for macOS /private/var comparison.
	wantDir, _ := filepath.EvalSymlinks(absDir)
	gotDir, _ := filepath.EvalSymlinks(meta.CWD)
	if gotDir != wantDir {
		t.Errorf("session CWD = %q, want %q", meta.CWD, absDir)
	}
}

// §4.3 Relative working_dir (resolved against h2 dir)
func TestWorkingDir_Relative(t *testing.T) {
	h2Dir := createTestH2Dir(t)

	// Create the target directory under h2Dir.
	projDir := filepath.Join(h2Dir, "projects", "myapp")
	os.MkdirAll(projDir, 0o755)

	createRole(t, h2Dir, "rel-dir", `
name: rel-dir
agent_type: "true"
instructions: test relative working_dir
working_dir: projects/myapp
`)

	result := runH2(t, h2Dir, "run", "--role", "rel-dir", "--name", "test-rel-dir", "--detach")
	if result.ExitCode != 0 {
		t.Fatalf("h2 run failed: exit=%d stderr=%s stdout=%s", result.ExitCode, result.Stderr, result.Stdout)
	}
	t.Cleanup(func() { stopAgent(t, h2Dir, "test-rel-dir") })

	meta := readSessionMetadata(t, h2Dir, "test-rel-dir")
	wantDir, _ := filepath.EvalSymlinks(projDir)
	gotDir, _ := filepath.EvalSymlinks(meta.CWD)
	if gotDir != wantDir {
		t.Errorf("session CWD = %q, want %q", meta.CWD, projDir)
	}
}

// §5.1-5.2 Launch agent with worktree (branch created, CWD in worktree)
func TestWorktree_NewBranch(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	_ = createGitRepo(t, h2Dir, "projects/myrepo")

	createRole(t, h2Dir, "wt-agent", `
name: wt-agent
agent_type: "true"
instructions: test worktree agent
working_dir: projects/myrepo
worktree:
  enabled: true
  branch_from: main
`)

	result := runH2(t, h2Dir, "run", "--role", "wt-agent", "--name", "wt-test", "--detach")
	if result.ExitCode != 0 {
		t.Fatalf("h2 run failed: exit=%d stderr=%s stdout=%s", result.ExitCode, result.Stderr, result.Stdout)
	}
	t.Cleanup(func() { stopAgent(t, h2Dir, "wt-test") })

	worktreePath := filepath.Join(h2Dir, "worktrees", "wt-test")

	// Verify worktree directory exists.
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("worktree dir does not exist: %v", err)
	}

	// Verify .git file (not directory) exists.
	gitPath := filepath.Join(worktreePath, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		t.Fatalf("worktree .git not found: %v", err)
	}
	if info.IsDir() {
		t.Error("expected .git to be a file (worktree), not a directory")
	}

	// Verify branch name.
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git branch --show-current: %v", err)
	}
	branch := strings.TrimSpace(string(out))
	if branch != "wt-test" {
		t.Errorf("branch = %q, want %q", branch, "wt-test")
	}

	// Verify agent CWD is the worktree.
	meta := readSessionMetadata(t, h2Dir, "wt-test")
	wantCWD, _ := filepath.EvalSymlinks(worktreePath)
	gotCWD, _ := filepath.EvalSymlinks(meta.CWD)
	if gotCWD != wantCWD {
		t.Errorf("session CWD = %q, want worktree %q", meta.CWD, worktreePath)
	}
}

// §5.3 Worktree with detached head
func TestWorktree_DetachedHead(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	_ = createGitRepo(t, h2Dir, "projects/myrepo")

	createRole(t, h2Dir, "wt-detached", `
name: wt-detached
agent_type: "true"
instructions: test detached worktree
working_dir: projects/myrepo
worktree:
  enabled: true
  branch_from: main
  use_detached_head: true
`)

	result := runH2(t, h2Dir, "run", "--role", "wt-detached", "--name", "wt-detach", "--detach")
	if result.ExitCode != 0 {
		t.Fatalf("h2 run failed: exit=%d stderr=%s stdout=%s", result.ExitCode, result.Stderr, result.Stdout)
	}
	t.Cleanup(func() { stopAgent(t, h2Dir, "wt-detach") })

	worktreePath := filepath.Join(h2Dir, "worktrees", "wt-detach")

	// Verify worktree exists.
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		t.Fatalf("worktree .git not found: %v", err)
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

// §5.4 Worktree error: non-git working_dir
func TestWorktree_NonGitWorkingDir(t *testing.T) {
	h2Dir := createTestH2Dir(t)

	// Create a non-git directory for working_dir.
	notGitDir := filepath.Join(h2Dir, "projects", "not-a-repo")
	os.MkdirAll(notGitDir, 0o755)

	createRole(t, h2Dir, "wt-nogit", `
name: wt-nogit
agent_type: "true"
instructions: test
working_dir: projects/not-a-repo
worktree:
  enabled: true
`)

	result := runH2(t, h2Dir, "run", "--role", "wt-nogit", "--name", "wt-fail", "--detach")
	if result.ExitCode == 0 {
		t.Cleanup(func() { stopAgent(t, h2Dir, "wt-fail") })
		t.Fatal("expected error for non-git working_dir with worktree enabled")
	}

	combined := result.Stdout + result.Stderr
	if !strings.Contains(combined, "not a git repository") {
		t.Errorf("error = %q, want it to contain 'not a git repository'", combined)
	}
}

// §5.5 Worktree error: corrupt worktree dir
func TestWorktree_CorruptWorktreeDir(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	createGitRepo(t, h2Dir, "projects/myrepo")

	createRole(t, h2Dir, "wt-corrupt", `
name: wt-corrupt
agent_type: "true"
instructions: test
working_dir: projects/myrepo
worktree:
  enabled: true
  branch_from: main
`)

	// Pre-create a corrupt worktree dir (has files but no .git).
	corruptDir := filepath.Join(h2Dir, "worktrees", "wt-corrupt-test")
	os.MkdirAll(corruptDir, 0o755)
	os.WriteFile(filepath.Join(corruptDir, "stale-file.txt"), []byte("stale"), 0o644)

	result := runH2(t, h2Dir, "run", "--role", "wt-corrupt", "--name", "wt-corrupt-test", "--detach")
	if result.ExitCode == 0 {
		t.Cleanup(func() { stopAgent(t, h2Dir, "wt-corrupt-test") })
		t.Fatal("expected error for corrupt worktree dir")
	}

	combined := result.Stdout + result.Stderr
	if !strings.Contains(combined, "no .git file") {
		t.Errorf("error = %q, want it to contain 'no .git file'", combined)
	}
}

// §5.6 Worktree re-run reuses existing worktree
func TestWorktree_ReuseExisting(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	createGitRepo(t, h2Dir, "projects/myrepo")

	createRole(t, h2Dir, "wt-reuse", `
name: wt-reuse
agent_type: "true"
instructions: test worktree reuse
working_dir: projects/myrepo
worktree:
  enabled: true
  branch_from: main
`)

	// First run — creates the worktree.
	result := runH2(t, h2Dir, "run", "--role", "wt-reuse", "--name", "wt-reuse-test", "--detach")
	if result.ExitCode != 0 {
		t.Fatalf("first h2 run failed: exit=%d stderr=%s stdout=%s", result.ExitCode, result.Stderr, result.Stdout)
	}

	worktreePath := filepath.Join(h2Dir, "worktrees", "wt-reuse-test")

	// Write a marker file in the worktree.
	os.WriteFile(filepath.Join(worktreePath, "reuse-marker.txt"), []byte("I was here"), 0o644)

	// Stop the agent.
	stopAgent(t, h2Dir, "wt-reuse-test")

	// Second run — should reuse the existing worktree.
	result = runH2(t, h2Dir, "run", "--role", "wt-reuse", "--name", "wt-reuse-test", "--detach")
	if result.ExitCode != 0 {
		t.Fatalf("second h2 run failed: exit=%d stderr=%s stdout=%s", result.ExitCode, result.Stderr, result.Stdout)
	}
	t.Cleanup(func() { stopAgent(t, h2Dir, "wt-reuse-test") })

	// Verify marker file still exists (worktree was reused, not recreated).
	if _, err := os.Stat(filepath.Join(worktreePath, "reuse-marker.txt")); err != nil {
		t.Error("reuse-marker.txt not found — worktree was not reused")
	}
}

// --- helpers ---

// createGitRepo creates a git repo at the given relative path under h2Dir.
// Returns the absolute path. The repo has one commit on "main".
func createGitRepo(t *testing.T, h2Dir, relPath string) string {
	t.Helper()
	repoDir := filepath.Join(h2Dir, relPath)
	os.MkdirAll(repoDir, 0o755)

	gitRun(t, repoDir, "init")
	gitRun(t, repoDir, "config", "user.email", "test@test.com")
	gitRun(t, repoDir, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hello"), 0o644)
	gitRun(t, repoDir, "add", ".")
	gitRun(t, repoDir, "commit", "-m", "initial")

	// Ensure we're on "main" branch.
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = repoDir
	out, _ := cmd.Output()
	currentBranch := strings.TrimSpace(string(out))
	if currentBranch != "main" {
		gitRun(t, repoDir, "branch", "-m", currentBranch, "main")
	}

	return repoDir
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %s: %v", strings.Join(args, " "), out, err)
	}
}

// sessionMetadata is a minimal struct for reading session metadata JSON.
type sessionMetadata struct {
	CWD       string            `json:"cwd"`
	Overrides map[string]string `json:"overrides,omitempty"`
}

// readSessionMetadata reads and parses session.metadata.json for the given agent.
func readSessionMetadata(t *testing.T, h2Dir, agentName string) sessionMetadata {
	t.Helper()
	path := filepath.Join(h2Dir, "sessions", agentName, "session.metadata.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read session metadata for %s: %v", agentName, err)
	}
	var meta sessionMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("parse session metadata for %s: %v", agentName, err)
	}
	return meta
}
