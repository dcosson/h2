package sandbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreate_BasicSandbox(t *testing.T) {
	baseDir := t.TempDir()

	sb, err := Create("test-bench", "hooks", "", baseDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if sb.Name != "test-bench" {
		t.Errorf("Name = %q, want %q", sb.Name, "test-bench")
	}
	if sb.Preset != "hooks" {
		t.Errorf("Preset = %q, want %q", sb.Preset, "hooks")
	}

	// Verify directory structure.
	for _, sub := range allDirs {
		path := filepath.Join(sb.Dir, sub)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected directory %s to exist", sub)
		}
	}

	// Verify h2 marker file.
	markerPath := filepath.Join(sb.Dir, ".h2-dir.txt")
	if _, err := os.Stat(markerPath); os.IsNotExist(err) {
		t.Error("expected .h2-dir.txt marker to exist")
	}

	// Verify config.yaml.
	configPath := filepath.Join(sb.Dir, "config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("expected config.yaml to exist")
	}

	// Verify sandbox.json metadata.
	meta, err := readMeta(sb.Dir)
	if err != nil {
		t.Fatalf("readMeta: %v", err)
	}
	if meta.Name != "test-bench" {
		t.Errorf("meta.Name = %q, want %q", meta.Name, "test-bench")
	}
	if meta.Preset != "hooks" {
		t.Errorf("meta.Preset = %q, want %q", meta.Preset, "hooks")
	}
}

func TestCreate_EmptyPreset(t *testing.T) {
	baseDir := t.TempDir()

	sb, err := Create("empty-bench", "empty", "", baseDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Empty preset should have empty settings.json (no hooks).
	settingsPath := filepath.Join(sb.Dir, "claude-config", "default", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}

	if len(settings) != 0 {
		t.Errorf("empty preset should have no settings, got %v", settings)
	}

	// No roles should exist.
	rolesDir := filepath.Join(sb.Dir, "roles")
	entries, _ := os.ReadDir(rolesDir)
	if len(entries) != 0 {
		t.Errorf("empty preset should have no roles, got %d", len(entries))
	}

	// No CLAUDE.md.
	claudeMDPath := filepath.Join(sb.Dir, "claude-config", "default", "CLAUDE.md")
	if _, err := os.Stat(claudeMDPath); !os.IsNotExist(err) {
		t.Error("empty preset should not have CLAUDE.md")
	}
}

func TestCreate_HooksPreset(t *testing.T) {
	baseDir := t.TempDir()

	sb, err := Create("hooks-bench", "hooks", "", baseDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Hooks preset should have h2 hooks in settings.json.
	settingsPath := filepath.Join(sb.Dir, "claude-config", "default", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks not found in settings.json")
	}
	for _, event := range []string{"PreToolUse", "PostToolUse", "SessionStart", "Stop", "UserPromptSubmit", "PermissionRequest"} {
		if _, ok := hooks[event]; !ok {
			t.Errorf("hook event %q not found", event)
		}
	}
}

func TestCreate_HaikuPreset(t *testing.T) {
	baseDir := t.TempDir()

	sb, err := Create("haiku-bench", "haiku", "", baseDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Haiku preset should have a default role.
	rolePath := filepath.Join(sb.Dir, "roles", "default.yaml")
	data, err := os.ReadFile(rolePath)
	if err != nil {
		t.Fatalf("read default role: %v", err)
	}

	roleContent := string(data)
	if !strings.Contains(roleContent, "model: haiku") {
		t.Error("haiku preset default role should specify model: haiku")
	}
	if !strings.Contains(roleContent, "permission_mode: bypassPermissions") {
		t.Error("haiku preset default role should have bypassPermissions")
	}
}

func TestCreate_OpusPreset(t *testing.T) {
	baseDir := t.TempDir()

	sb, err := Create("opus-bench", "opus", "", baseDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Opus preset should have multiple roles.
	expectedRoles := []string{"default", "concierge", "coder", "reviewer"}
	for _, roleName := range expectedRoles {
		rolePath := filepath.Join(sb.Dir, "roles", roleName+".yaml")
		if _, err := os.Stat(rolePath); os.IsNotExist(err) {
			t.Errorf("expected role %q to exist", roleName)
		}
	}

	// Should have CLAUDE.md.
	claudeMDPath := filepath.Join(sb.Dir, "claude-config", "default", "CLAUDE.md")
	data, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	if !strings.Contains(string(data), "Benchmark Agent") {
		t.Error("CLAUDE.md should contain benchmark agent instructions")
	}

	// Should have benchmark pod template.
	templatePath := filepath.Join(sb.Dir, "pods", "templates", "benchmark.yaml")
	if _, err := os.Stat(templatePath); os.IsNotExist(err) {
		t.Error("expected benchmark pod template to exist")
	}
}

func TestCreate_WithAuthFrom(t *testing.T) {
	baseDir := t.TempDir()

	// Create a source auth dir with a .claude.json.
	authDir := filepath.Join(t.TempDir(), "auth")
	os.MkdirAll(authDir, 0o755)
	authContent := `{"oauthAccount": {"accountUuid": "test-uuid", "emailAddress": "test@example.com"}}`
	os.WriteFile(filepath.Join(authDir, ".claude.json"), []byte(authContent), 0o644)

	sb, err := Create("auth-bench", "empty", authDir, baseDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify .claude.json was copied.
	claudeJSONPath := filepath.Join(sb.Dir, "claude-config", "default", ".claude.json")
	data, err := os.ReadFile(claudeJSONPath)
	if err != nil {
		t.Fatalf("read .claude.json: %v", err)
	}

	if string(data) != authContent {
		t.Errorf(".claude.json content = %q, want %q", string(data), authContent)
	}
}

func TestCreate_DuplicateName(t *testing.T) {
	baseDir := t.TempDir()

	_, err := Create("dup", "empty", "", baseDir)
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}

	_, err = Create("dup", "empty", "", baseDir)
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, expected 'already exists'", err.Error())
	}
}

func TestCreate_InvalidName(t *testing.T) {
	baseDir := t.TempDir()

	tests := []struct {
		name    string
		wantErr bool
	}{
		{"valid-name", false},
		{"valid_name", false},
		{"ValidName123", false},
		{"invalid name", true},
		{"invalid/name", true},
		{"invalid.name", true},
		{"", true}, // caught before validateName
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Create(tt.name, "empty", "", baseDir)
			if (err != nil) != tt.wantErr {
				t.Errorf("Create(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
			if !tt.wantErr {
				// Clean up for next test.
				Destroy(tt.name, baseDir)
			}
		})
	}
}

func TestCreate_InvalidPreset(t *testing.T) {
	baseDir := t.TempDir()

	_, err := Create("bad-preset", "nonexistent", "", baseDir)
	if err == nil {
		t.Fatal("expected error for invalid preset")
	}
	if !strings.Contains(err.Error(), "unknown preset") {
		t.Errorf("error = %q, expected 'unknown preset'", err.Error())
	}
}

func TestReset_PreservesAuth(t *testing.T) {
	baseDir := t.TempDir()

	// Create with auth.
	authDir := filepath.Join(t.TempDir(), "auth")
	os.MkdirAll(authDir, 0o755)
	authContent := `{"oauthAccount": {"accountUuid": "reset-test"}}`
	os.WriteFile(filepath.Join(authDir, ".claude.json"), []byte(authContent), 0o644)

	sb, err := Create("reset-test", "hooks", authDir, baseDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Write some session data that should be wiped.
	sessionFile := filepath.Join(sb.Dir, "sessions", "test-session.json")
	os.WriteFile(sessionFile, []byte(`{"test": true}`), 0o644)

	// Write a role file that should be re-created from preset.
	rolePath := filepath.Join(sb.Dir, "roles", "custom.yaml")
	os.WriteFile(rolePath, []byte(`name: custom\ninstructions: test`), 0o644)

	// Reset.
	if err := Reset("reset-test", "", baseDir); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	// Auth should be preserved.
	claudeJSONPath := filepath.Join(sb.Dir, "claude-config", "default", ".claude.json")
	data, err := os.ReadFile(claudeJSONPath)
	if err != nil {
		t.Fatalf("read .claude.json after reset: %v", err)
	}
	if string(data) != authContent {
		t.Error(".claude.json should be preserved after reset")
	}

	// Session data should be wiped.
	if _, err := os.Stat(sessionFile); !os.IsNotExist(err) {
		t.Error("session data should be wiped after reset")
	}

	// Custom role should be wiped (only preset roles remain).
	if _, err := os.Stat(rolePath); !os.IsNotExist(err) {
		t.Error("custom role should be wiped after reset")
	}
}

func TestReset_ChangesPreset(t *testing.T) {
	baseDir := t.TempDir()

	_, err := Create("preset-change", "empty", "", baseDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Reset with a different preset.
	if err := Reset("preset-change", "haiku", baseDir); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	// Metadata should reflect new preset.
	meta, err := readMeta(sandboxDir(baseDir, "preset-change"))
	if err != nil {
		t.Fatalf("readMeta: %v", err)
	}
	if meta.Preset != "haiku" {
		t.Errorf("preset after reset = %q, want %q", meta.Preset, "haiku")
	}

	// Should now have haiku role.
	rolePath := filepath.Join(sandboxDir(baseDir, "preset-change"), "roles", "default.yaml")
	data, err := os.ReadFile(rolePath)
	if err != nil {
		t.Fatalf("read default role after reset: %v", err)
	}
	if !strings.Contains(string(data), "model: haiku") {
		t.Error("after preset change, default role should have model: haiku")
	}
}

func TestReset_NonexistentSandbox(t *testing.T) {
	baseDir := t.TempDir()

	err := Reset("nonexistent", "", baseDir)
	if err == nil {
		t.Fatal("expected error for nonexistent sandbox")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, expected 'not found'", err.Error())
	}
}

func TestDestroy(t *testing.T) {
	baseDir := t.TempDir()

	sb, err := Create("destroy-me", "empty", "", baseDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify it exists.
	if _, err := os.Stat(sb.Dir); os.IsNotExist(err) {
		t.Fatal("sandbox should exist before destroy")
	}

	if err := Destroy("destroy-me", baseDir); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	// Verify it's gone.
	if _, err := os.Stat(sb.Dir); !os.IsNotExist(err) {
		t.Error("sandbox directory should be removed after destroy")
	}
}

func TestDestroy_Nonexistent(t *testing.T) {
	baseDir := t.TempDir()

	err := Destroy("nonexistent", baseDir)
	if err == nil {
		t.Fatal("expected error for nonexistent sandbox")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %q, expected 'does not exist'", err.Error())
	}
}

func TestList_Empty(t *testing.T) {
	baseDir := t.TempDir()

	infos, err := List(baseDir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("expected empty list, got %d", len(infos))
	}
}

func TestList_MultipleSandboxes(t *testing.T) {
	baseDir := t.TempDir()

	// Create several sandboxes.
	for _, name := range []string{"charlie", "alpha", "bravo"} {
		if _, err := Create(name, "empty", "", baseDir); err != nil {
			t.Fatalf("Create(%s): %v", name, err)
		}
	}

	infos, err := List(baseDir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(infos) != 3 {
		t.Fatalf("expected 3 sandboxes, got %d", len(infos))
	}

	// Should be sorted alphabetically.
	if infos[0].Name != "alpha" || infos[1].Name != "bravo" || infos[2].Name != "charlie" {
		t.Errorf("list not sorted: %v, %v, %v", infos[0].Name, infos[1].Name, infos[2].Name)
	}
}

func TestList_WithAuth(t *testing.T) {
	baseDir := t.TempDir()

	// Create sandbox with auth.
	authDir := filepath.Join(t.TempDir(), "auth")
	os.MkdirAll(authDir, 0o755)
	os.WriteFile(filepath.Join(authDir, ".claude.json"), []byte(`{"test": true}`), 0o644)

	Create("with-auth", "empty", authDir, baseDir)
	// Use a non-existent auth source so no .claude.json gets copied.
	noAuthDir := filepath.Join(t.TempDir(), "no-auth-source")
	Create("no-auth", "empty", noAuthDir, baseDir)

	infos, err := List(baseDir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	// Find the sandbox with auth.
	for _, info := range infos {
		if info.Name == "with-auth" && !info.HasAuth {
			t.Error("with-auth sandbox should have HasAuth=true")
		}
		if info.Name == "no-auth" && info.HasAuth {
			t.Error("no-auth sandbox should have HasAuth=false")
		}
	}
}

func TestGet(t *testing.T) {
	baseDir := t.TempDir()

	created, err := Create("get-me", "haiku", "", baseDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := Get("get-me", baseDir)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name != created.Name {
		t.Errorf("Name = %q, want %q", got.Name, created.Name)
	}
	if got.Preset != "haiku" {
		t.Errorf("Preset = %q, want %q", got.Preset, "haiku")
	}
	if got.Dir != created.Dir {
		t.Errorf("Dir = %q, want %q", got.Dir, created.Dir)
	}
}

func TestGet_Nonexistent(t *testing.T) {
	baseDir := t.TempDir()

	_, err := Get("nonexistent", baseDir)
	if err == nil {
		t.Fatal("expected error for nonexistent sandbox")
	}
}

func TestExec_SimpleCommand(t *testing.T) {
	baseDir := t.TempDir()

	_, err := Create("exec-test", "empty", "", baseDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	output, err := Exec("exec-test", baseDir, []string{"echo", "hello"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	if strings.TrimSpace(string(output)) != "hello" {
		t.Errorf("output = %q, want %q", string(output), "hello")
	}
}

func TestExec_H2DirEnvSet(t *testing.T) {
	baseDir := t.TempDir()

	sb, err := Create("env-test", "empty", "", baseDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Use printenv to verify H2_DIR is set to the sandbox directory.
	output, err := Exec("env-test", baseDir, []string{"printenv", "H2_DIR"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	if strings.TrimSpace(string(output)) != sb.Dir {
		t.Errorf("H2_DIR = %q, want %q", strings.TrimSpace(string(output)), sb.Dir)
	}
}

func TestExec_Nonexistent(t *testing.T) {
	baseDir := t.TempDir()

	_, err := Exec("nonexistent", baseDir, []string{"echo", "hello"})
	if err == nil {
		t.Fatal("expected error for nonexistent sandbox")
	}
}

func TestExec_NoCommand(t *testing.T) {
	baseDir := t.TempDir()

	_, err := Exec("test", baseDir, []string{})
	if err == nil {
		t.Fatal("expected error for empty command")
	}
	if !strings.Contains(err.Error(), "no command") {
		t.Errorf("error = %q, expected 'no command'", err.Error())
	}
}

func TestClaudeConfigDir(t *testing.T) {
	sb := &Sandbox{Dir: "/test/sandbox"}
	want := "/test/sandbox/claude-config/default"
	if got := sb.ClaudeConfigDir(); got != want {
		t.Errorf("ClaudeConfigDir() = %q, want %q", got, want)
	}
}

func TestValidateName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"valid", false},
		{"valid-name", false},
		{"valid_name", false},
		{"CamelCase", false},
		{"name123", false},
		{"has space", true},
		{"has/slash", true},
		{"has.dot", true},
		{"has@symbol", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateName(tt.name)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateName(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
		})
	}
}

func TestSandboxEnv(t *testing.T) {
	env := sandboxEnv("/test/dir")

	found := false
	for _, e := range env {
		if e == "H2_DIR=/test/dir" {
			found = true
			break
		}
	}
	if !found {
		t.Error("H2_DIR not found in sandbox env")
	}
}

func TestPresetSettings(t *testing.T) {
	tests := []struct {
		name     string
		preset   string
		hasHooks bool
	}{
		{"empty has no hooks", "empty", false},
		{"hooks has hooks", "hooks", true},
		{"haiku has hooks", "haiku", true},
		{"opus has hooks", "opus", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preset, err := GetPreset(tt.preset)
			if err != nil {
				t.Fatalf("GetPreset(%q): %v", tt.preset, err)
			}

			settings := preset.Settings()
			_, hasHooks := settings["hooks"]
			if hasHooks != tt.hasHooks {
				t.Errorf("preset %q hasHooks = %v, want %v", tt.preset, hasHooks, tt.hasHooks)
			}
		})
	}
}

func TestPresetRoles(t *testing.T) {
	tests := []struct {
		name       string
		preset     string
		wantRoles  []string
	}{
		{"empty has no roles", "empty", nil},
		{"hooks has no roles", "hooks", nil},
		{"haiku has default role", "haiku", []string{"default"}},
		{"opus has all roles", "opus", []string{"default", "concierge", "coder", "reviewer"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preset, err := GetPreset(tt.preset)
			if err != nil {
				t.Fatalf("GetPreset(%q): %v", tt.preset, err)
			}

			roles := preset.Roles()
			if tt.wantRoles == nil {
				if roles != nil && len(roles) > 0 {
					t.Errorf("preset %q should have no roles, got %d", tt.preset, len(roles))
				}
			} else {
				for _, roleName := range tt.wantRoles {
					if _, ok := roles[roleName]; !ok {
						t.Errorf("preset %q missing role %q", tt.preset, roleName)
					}
				}
			}
		})
	}
}

func TestPresetClaudeMD(t *testing.T) {
	tests := []struct {
		name      string
		preset    string
		wantEmpty bool
	}{
		{"empty has no CLAUDE.md", "empty", true},
		{"hooks has no CLAUDE.md", "hooks", true},
		{"haiku has no CLAUDE.md", "haiku", true},
		{"opus has CLAUDE.md", "opus", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preset, err := GetPreset(tt.preset)
			if err != nil {
				t.Fatalf("GetPreset(%q): %v", tt.preset, err)
			}

			claudeMD := preset.ClaudeMD()
			if (claudeMD == "") != tt.wantEmpty {
				t.Errorf("preset %q ClaudeMD empty = %v, want %v", tt.preset, claudeMD == "", tt.wantEmpty)
			}
		})
	}
}

func TestPresetPodTemplates(t *testing.T) {
	tests := []struct {
		name          string
		preset        string
		wantTemplates []string
	}{
		{"empty has no templates", "empty", nil},
		{"hooks has no templates", "hooks", nil},
		{"haiku has no templates", "haiku", nil},
		{"opus has benchmark template", "opus", []string{"benchmark"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preset, err := GetPreset(tt.preset)
			if err != nil {
				t.Fatalf("GetPreset(%q): %v", tt.preset, err)
			}

			templates := preset.PodTemplates()
			if tt.wantTemplates == nil {
				if templates != nil && len(templates) > 0 {
					t.Errorf("preset %q should have no templates, got %d", tt.preset, len(templates))
				}
			} else {
				for _, tmplName := range tt.wantTemplates {
					if _, ok := templates[tmplName]; !ok {
						t.Errorf("preset %q missing template %q", tt.preset, tmplName)
					}
				}
			}
		})
	}
}

func TestGetPreset_Invalid(t *testing.T) {
	_, err := GetPreset("nonexistent")
	if err == nil {
		t.Fatal("expected error for invalid preset")
	}
	if !strings.Contains(err.Error(), "unknown preset") {
		t.Errorf("error = %q, expected 'unknown preset'", err.Error())
	}
}

func TestReset_WipesSettingsAndClaudeMD(t *testing.T) {
	baseDir := t.TempDir()

	// Create with opus preset (has CLAUDE.md).
	sb, err := Create("wipe-test", "opus", "", baseDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	claudeMDPath := filepath.Join(sb.Dir, "claude-config", "default", "CLAUDE.md")
	if _, err := os.Stat(claudeMDPath); os.IsNotExist(err) {
		t.Fatal("opus preset should create CLAUDE.md")
	}

	// Reset to empty preset (no CLAUDE.md).
	if err := Reset("wipe-test", "empty", baseDir); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	// CLAUDE.md should be gone after reset to empty.
	if _, err := os.Stat(claudeMDPath); !os.IsNotExist(err) {
		t.Error("CLAUDE.md should be removed when resetting to empty preset")
	}

	// settings.json should exist but be empty.
	settingsPath := filepath.Join(sb.Dir, "claude-config", "default", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}
	if len(settings) != 0 {
		t.Errorf("empty preset settings should be empty, got %v", settings)
	}
}

func TestCreate_NoAuthSourceOK(t *testing.T) {
	baseDir := t.TempDir()

	// Create with a non-existent auth source — should still succeed
	// (no auth to copy is not an error).
	nonExistentAuth := filepath.Join(t.TempDir(), "does-not-exist")
	sb, err := Create("no-auth-src", "empty", nonExistentAuth, baseDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// .claude.json should not exist.
	claudeJSONPath := filepath.Join(sb.Dir, "claude-config", "default", ".claude.json")
	if _, err := os.Stat(claudeJSONPath); !os.IsNotExist(err) {
		t.Error(".claude.json should not exist when auth source doesn't exist")
	}
}
