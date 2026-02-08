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

	// Check CLAUDE.md was created.
	claudeMD, err := os.ReadFile(filepath.Join(sessionDir, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	if string(claudeMD) != role.Instructions {
		t.Errorf("CLAUDE.md content = %q, want %q", string(claudeMD), role.Instructions)
	}

	// Check settings.json was created and has expected structure.
	settingsData, err := os.ReadFile(filepath.Join(sessionDir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(settingsData, &settings); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}

	// Check model.
	if settings["model"] != "opus" {
		t.Errorf("model = %v, want opus", settings["model"])
	}

	// Check permissions.
	perms, ok := settings["permissions"].(map[string]any)
	if !ok {
		t.Fatal("permissions not found in settings.json")
	}
	allow, ok := perms["allow"].([]any)
	if !ok || len(allow) != 3 {
		t.Errorf("allow = %v, want 3 entries", perms["allow"])
	}
	deny, ok := perms["deny"].([]any)
	if !ok || len(deny) != 1 {
		t.Errorf("deny = %v, want 1 entry", perms["deny"])
	}

	// Check hooks exist.
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

	// Check permission-reviewer.md was created.
	reviewerData, err := os.ReadFile(filepath.Join(sessionDir, "permission-reviewer.md"))
	if err != nil {
		t.Fatalf("read permission-reviewer.md: %v", err)
	}
	if string(reviewerData) != role.Permissions.Agent.Instructions {
		t.Errorf("permission-reviewer.md content = %q, want %q", string(reviewerData), role.Permissions.Agent.Instructions)
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

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
