package cmd

import (
	"bytes"
	"os"
	"path/filepath"
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

func TestProfileCreate_CopyFlagRemoved(t *testing.T) {
	setupProfileTestH2Dir(t)

	cmd := newProfileCreateCmd()
	cmd.SetArgs([]string{"new", "--copy", "base"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown --copy flag")
	}
	if err.Error() != "unknown flag: --copy" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProfileReset_DefaultsPreserveAuthAndCustomSkills(t *testing.T) {
	h2Dir := setupProfileTestH2Dir(t)
	name := "work"

	sharedDir := filepath.Join(h2Dir, "account-profiles-shared", name)
	sharedSkills := filepath.Join(sharedDir, "skills")
	if err := os.MkdirAll(sharedSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedDir, "CLAUDE_AND_AGENTS.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sharedSkills, "shaping"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedSkills, "shaping", "SKILL.md"), []byte("stale managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sharedSkills, "custom-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedSkills, "custom-skill", "SKILL.md"), []byte("custom"), 0o644); err != nil {
		t.Fatal(err)
	}

	claudeDir := filepath.Join(h2Dir, "claude-config", name)
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte("old-settings"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, ".claude.json"), []byte(`{"auth":"keep"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	codexDir := filepath.Join(h2Dir, "codex-config", name)
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte("old-config"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "requirements.toml"), []byte("old-reqs"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(`{"auth":"keep"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newProfileResetCmd()
	cmd.SetArgs([]string{name})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("profile reset failed: %v", err)
	}

	gotInstructions, err := os.ReadFile(filepath.Join(sharedDir, "CLAUDE_AND_AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotInstructions) != config.InstructionsTemplateWithStyle("opinionated") {
		t.Fatalf("instructions were not reset")
	}

	wantManagedSkill, err := config.Templates.ReadFile("templates/styles/opinionated/skills/shaping/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	gotManagedSkill, err := os.ReadFile(filepath.Join(sharedSkills, "shaping", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotManagedSkill) != string(wantManagedSkill) {
		t.Fatalf("managed skill was not updated")
	}

	gotCustomSkill, err := os.ReadFile(filepath.Join(sharedSkills, "custom-skill", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotCustomSkill) != "custom" {
		t.Fatalf("custom skill was modified: %q", string(gotCustomSkill))
	}

	gotClaudeSettings, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotClaudeSettings) != config.ClaudeSettingsTemplate("opinionated") {
		t.Fatalf("claude settings were not reset")
	}
	gotCodexConfig, err := os.ReadFile(filepath.Join(codexDir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotCodexConfig) != config.CodexConfigTemplate("opinionated") {
		t.Fatalf("codex config was not reset")
	}
	gotCodexReqs, err := os.ReadFile(filepath.Join(codexDir, "requirements.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotCodexReqs) != config.CodexRequirementsTemplate("opinionated") {
		t.Fatalf("codex requirements were not reset")
	}

	claudeAuth, err := os.ReadFile(filepath.Join(claudeDir, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(claudeAuth) != `{"auth":"keep"}` {
		t.Fatalf("claude auth changed unexpectedly")
	}
	codexAuth, err := os.ReadFile(filepath.Join(codexDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(codexAuth) != `{"auth":"keep"}` {
		t.Fatalf("codex auth changed unexpectedly")
	}
}

func TestProfileReset_IncludeAuthClearsAuthFiles(t *testing.T) {
	h2Dir := setupProfileTestH2Dir(t)
	name := "work"

	sharedDir := filepath.Join(h2Dir, "account-profiles-shared", name)
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		t.Fatal(err)
	}

	claudeDir := filepath.Join(h2Dir, "claude-config", name)
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, ".claude.json"), []byte(`{"auth":"delete"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	codexDir := filepath.Join(h2Dir, "codex-config", name)
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(`{"auth":"delete"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newProfileResetCmd()
	cmd.SetArgs([]string{name, "--include-auth", "--include-skills=false", "--include-instructions=false", "--include-settings=false"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("profile reset failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(claudeDir, ".claude.json")); !os.IsNotExist(err) {
		t.Fatalf("expected .claude.json to be removed, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(codexDir, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("expected auth.json to be removed, err=%v", err)
	}
}
