package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2/internal/config"
)

func setupProfileTestH2Dir(t *testing.T) string {
	t.Helper()

	config.ResetResolveCache()
	t.Cleanup(config.ResetResolveCache)

	h2Dir := filepath.Join(t.TempDir(), "myh2")
	for _, sub := range []string{
		"account-profiles-shared",
		"claude-config",
		"codex-config",
		"roles",
		"sessions",
		"sockets",
	} {
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

func TestProfileCreate_SymlinkShared(t *testing.T) {
	h2Dir := setupProfileTestH2Dir(t)

	srcProfile := "base"
	if err := os.MkdirAll(filepath.Join(h2Dir, "account-profiles-shared", srcProfile, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h2Dir, "account-profiles-shared", srcProfile, "CLAUDE_AND_AGENTS.md"), []byte("shared"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h2Dir, "account-profiles-shared", srcProfile, "skills", "SKILL.md"), []byte("skill"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(h2Dir, "claude-config", srcProfile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h2Dir, "claude-config", srcProfile, "settings.json"), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h2Dir, "claude-config", srcProfile, ".claude.json"), []byte(`{"auth":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(h2Dir, "codex-config", srcProfile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h2Dir, "codex-config", srcProfile, "config.toml"), []byte("ok = true"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h2Dir, "codex-config", srcProfile, "requirements.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h2Dir, "codex-config", srcProfile, "auth.json"), []byte(`{"auth":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newProfileCreateCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"new", "--symlink-shared", srcProfile})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("profile create failed: %v", err)
	}

	sharedLink := filepath.Join(h2Dir, "account-profiles-shared", "new")
	info, err := os.Lstat(sharedLink)
	if err != nil {
		t.Fatalf("lstat shared link: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected %s to be a symlink", sharedLink)
	}
	target, err := os.Readlink(sharedLink)
	if err != nil {
		t.Fatalf("readlink shared link: %v", err)
	}
	if target != srcProfile {
		t.Fatalf("shared symlink target = %q, want %q", target, srcProfile)
	}

	if _, err := os.Stat(filepath.Join(h2Dir, "claude-config", "new", ".claude.json")); !os.IsNotExist(err) {
		t.Fatalf("expected claude auth file to be excluded, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(h2Dir, "codex-config", "new", "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("expected codex auth file to be excluded, got err=%v", err)
	}

	claudeTarget, err := os.Readlink(filepath.Join(h2Dir, "claude-config", "new", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("readlink claude shared link: %v", err)
	}
	if want := filepath.Join("..", "..", "account-profiles-shared", "new", "CLAUDE_AND_AGENTS.md"); claudeTarget != want {
		t.Fatalf("claude CLAUDE.md target = %q, want %q", claudeTarget, want)
	}

	codexTarget, err := os.Readlink(filepath.Join(h2Dir, "codex-config", "new", "AGENTS.md"))
	if err != nil {
		t.Fatalf("readlink codex shared link: %v", err)
	}
	if want := filepath.Join("..", "..", "account-profiles-shared", "new", "CLAUDE_AND_AGENTS.md"); codexTarget != want {
		t.Fatalf("codex AGENTS.md target = %q, want %q", codexTarget, want)
	}
}

func TestProfileCreate_CopyAndSymlinkSharedMutuallyExclusive(t *testing.T) {
	setupProfileTestH2Dir(t)

	cmd := newProfileCreateCmd()
	cmd.SetArgs([]string{"new", "--copy", "base", "--symlink-shared", "base"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for mutually exclusive flags")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("unexpected error: %v", err)
	}
}

