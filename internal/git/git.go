package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"h2/internal/config"
)

// CreateWorktree creates a git worktree for an agent.
// Returns the absolute path to the new worktree.
//
// If the worktree already exists with a valid .git file, it is reused.
// repoDir must be a git repository (or worktree).
func CreateWorktree(agentName, repoDir string, cfg *config.WorktreeConfig) (string, error) {
	// Verify repoDir is a git repo.
	if !isGitRepo(repoDir) {
		return "", fmt.Errorf("working_dir %q is not a git repository", repoDir)
	}

	worktreePath := filepath.Join(config.WorktreesDir(), agentName)

	// Check if worktree already exists.
	gitFile := filepath.Join(worktreePath, ".git")
	if info, err := os.Stat(gitFile); err == nil {
		if info.IsDir() {
			return "", fmt.Errorf("worktree path %q contains a .git directory (expected a file); remove it to proceed", worktreePath)
		}
		// Valid .git file — reuse the existing worktree.
		// Verify it's actually a valid worktree by checking the gitdir reference.
		data, err := os.ReadFile(gitFile)
		if err != nil {
			return "", fmt.Errorf("read worktree .git file: %w", err)
		}
		content := strings.TrimSpace(string(data))
		if !strings.HasPrefix(content, "gitdir:") {
			return "", fmt.Errorf("worktree path %q has corrupt .git file (missing gitdir reference)", worktreePath)
		}
		return worktreePath, nil
	}

	// Check if the worktree dir exists but has no .git file (corrupt/partial).
	if info, err := os.Stat(worktreePath); err == nil && info.IsDir() {
		entries, _ := os.ReadDir(worktreePath)
		if len(entries) > 0 {
			return "", fmt.Errorf("worktree path %q exists but has no .git file; remove it to proceed", worktreePath)
		}
		// Empty directory is fine — git worktree add will use it.
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return "", fmt.Errorf("create worktrees dir: %w", err)
	}

	branchFrom := cfg.GetBranchFrom()

	var args []string
	if cfg.UseDetachedHead {
		args = []string{"worktree", "add", "--detach", worktreePath, branchFrom}
	} else {
		args = []string{"worktree", "add", "-b", agentName, worktreePath, branchFrom}
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git worktree add: %s: %w", strings.TrimSpace(string(output)), err)
	}

	return worktreePath, nil
}

// isGitRepo returns true if the directory is a git repository or worktree.
func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = dir
	return cmd.Run() == nil
}
