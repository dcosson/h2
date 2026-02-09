package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRoleFrom_FullRole(t *testing.T) {
	yaml := `
name: architect
description: "Designs systems"
model: opus
instructions: |
  You are an architect agent.
  Design system architecture.
permissions:
  allow:
    - "Read"
    - "Glob"
    - "Write(docs/**)"
  deny:
    - "Bash(rm -rf *)"
  agent:
    enabled: true
    instructions: |
      You are reviewing permissions for an architect.
      ALLOW: read-only tools
      DENY: destructive operations
`
	path := writeTempFile(t, "architect.yaml", yaml)

	role, err := LoadRoleFrom(path)
	if err != nil {
		t.Fatalf("LoadRoleFrom: %v", err)
	}

	if role.Name != "architect" {
		t.Errorf("Name = %q, want %q", role.Name, "architect")
	}
	if role.Description != "Designs systems" {
		t.Errorf("Description = %q, want %q", role.Description, "Designs systems")
	}
	if role.Model != "opus" {
		t.Errorf("Model = %q, want %q", role.Model, "opus")
	}
	if len(role.Permissions.Allow) != 3 {
		t.Errorf("Allow len = %d, want 3", len(role.Permissions.Allow))
	}
	if len(role.Permissions.Deny) != 1 {
		t.Errorf("Deny len = %d, want 1", len(role.Permissions.Deny))
	}
	if role.Permissions.Agent == nil {
		t.Fatal("Agent is nil")
	}
	if !role.Permissions.Agent.IsEnabled() {
		t.Error("Agent should be enabled")
	}
	if role.Permissions.Agent.Instructions == "" {
		t.Error("Agent instructions should not be empty")
	}
}

func TestLoadRoleFrom_MinimalRole(t *testing.T) {
	yaml := `
name: coder
instructions: |
  You are a coding agent.
permissions:
  allow:
    - "Read"
    - "Bash"
`
	path := writeTempFile(t, "coder.yaml", yaml)

	role, err := LoadRoleFrom(path)
	if err != nil {
		t.Fatalf("LoadRoleFrom: %v", err)
	}

	if role.Name != "coder" {
		t.Errorf("Name = %q, want %q", role.Name, "coder")
	}
	if role.Model != "" {
		t.Errorf("Model = %q, want empty", role.Model)
	}
	if role.Permissions.Agent != nil {
		t.Error("Agent should be nil for minimal role")
	}
}

func TestLoadRoleFrom_ValidationError(t *testing.T) {
	// Missing name.
	yaml := `
instructions: |
  Some instructions.
`
	path := writeTempFile(t, "bad.yaml", yaml)
	_, err := LoadRoleFrom(path)
	if err == nil {
		t.Fatal("expected error for missing name")
	}

	// Missing instructions.
	yaml2 := `name: test`
	path2 := writeTempFile(t, "bad2.yaml", yaml2)
	_, err2 := LoadRoleFrom(path2)
	if err2 == nil {
		t.Fatal("expected error for missing instructions")
	}
}

func TestPermissionAgent_IsEnabled(t *testing.T) {
	// Explicit enabled: true
	tr := true
	pa := &PermissionAgent{Enabled: &tr, Instructions: "test"}
	if !pa.IsEnabled() {
		t.Error("should be enabled when Enabled=true")
	}

	// Explicit enabled: false
	fa := false
	pa2 := &PermissionAgent{Enabled: &fa, Instructions: "test"}
	if pa2.IsEnabled() {
		t.Error("should be disabled when Enabled=false")
	}

	// Implicit: instructions present → enabled
	pa3 := &PermissionAgent{Instructions: "test"}
	if !pa3.IsEnabled() {
		t.Error("should be enabled when instructions present")
	}

	// Implicit: no instructions → disabled
	pa4 := &PermissionAgent{}
	if pa4.IsEnabled() {
		t.Error("should be disabled when no instructions")
	}
}

func TestListRoles(t *testing.T) {
	dir := t.TempDir()
	rolesDir := filepath.Join(dir, "roles")
	os.MkdirAll(rolesDir, 0o755)

	// Write two valid role files.
	os.WriteFile(filepath.Join(rolesDir, "architect.yaml"), []byte(`
name: architect
instructions: |
  Architect agent.
`), 0o644)

	os.WriteFile(filepath.Join(rolesDir, "coder.yaml"), []byte(`
name: coder
instructions: |
  Coder agent.
`), 0o644)

	// Write a non-yaml file (should be skipped).
	os.WriteFile(filepath.Join(rolesDir, "README.md"), []byte("# Roles"), 0o644)

	// Override RolesDir by testing LoadRoleFrom directly.
	entries, err := os.ReadDir(rolesDir)
	if err != nil {
		t.Fatal(err)
	}

	var roles []*Role
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		role, err := LoadRoleFrom(filepath.Join(rolesDir, entry.Name()))
		if err != nil {
			continue
		}
		roles = append(roles, role)
	}

	if len(roles) != 2 {
		t.Fatalf("got %d roles, want 2", len(roles))
	}
}

func TestSetupSessionDir(t *testing.T) {
	// Override the config dir for this test.
	origHome := os.Getenv("HOME")
	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	role := &Role{
		Name:  "architect",
		Model: "opus",
		Instructions: "You are an architect agent.\nDesign systems.\n",
		Permissions: Permissions{
			Allow: []string{"Read", "Glob", "Write(docs/**)"},
			Deny:  []string{"Bash(rm -rf *)"},
			Agent: &PermissionAgent{
				Instructions: "Review permissions for architect.\nALLOW: read-only\n",
			},
		},
	}

	sessionDir, err := SetupSessionDir("arch-1", role)
	if err != nil {
		t.Fatalf("SetupSessionDir: %v", err)
	}

	// Check session dir was created.
	if _, err := os.Stat(sessionDir); os.IsNotExist(err) {
		t.Fatal("session dir should exist")
	}

	// Check permission-reviewer.md was created.
	reviewerData, err := os.ReadFile(filepath.Join(sessionDir, "permission-reviewer.md"))
	if err != nil {
		t.Fatalf("read permission-reviewer.md: %v", err)
	}
	if string(reviewerData) != role.Permissions.Agent.Instructions {
		t.Errorf("permission-reviewer.md content = %q, want %q", string(reviewerData), role.Permissions.Agent.Instructions)
	}

	// No .claude subdir should be created.
	if _, err := os.Stat(filepath.Join(sessionDir, ".claude")); !os.IsNotExist(err) {
		t.Error(".claude subdir should not exist in session dir")
	}
}

func TestEnsureClaudeConfigDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "claude-config")

	if err := EnsureClaudeConfigDir(dir); err != nil {
		t.Fatalf("EnsureClaudeConfigDir: %v", err)
	}

	// Check settings.json was created with hooks.
	settingsData, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(settingsData, &settings); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks not found in settings.json")
	}
	if _, ok := hooks["PreToolUse"]; !ok {
		t.Error("PreToolUse hook not found")
	}
	if _, ok := hooks["PermissionRequest"]; !ok {
		t.Error("PermissionRequest hook not found")
	}

	// Calling again should not overwrite existing settings.json.
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"custom": true}`), 0o644)
	if err := EnsureClaudeConfigDir(dir); err != nil {
		t.Fatalf("EnsureClaudeConfigDir (2nd call): %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(data) != `{"custom": true}` {
		t.Error("settings.json should not be overwritten on second call")
	}
}

func TestSetupSessionDir_NoAgent(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	role := &Role{
		Name:         "coder",
		Instructions: "Code stuff.\n",
		Permissions: Permissions{
			Allow: []string{"Read", "Bash"},
		},
	}

	sessionDir, err := SetupSessionDir("coder-1", role)
	if err != nil {
		t.Fatalf("SetupSessionDir: %v", err)
	}

	// permission-reviewer.md should NOT exist.
	if _, err := os.Stat(filepath.Join(sessionDir, "permission-reviewer.md")); !os.IsNotExist(err) {
		t.Error("permission-reviewer.md should not exist when no agent configured")
	}
}

func TestIsClaudeConfigAuthenticated(t *testing.T) {
	tests := []struct {
		name           string
		claudeJSON     string
		want           bool
		wantErr        bool
	}{
		{
			name: "authenticated with oauthAccount",
			claudeJSON: `{
				"userID": "test-user-id",
				"oauthAccount": {
					"accountUuid": "test-uuid",
					"emailAddress": "test@example.com"
				}
			}`,
			want:    true,
			wantErr: false,
		},
		{
			name: "not authenticated - no oauthAccount",
			claudeJSON: `{
				"userID": "test-user-id"
			}`,
			want:    false,
			wantErr: false,
		},
		{
			name: "not authenticated - empty oauthAccount",
			claudeJSON: `{
				"userID": "test-user-id",
				"oauthAccount": {}
			}`,
			want:    false,
			wantErr: false,
		},
		{
			name: "not authenticated - missing fields",
			claudeJSON: `{
				"userID": "test-user-id",
				"oauthAccount": {
					"accountUuid": "test-uuid"
				}
			}`,
			want:    false,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.claudeJSON != "" {
				claudeJSONPath := filepath.Join(dir, ".claude.json")
				if err := os.WriteFile(claudeJSONPath, []byte(tt.claudeJSON), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			got, err := IsClaudeConfigAuthenticated(dir)
			if (err != nil) != tt.wantErr {
				t.Errorf("IsClaudeConfigAuthenticated() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("IsClaudeConfigAuthenticated() = %v, want %v", got, tt.want)
			}
		})
	}

	// Test missing .claude.json
	t.Run("not authenticated - no file", func(t *testing.T) {
		dir := t.TempDir()
		got, err := IsClaudeConfigAuthenticated(dir)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if got {
			t.Error("should not be authenticated when .claude.json doesn't exist")
		}
	})
}

func TestRole_GetClaudeConfigDir(t *testing.T) {
	// Save and restore HOME env var.
	origHome := os.Getenv("HOME")
	defer os.Setenv("HOME", origHome)
	os.Setenv("HOME", "/Users/testuser")

	tests := []struct {
		name            string
		claudeConfigDir string
		want            string
	}{
		{
			name:            "default when not specified",
			claudeConfigDir: "",
			want:            "/Users/testuser/.h2/claude-config/default",
		},
		{
			name:            "absolute path",
			claudeConfigDir: "/custom/path/to/config",
			want:            "/custom/path/to/config",
		},
		{
			name:            "tilde expansion",
			claudeConfigDir: "~/my-claude-config",
			want:            "/Users/testuser/my-claude-config",
		},
		{
			name:            "relative path within h2",
			claudeConfigDir: "/Users/testuser/.h2/claude-config/custom",
			want:            "/Users/testuser/.h2/claude-config/custom",
		},
		{
			name:            "tilde only means system default",
			claudeConfigDir: "~/",
			want:            "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role := &Role{
				Name:            "test",
				ClaudeConfigDir: tt.claudeConfigDir,
				Instructions:    "test",
			}
			got := role.GetClaudeConfigDir()
			if got != tt.want {
				t.Errorf("GetClaudeConfigDir() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadRoleFrom_WithKeepalive(t *testing.T) {
	yaml := `
name: scheduler
instructions: |
  You are a scheduler agent.
keepalive:
  idle_timeout: 30s
  message: "Check bd ready for new tasks to assign."
  condition: "bd ready -q"
`
	path := writeTempFile(t, "scheduler.yaml", yaml)

	role, err := LoadRoleFrom(path)
	if err != nil {
		t.Fatalf("LoadRoleFrom: %v", err)
	}

	if role.Keepalive == nil {
		t.Fatal("Keepalive should not be nil")
	}
	if role.Keepalive.IdleTimeout != "30s" {
		t.Errorf("IdleTimeout = %q, want %q", role.Keepalive.IdleTimeout, "30s")
	}
	if role.Keepalive.Message != "Check bd ready for new tasks to assign." {
		t.Errorf("Message = %q, want %q", role.Keepalive.Message, "Check bd ready for new tasks to assign.")
	}
	if role.Keepalive.Condition != "bd ready -q" {
		t.Errorf("Condition = %q, want %q", role.Keepalive.Condition, "bd ready -q")
	}
}

func TestLoadRoleFrom_KeepaliveOptional(t *testing.T) {
	yaml := `
name: simple
instructions: |
  A simple agent.
`
	path := writeTempFile(t, "simple.yaml", yaml)

	role, err := LoadRoleFrom(path)
	if err != nil {
		t.Fatalf("LoadRoleFrom: %v", err)
	}

	if role.Keepalive != nil {
		t.Error("Keepalive should be nil when not specified")
	}
}

func TestKeepaliveConfig_ParseIdleTimeout(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid seconds", "30s", false},
		{"valid minutes", "5m", false},
		{"valid mixed", "1m30s", false},
		{"valid milliseconds", "500ms", false},
		{"invalid", "abc", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := &KeepaliveConfig{IdleTimeout: tt.input}
			_, err := k.ParseIdleTimeout()
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseIdleTimeout(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}

	// Verify actual parsed value.
	k := &KeepaliveConfig{IdleTimeout: "30s"}
	d, _ := k.ParseIdleTimeout()
	if d != 30*1e9 { // 30 seconds in nanoseconds
		t.Errorf("parsed duration = %v, want 30s", d)
	}
}

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
