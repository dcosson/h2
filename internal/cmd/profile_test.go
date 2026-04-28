package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"h2/internal/config"
)

func setupProfileTestH2Dir(t *testing.T) string {
	t.Helper()

	config.ResetResolveCache()
	t.Cleanup(config.ResetResolveCache)

	h2Dir := filepath.Join(t.TempDir(), "myh2")
	for _, sub := range []string{
		"profiles-shared",
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
	if err := os.MkdirAll(filepath.Join(h2Dir, "profiles-shared", srcProfile, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h2Dir, "profiles-shared", srcProfile, "CLAUDE_AND_AGENTS.md"), []byte("shared"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h2Dir, "profiles-shared", srcProfile, "skills", "SKILL.md"), []byte("skill"), 0o644); err != nil {
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
	// Runtime state in the source must NOT leak into the new profile.
	if err := os.WriteFile(filepath.Join(h2Dir, "claude-config", srcProfile, "ratelimit.json"), []byte(`{"resets_at":"2099-01-01T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h2Dir, "claude-config", srcProfile, "history.jsonl"), []byte(`{"x":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h2Dir, "claude-config", srcProfile, "projects", "some-project"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h2Dir, "claude-config", srcProfile, "projects", "some-project", "session.jsonl"), []byte(""), 0o644); err != nil {
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
	if err := os.WriteFile(filepath.Join(h2Dir, "codex-config", srcProfile, "ratelimit.json"), []byte(`{"resets_at":"2099-01-01T00:00:00Z"}`), 0o644); err != nil {
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

	sharedLink := filepath.Join(h2Dir, "profiles-shared", "new")
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
	if want := filepath.Join("..", "..", "profiles-shared", "new", "CLAUDE_AND_AGENTS.md"); claudeTarget != want {
		t.Fatalf("claude CLAUDE.md target = %q, want %q", claudeTarget, want)
	}

	codexTarget, err := os.Readlink(filepath.Join(h2Dir, "codex-config", "new", "AGENTS.md"))
	if err != nil {
		t.Fatalf("readlink codex shared link: %v", err)
	}
	if want := filepath.Join("..", "..", "profiles-shared", "new", "CLAUDE_AND_AGENTS.md"); codexTarget != want {
		t.Fatalf("codex AGENTS.md target = %q, want %q", codexTarget, want)
	}

	// Runtime state from the source must not be copied into the new profile.
	for _, leaked := range []string{
		filepath.Join(h2Dir, "claude-config", "new", "ratelimit.json"),
		filepath.Join(h2Dir, "claude-config", "new", "history.jsonl"),
		filepath.Join(h2Dir, "claude-config", "new", "projects"),
		filepath.Join(h2Dir, "codex-config", "new", "ratelimit.json"),
	} {
		if _, err := os.Stat(leaked); !os.IsNotExist(err) {
			t.Fatalf("expected %s to not exist (runtime state should not leak), err=%v", leaked, err)
		}
	}

	// settings.json should be the fresh template, not a copy of the source's contents.
	gotClaudeSettings, err := os.ReadFile(filepath.Join(h2Dir, "claude-config", "new", "settings.json"))
	if err != nil {
		t.Fatalf("read claude settings: %v", err)
	}
	if string(gotClaudeSettings) == `{"ok":true}` {
		t.Fatalf("claude settings.json was copied from source instead of templated")
	}
	if string(gotClaudeSettings) != config.ClaudeSettingsTemplate("opinionated") {
		t.Fatalf("claude settings.json was not the opinionated template")
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

	sharedDir := filepath.Join(h2Dir, "profiles-shared", name)
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

	cmd := newProfileUpdateCmd()
	cmd.SetArgs([]string{name})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("profile update failed: %v", err)
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

	sharedMeta, err := config.ReadContentMeta(sharedDir)
	if err != nil {
		t.Fatalf("read shared metadata: %v", err)
	}
	if _, ok := sharedMeta.Files["CLAUDE_AND_AGENTS.md"]; !ok {
		t.Fatalf("expected CLAUDE_AND_AGENTS.md metadata entry")
	}
	if _, ok := sharedMeta.Files["skills/shaping/SKILL.md"]; !ok {
		t.Fatalf("expected managed skill metadata entry")
	}
	if _, ok := sharedMeta.Files["skills/custom-skill/SKILL.md"]; ok {
		t.Fatalf("did not expect custom skill metadata entry")
	}

	claudeMeta, err := config.ReadContentMeta(claudeDir)
	if err != nil {
		t.Fatalf("read claude metadata: %v", err)
	}
	if _, ok := claudeMeta.Files["settings.json"]; !ok {
		t.Fatalf("expected settings.json metadata entry")
	}

	codexMeta, err := config.ReadContentMeta(codexDir)
	if err != nil {
		t.Fatalf("read codex metadata: %v", err)
	}
	if _, ok := codexMeta.Files["config.toml"]; !ok {
		t.Fatalf("expected config.toml metadata entry")
	}
	if _, ok := codexMeta.Files["requirements.toml"]; !ok {
		t.Fatalf("expected requirements.toml metadata entry")
	}
}

func TestProfileReset_IncludeAuthClearsAuthFiles(t *testing.T) {
	h2Dir := setupProfileTestH2Dir(t)
	name := "work"

	sharedDir := filepath.Join(h2Dir, "profiles-shared", name)
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

	cmd := newProfileUpdateCmd()
	cmd.SetArgs([]string{name, "--include-auth", "--include-skills=false", "--include-instructions=false", "--include-settings=false"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("profile update failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(claudeDir, ".claude.json")); !os.IsNotExist(err) {
		t.Fatalf("expected .claude.json to be removed, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(codexDir, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("expected auth.json to be removed, err=%v", err)
	}
}

func TestProfileShow_IncludesSymlinksAndMetadata(t *testing.T) {
	h2Dir := setupProfileTestH2Dir(t)
	cmd := newProfileCreateCmd()
	cmd.SetArgs([]string{"demo"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("profile create failed: %v", err)
	}

	show := newProfileShowCmd()
	var out bytes.Buffer
	show.SetOut(&out)
	show.SetErr(&out)
	show.SetArgs([]string{"demo"})
	if err := show.Execute(); err != nil {
		t.Fatalf("profile show failed: %v", err)
	}

	s := out.String()
	checks := []string{
		"Symlink profiles-shared/demo: no",
		"Symlink claude-config/demo/CLAUDE.md: yes ->",
		"Symlink codex-config/demo/AGENTS.md: yes ->",
		"Metadata profiles-shared/demo:",
		"CLAUDE_AND_AGENTS.md | v",
		"Metadata claude-config/demo:",
		"settings.json | v",
		"Metadata codex-config/demo:",
		"config.toml | v",
		"requirements.toml | v",
	}
	for _, want := range checks {
		if !strings.Contains(s, want) {
			t.Fatalf("profile show output missing %q:\n%s", want, s)
		}
	}

	if _, err := os.Stat(filepath.Join(h2Dir, "profiles-shared", "demo", config.ContentMetaFileName)); err != nil {
		t.Fatalf("missing shared metadata file: %v", err)
	}
}

func TestProfileUpdate_All(t *testing.T) {
	h2Dir := setupProfileTestH2Dir(t)

	// Create two profiles.
	for _, name := range []string{"alpha", "beta"} {
		cmd := newProfileCreateCmd()
		cmd.SetArgs([]string{name})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("create profile %s: %v", name, err)
		}
	}

	// Modify instructions in both to make them stale.
	for _, name := range []string{"alpha", "beta"} {
		p := filepath.Join(h2Dir, "profiles-shared", name, "CLAUDE_AND_AGENTS.md")
		if err := os.WriteFile(p, []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Run update --all.
	cmd := newProfileUpdateCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--all"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("update --all failed: %v", err)
	}

	output := out.String()
	// Both profiles should be mentioned.
	if !strings.Contains(output, `Updating profile "alpha"`) {
		t.Fatalf("expected alpha in output:\n%s", output)
	}
	if !strings.Contains(output, `Updating profile "beta"`) {
		t.Fatalf("expected beta in output:\n%s", output)
	}

	// Verify both were actually updated.
	for _, name := range []string{"alpha", "beta"} {
		got, err := os.ReadFile(filepath.Join(h2Dir, "profiles-shared", name, "CLAUDE_AND_AGENTS.md"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) == "stale" {
			t.Fatalf("profile %s was not updated", name)
		}
	}
}

func TestProfileUpdate_AllAndNameConflict(t *testing.T) {
	setupProfileTestH2Dir(t)

	cmd := newProfileUpdateCmd()
	cmd.SetArgs([]string{"--all", "some-name"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --all and name both provided")
	}
	if !strings.Contains(err.Error(), "cannot specify both") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProfileUpdate_DryRun(t *testing.T) {
	h2Dir := setupProfileTestH2Dir(t)

	// Create a profile.
	createCmd := newProfileCreateCmd()
	createCmd.SetArgs([]string{"test-profile"})
	if err := createCmd.Execute(); err != nil {
		t.Fatal(err)
	}

	// Make instructions stale so dry-run shows "updated".
	instrPath := filepath.Join(h2Dir, "profiles-shared", "test-profile", "CLAUDE_AND_AGENTS.md")
	if err := os.WriteFile(instrPath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	origContent, _ := os.ReadFile(instrPath)

	// Run update --dry-run.
	cmd := newProfileUpdateCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"test-profile", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "CLAUDE_AND_AGENTS.md: updated") {
		t.Fatalf("expected 'updated' status in dry-run output:\n%s", output)
	}
	if !strings.Contains(output, "Dry run") {
		t.Fatalf("expected 'Dry run' in output:\n%s", output)
	}

	// File should NOT have changed.
	afterContent, _ := os.ReadFile(instrPath)
	if string(afterContent) != string(origContent) {
		t.Fatalf("dry-run modified the file")
	}
}

func TestProfileUpdate_DryRunUnchanged(t *testing.T) {
	setupProfileTestH2Dir(t)

	// Create a profile (fresh, so everything matches templates).
	createCmd := newProfileCreateCmd()
	createCmd.SetArgs([]string{"fresh"})
	if err := createCmd.Execute(); err != nil {
		t.Fatal(err)
	}

	// Run update --dry-run — everything should be "unchanged".
	cmd := newProfileUpdateCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"fresh", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "CLAUDE_AND_AGENTS.md: unchanged") {
		t.Fatalf("expected 'unchanged' for fresh profile:\n%s", output)
	}
}

func TestProfileUpdate_DryRunAll(t *testing.T) {
	h2Dir := setupProfileTestH2Dir(t)

	// Create two profiles, make one stale.
	for _, name := range []string{"p1", "p2"} {
		cmd := newProfileCreateCmd()
		cmd.SetArgs([]string{name})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(h2Dir, "profiles-shared", "p1", "CLAUDE_AND_AGENTS.md"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newProfileUpdateCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--all", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dry-run --all failed: %v", err)
	}

	output := out.String()
	// p1 should show updated, p2 unchanged.
	if !strings.Contains(output, `Updating profile "p1"`) {
		t.Fatalf("missing p1 in output:\n%s", output)
	}
	if !strings.Contains(output, `Updating profile "p2"`) {
		t.Fatalf("missing p2 in output:\n%s", output)
	}
	if !strings.Contains(output, "Dry run") {
		t.Fatalf("missing 'Dry run' in output:\n%s", output)
	}

	// Verify no files were modified.
	p1Content, _ := os.ReadFile(filepath.Join(h2Dir, "profiles-shared", "p1", "CLAUDE_AND_AGENTS.md"))
	if string(p1Content) != "stale" {
		t.Fatal("dry-run modified p1")
	}
}

func TestDiscoverProfilesWithHarness(t *testing.T) {
	h2Dir := setupProfileTestH2Dir(t)

	// Create claude-only, codex-only, and both profiles.
	os.MkdirAll(filepath.Join(h2Dir, "claude-config", "claude-only"), 0o755)
	os.MkdirAll(filepath.Join(h2Dir, "codex-config", "codex-only"), 0o755)
	os.MkdirAll(filepath.Join(h2Dir, "claude-config", "both"), 0o755)
	os.MkdirAll(filepath.Join(h2Dir, "codex-config", "both"), 0o755)
	// profiles-shared only should NOT appear.
	os.MkdirAll(filepath.Join(h2Dir, "profiles-shared", "shared-only"), 0o755)

	infos, err := discoverProfilesWithHarness(h2Dir)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string][]string{}
	for _, info := range infos {
		got[info.Name] = info.Harnesses
	}

	// Check expected profiles.
	if h := got["both"]; len(h) != 2 || h[0] != "claude_code" || h[1] != "codex" {
		t.Errorf("both: got %v, want [claude_code codex]", h)
	}
	if h := got["claude-only"]; len(h) != 1 || h[0] != "claude_code" {
		t.Errorf("claude-only: got %v, want [claude_code]", h)
	}
	if h := got["codex-only"]; len(h) != 1 || h[0] != "codex" {
		t.Errorf("codex-only: got %v, want [codex]", h)
	}
	if _, found := got["shared-only"]; found {
		t.Error("profiles-shared-only profile should not be discovered")
	}
}

func TestProfileList_ShowsHarnesses(t *testing.T) {
	h2Dir := setupProfileTestH2Dir(t)

	os.MkdirAll(filepath.Join(h2Dir, "claude-config", "staging"), 0o755)
	os.MkdirAll(filepath.Join(h2Dir, "codex-config", "staging"), 0o755)
	os.MkdirAll(filepath.Join(h2Dir, "claude-config", "prod"), 0o755)

	cmd := newProfileListCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	output := out.String()
	if !strings.Contains(output, "prod (claude_code)") {
		t.Errorf("expected 'prod (claude_code)' in output:\n%s", output)
	}
	if !strings.Contains(output, "staging (claude_code, codex)") {
		t.Errorf("expected 'staging (claude_code, codex)' in output:\n%s", output)
	}
}

func TestProfileList_ShowsRateLimitedHarness(t *testing.T) {
	h2Dir := setupProfileTestH2Dir(t)

	os.MkdirAll(filepath.Join(h2Dir, "claude-config", "default"), 0o755)
	os.MkdirAll(filepath.Join(h2Dir, "codex-config", "default"), 0o755)

	// Write a rate limit for codex only.
	rl := &config.RateLimitInfo{
		ResetsAt:   time.Now().Add(2 * time.Hour),
		RecordedAt: time.Now(),
		AgentName:  "test-agent",
	}
	if err := config.WriteRateLimit(filepath.Join(h2Dir, "codex-config", "default"), rl); err != nil {
		t.Fatal(err)
	}

	infos, err := discoverProfilesWithHarness(h2Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(infos))
	}

	label := formatHarnessLabelsPlain(infos[0])
	if !strings.Contains(label, "codex rate limited until") {
		t.Errorf("expected rate limit in label, got: %s", label)
	}
	// claude_code should not be rate limited.
	if strings.Contains(label, "claude_code rate limited") {
		t.Errorf("claude_code should not be rate limited: %s", label)
	}
}

func TestFormatHarnessLabels_NoRateLimit(t *testing.T) {
	p := profileInfo{
		Name:      "test",
		Harnesses: []string{"claude_code", "codex"},
	}
	got := formatHarnessLabelsPlain(p)
	if got != "claude_code, codex" {
		t.Errorf("got %q, want %q", got, "claude_code, codex")
	}
}

func TestFormatHarnessLabels_WithRateLimit(t *testing.T) {
	resetsAt := time.Date(2026, 3, 25, 12, 45, 0, 0, time.Local)
	p := profileInfo{
		Name:      "test",
		Harnesses: []string{"claude_code", "codex"},
		RateLimitedMap: map[string]*config.RateLimitInfo{
			"codex": {ResetsAt: resetsAt},
		},
	}
	got := formatHarnessLabelsPlain(p)
	want := "claude_code, codex rate limited until Mar 25 12:45 PM"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatHarnessLabels_WithIndefiniteRateLimit(t *testing.T) {
	p := profileInfo{
		Name:      "test",
		Harnesses: []string{"claude_code", "codex"},
		RateLimitedMap: map[string]*config.RateLimitInfo{
			"claude_code": {},
		},
	}
	got := formatHarnessLabelsPlain(p)
	want := "claude_code rate limited, codex"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
