package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"h2/internal/config"
)

// initGitRepo creates a minimal git repo with one commit in dir.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@test.com")
	run(t, dir, "git", "config", "user.name", "Test")
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "initial")
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %s: %v", name, strings.Join(args, " "), out, err)
	}
}

// setupWorktreeTest creates a git repo and configures H2_DIR for worktree paths.
func setupWorktreeTest(t *testing.T) (repoDir string) {
	t.Helper()

	config.ResetResolveCache()
	t.Cleanup(config.ResetResolveCache)

	repoDir = filepath.Join(t.TempDir(), "repo")
	os.MkdirAll(repoDir, 0o755)
	initGitRepo(t, repoDir)

	// Set up a valid h2 dir for WorktreesDir().
	h2Dir := filepath.Join(t.TempDir(), ".h2")
	os.MkdirAll(h2Dir, 0o755)
	config.WriteMarker(h2Dir)
	t.Setenv("H2_DIR", h2Dir)

	return repoDir
}

func TestCreateWorktree_NewBranch(t *testing.T) {
	repoDir := setupWorktreeTest(t)

	cfg := &config.WorktreeConfig{
		Enabled:    true,
		BranchFrom: "main",
	}

	path, err := CreateWorktree("test-agent", repoDir, cfg)
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	// Verify worktree was created.
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		t.Errorf("expected .git file in worktree, got error: %v", err)
	}

	// Verify we're on the expected branch.
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git branch --show-current: %v", err)
	}
	branch := strings.TrimSpace(string(out))
	if branch != "test-agent" {
		t.Errorf("branch = %q, want %q", branch, "test-agent")
	}
}

func TestCreateWorktree_DetachedHead(t *testing.T) {
	repoDir := setupWorktreeTest(t)

	cfg := &config.WorktreeConfig{
		Enabled:         true,
		BranchFrom:      "main",
		UseDetachedHead: true,
	}

	path, err := CreateWorktree("detached-agent", repoDir, cfg)
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	// Verify worktree was created.
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		t.Errorf("expected .git file in worktree, got error: %v", err)
	}

	// Verify HEAD is detached.
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git branch --show-current: %v", err)
	}
	branch := strings.TrimSpace(string(out))
	if branch != "" {
		t.Errorf("expected detached HEAD (empty branch), got %q", branch)
	}
}

func TestCreateWorktree_ReuseExisting(t *testing.T) {
	repoDir := setupWorktreeTest(t)

	cfg := &config.WorktreeConfig{
		Enabled:    true,
		BranchFrom: "main",
	}

	// Create the worktree first time.
	path1, err := CreateWorktree("reuse-agent", repoDir, cfg)
	if err != nil {
		t.Fatalf("CreateWorktree (first): %v", err)
	}

	// Create a file in the worktree to verify it's reused.
	os.WriteFile(filepath.Join(path1, "marker.txt"), []byte("exists"), 0o644)

	// Call again — should reuse.
	path2, err := CreateWorktree("reuse-agent", repoDir, cfg)
	if err != nil {
		t.Fatalf("CreateWorktree (second): %v", err)
	}

	if path1 != path2 {
		t.Errorf("paths differ: %q vs %q", path1, path2)
	}

	// Verify marker file still exists.
	if _, err := os.Stat(filepath.Join(path2, "marker.txt")); err != nil {
		t.Error("marker.txt not found — worktree was not reused")
	}
}

func TestCreateWorktree_NonGitDir(t *testing.T) {
	config.ResetResolveCache()
	defer config.ResetResolveCache()

	h2Dir := filepath.Join(t.TempDir(), ".h2")
	os.MkdirAll(h2Dir, 0o755)
	config.WriteMarker(h2Dir)
	t.Setenv("H2_DIR", h2Dir)

	notGitDir := t.TempDir()
	cfg := &config.WorktreeConfig{Enabled: true, BranchFrom: "main"}

	_, err := CreateWorktree("agent", notGitDir, cfg)
	if err == nil {
		t.Fatal("expected error for non-git directory")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error = %q, want it to contain 'not a git repository'", err.Error())
	}
}

func TestCreateWorktree_CorruptWorktreeDir(t *testing.T) {
	repoDir := setupWorktreeTest(t)

	cfg := &config.WorktreeConfig{Enabled: true, BranchFrom: "main"}

	// Pre-create the worktree dir with a file but no .git.
	worktreePath := filepath.Join(config.WorktreesDir(), "corrupt-agent")
	os.MkdirAll(worktreePath, 0o755)
	os.WriteFile(filepath.Join(worktreePath, "some-file.txt"), []byte("data"), 0o644)

	_, err := CreateWorktree("corrupt-agent", repoDir, cfg)
	if err == nil {
		t.Fatal("expected error for corrupt worktree dir")
	}
	if !strings.Contains(err.Error(), "no .git file") {
		t.Errorf("error = %q, want it to contain 'no .git file'", err.Error())
	}
}

func TestCreateWorktree_DefaultBranchFrom(t *testing.T) {
	repoDir := setupWorktreeTest(t)

	// Don't set BranchFrom — should default to "main".
	cfg := &config.WorktreeConfig{Enabled: true}

	path, err := CreateWorktree("default-branch-agent", repoDir, cfg)
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		t.Errorf("expected .git file in worktree, got error: %v", err)
	}
}

func TestWorktreeConfig_GetBranchFrom(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.WorktreeConfig
		want string
	}{
		{"default", config.WorktreeConfig{}, "main"},
		{"custom", config.WorktreeConfig{BranchFrom: "develop"}, "develop"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.GetBranchFrom(); got != tt.want {
				t.Errorf("GetBranchFrom() = %q, want %q", got, tt.want)
			}
		})
	}
}
