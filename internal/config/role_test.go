package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2/internal/tmpl"
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

func TestLoadRoleFrom_WithHeartbeat(t *testing.T) {
	yaml := `
name: scheduler
instructions: |
  You are a scheduler agent.
heartbeat:
  idle_timeout: 30s
  message: "Check bd ready for new tasks to assign."
  condition: "bd ready -q"
`
	path := writeTempFile(t, "scheduler.yaml", yaml)

	role, err := LoadRoleFrom(path)
	if err != nil {
		t.Fatalf("LoadRoleFrom: %v", err)
	}

	if role.Heartbeat == nil {
		t.Fatal("Heartbeat should not be nil")
	}
	if role.Heartbeat.IdleTimeout != "30s" {
		t.Errorf("IdleTimeout = %q, want %q", role.Heartbeat.IdleTimeout, "30s")
	}
	if role.Heartbeat.Message != "Check bd ready for new tasks to assign." {
		t.Errorf("Message = %q, want %q", role.Heartbeat.Message, "Check bd ready for new tasks to assign.")
	}
	if role.Heartbeat.Condition != "bd ready -q" {
		t.Errorf("Condition = %q, want %q", role.Heartbeat.Condition, "bd ready -q")
	}
}

func TestLoadRoleFrom_HeartbeatOptional(t *testing.T) {
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

	if role.Heartbeat != nil {
		t.Error("Heartbeat should be nil when not specified")
	}
}

func TestHeartbeatConfig_ParseIdleTimeout(t *testing.T) {
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
			k := &HeartbeatConfig{IdleTimeout: tt.input}
			_, err := k.ParseIdleTimeout()
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseIdleTimeout(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}

	// Verify actual parsed value.
	k := &HeartbeatConfig{IdleTimeout: "30s"}
	d, _ := k.ParseIdleTimeout()
	if d != 30*1e9 { // 30 seconds in nanoseconds
		t.Errorf("parsed duration = %v, want 30s", d)
	}
}

func TestResolveWorkingDir_Default(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test"}
	got, err := role.ResolveWorkingDir("/my/cwd")
	if err != nil {
		t.Fatalf("ResolveWorkingDir: %v", err)
	}
	if got != "/my/cwd" {
		t.Errorf("ResolveWorkingDir() = %q, want %q", got, "/my/cwd")
	}
}

func TestResolveWorkingDir_Dot(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test", WorkingDir: "."}
	got, err := role.ResolveWorkingDir("/my/cwd")
	if err != nil {
		t.Fatalf("ResolveWorkingDir: %v", err)
	}
	if got != "/my/cwd" {
		t.Errorf("ResolveWorkingDir(\".\") = %q, want %q", got, "/my/cwd")
	}
}

func TestResolveWorkingDir_Absolute(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test", WorkingDir: "/some/absolute/path"}
	got, err := role.ResolveWorkingDir("/my/cwd")
	if err != nil {
		t.Fatalf("ResolveWorkingDir: %v", err)
	}
	if got != "/some/absolute/path" {
		t.Errorf("ResolveWorkingDir(abs) = %q, want %q", got, "/some/absolute/path")
	}
}

func TestResolveWorkingDir_Relative(t *testing.T) {
	ResetResolveCache()
	defer ResetResolveCache()

	// Create a valid h2 dir so ResolveDir succeeds.
	h2Dir := t.TempDir()
	WriteMarker(h2Dir)
	t.Setenv("H2_DIR", h2Dir)

	role := &Role{Name: "test", Instructions: "test", WorkingDir: "projects/myapp"}
	got, err := role.ResolveWorkingDir("/my/cwd")
	if err != nil {
		t.Fatalf("ResolveWorkingDir: %v", err)
	}
	want := filepath.Join(h2Dir, "projects/myapp")
	if got != want {
		t.Errorf("ResolveWorkingDir(rel) = %q, want %q", got, want)
	}
}

func TestResolveWorkingDir_FromYAML(t *testing.T) {
	yaml := `
name: worker
instructions: |
  A worker agent.
working_dir: /workspace/project
`
	path := writeTempFile(t, "worker.yaml", yaml)
	role, err := LoadRoleFrom(path)
	if err != nil {
		t.Fatalf("LoadRoleFrom: %v", err)
	}
	if role.WorkingDir != "/workspace/project" {
		t.Errorf("WorkingDir = %q, want %q", role.WorkingDir, "/workspace/project")
	}
}

func TestValidate_WorktreeAndWorkingDirMutualExclusivity(t *testing.T) {
	// worktree + non-trivial working_dir should fail.
	role := &Role{
		Name:         "test",
		Instructions: "test",
		WorkingDir:   "projects/myapp",
		Worktree:     &WorktreeConfig{ProjectDir: "/tmp/repo", Name: "test-wt"},
	}
	err := role.Validate()
	if err == nil {
		t.Fatal("expected error for worktree + working_dir")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %q, want it to contain 'mutually exclusive'", err.Error())
	}

	// worktree + working_dir="." should be OK.
	role2 := &Role{
		Name:         "test",
		Instructions: "test",
		WorkingDir:   ".",
		Worktree:     &WorktreeConfig{ProjectDir: "/tmp/repo", Name: "test-wt"},
	}
	if err := role2.Validate(); err != nil {
		t.Errorf("worktree + working_dir='.' should be allowed: %v", err)
	}

	// worktree + empty working_dir should be OK.
	role3 := &Role{
		Name:         "test",
		Instructions: "test",
		Worktree:     &WorktreeConfig{ProjectDir: "/tmp/repo", Name: "test-wt"},
	}
	if err := role3.Validate(); err != nil {
		t.Errorf("worktree + empty working_dir should be allowed: %v", err)
	}
}

func TestValidate_WorktreeMissingProjectDir(t *testing.T) {
	role := &Role{
		Name:         "test",
		Instructions: "test",
		Worktree:     &WorktreeConfig{Name: "test-wt"},
	}
	err := role.Validate()
	if err == nil {
		t.Fatal("expected error for missing project_dir")
	}
	if !strings.Contains(err.Error(), "project_dir") {
		t.Errorf("error = %q, want it to contain 'project_dir'", err.Error())
	}
}

func TestValidate_WorktreeMissingName(t *testing.T) {
	role := &Role{
		Name:         "test",
		Instructions: "test",
		Worktree:     &WorktreeConfig{ProjectDir: "/tmp/repo"},
	}
	err := role.Validate()
	if err == nil {
		t.Fatal("expected error for missing worktree name")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error = %q, want it to contain 'name'", err.Error())
	}
}

// --- LoadRoleRendered tests ---

func TestLoadRoleRenderedFrom_BasicRendering(t *testing.T) {
	yamlContent := `
name: coder
variables:
  team:
    description: "Team name"
  env:
    description: "Environment"
    default: "dev"
instructions: |
  You are {{ .AgentName }} on team {{ .Var.team }} in {{ .Var.env }}.
`
	path := writeTempFile(t, "coder.yaml", yamlContent)
	ctx := &tmpl.Context{
		AgentName: "coder-1",
		Var:       map[string]string{"team": "backend"},
	}

	role, err := LoadRoleRenderedFrom(path, ctx)
	if err != nil {
		t.Fatalf("LoadRoleRenderedFrom: %v", err)
	}

	if !strings.Contains(role.Instructions, "coder-1") {
		t.Errorf("Instructions should contain AgentName, got: %s", role.Instructions)
	}
	if !strings.Contains(role.Instructions, "backend") {
		t.Errorf("Instructions should contain team var, got: %s", role.Instructions)
	}
	if !strings.Contains(role.Instructions, "dev") {
		t.Errorf("Instructions should contain default env, got: %s", role.Instructions)
	}
}

func TestLoadRoleRenderedFrom_WorktreeRendering(t *testing.T) {
	yamlContent := `
name: coder
instructions: |
  Work on ticket.
worktree:
  project_dir: /tmp/repo
  name: "{{ .AgentName }}-wt"
  branch_name: "feature/{{ .Var.ticket }}"
`
	path := writeTempFile(t, "worktree.yaml", yamlContent)
	ctx := &tmpl.Context{
		AgentName: "coder-1",
		Var:       map[string]string{"ticket": "123"},
	}

	role, err := LoadRoleRenderedFrom(path, ctx)
	if err != nil {
		t.Fatalf("LoadRoleRenderedFrom: %v", err)
	}

	if role.Worktree == nil {
		t.Fatal("Worktree should not be nil")
	}
	if role.Worktree.Name != "coder-1-wt" {
		t.Errorf("Worktree.Name = %q, want %q", role.Worktree.Name, "coder-1-wt")
	}
	if role.Worktree.BranchName != "feature/123" {
		t.Errorf("Worktree.BranchName = %q, want %q", role.Worktree.BranchName, "feature/123")
	}
}

func TestLoadRoleRenderedFrom_WorkingDirRendering(t *testing.T) {
	yamlContent := `
name: coder
instructions: |
  Work on project.
working_dir: "/projects/{{ .Var.project }}"
`
	path := writeTempFile(t, "workdir.yaml", yamlContent)
	ctx := &tmpl.Context{Var: map[string]string{"project": "h2"}}

	role, err := LoadRoleRenderedFrom(path, ctx)
	if err != nil {
		t.Fatalf("LoadRoleRenderedFrom: %v", err)
	}

	if role.WorkingDir != "/projects/h2" {
		t.Errorf("WorkingDir = %q, want %q", role.WorkingDir, "/projects/h2")
	}
}

func TestLoadRoleRenderedFrom_ModelRendering(t *testing.T) {
	yamlContent := `
name: coder
instructions: |
  Code.
model: "{{ .Var.model }}"
`
	path := writeTempFile(t, "model.yaml", yamlContent)
	ctx := &tmpl.Context{Var: map[string]string{"model": "haiku"}}

	role, err := LoadRoleRenderedFrom(path, ctx)
	if err != nil {
		t.Fatalf("LoadRoleRenderedFrom: %v", err)
	}

	if role.Model != "haiku" {
		t.Errorf("Model = %q, want %q", role.Model, "haiku")
	}
}

func TestLoadRoleRenderedFrom_HeartbeatRendering(t *testing.T) {
	yamlContent := `
name: scheduler
instructions: |
  Schedule.
heartbeat:
  idle_timeout: 30s
  message: "Hey {{ .AgentName }}"
`
	path := writeTempFile(t, "heartbeat.yaml", yamlContent)
	ctx := &tmpl.Context{AgentName: "scheduler-1"}

	role, err := LoadRoleRenderedFrom(path, ctx)
	if err != nil {
		t.Fatalf("LoadRoleRenderedFrom: %v", err)
	}

	if role.Heartbeat == nil {
		t.Fatal("Heartbeat should not be nil")
	}
	if role.Heartbeat.Message != "Hey scheduler-1" {
		t.Errorf("Heartbeat.Message = %q, want %q", role.Heartbeat.Message, "Hey scheduler-1")
	}
}

func TestLoadRoleRenderedFrom_RequiredVarMissing(t *testing.T) {
	yamlContent := `
name: coder
variables:
  team:
    description: "Team name"
instructions: |
  Team: {{ .Var.team }}.
`
	path := writeTempFile(t, "reqvar.yaml", yamlContent)
	ctx := &tmpl.Context{Var: map[string]string{}}

	_, err := LoadRoleRenderedFrom(path, ctx)
	if err == nil {
		t.Fatal("expected error for missing required variable")
	}
	if !strings.Contains(err.Error(), "team") {
		t.Errorf("error should mention 'team', got: %v", err)
	}
}

func TestLoadRoleRenderedFrom_RequiredVarProvided(t *testing.T) {
	yamlContent := `
name: coder
variables:
  team:
    description: "Team name"
instructions: |
  Team: {{ .Var.team }}.
`
	path := writeTempFile(t, "reqvar2.yaml", yamlContent)
	ctx := &tmpl.Context{Var: map[string]string{"team": "backend"}}

	role, err := LoadRoleRenderedFrom(path, ctx)
	if err != nil {
		t.Fatalf("LoadRoleRenderedFrom: %v", err)
	}
	if !strings.Contains(role.Instructions, "backend") {
		t.Errorf("Instructions should contain 'backend', got: %s", role.Instructions)
	}
}

func TestLoadRoleRenderedFrom_NilContext(t *testing.T) {
	yamlContent := `
name: coder
instructions: |
  Hello {{ .AgentName }}.
`
	path := writeTempFile(t, "nilctx.yaml", yamlContent)

	role, err := LoadRoleRenderedFrom(path, nil)
	if err != nil {
		t.Fatalf("LoadRoleRenderedFrom: %v", err)
	}
	// With nil context, template expressions are left as-is (no rendering).
	if !strings.Contains(role.Instructions, "{{ .AgentName }}") {
		t.Errorf("With nil ctx, instructions should contain raw template, got: %s", role.Instructions)
	}
}

func TestLoadRoleRenderedFrom_BackwardCompat(t *testing.T) {
	// Role with no template expressions and no variables section.
	yamlContent := `
name: simple
instructions: |
  A simple static role.
`
	path := writeTempFile(t, "static.yaml", yamlContent)
	ctx := &tmpl.Context{AgentName: "agent-1"}

	role, err := LoadRoleRenderedFrom(path, ctx)
	if err != nil {
		t.Fatalf("LoadRoleRenderedFrom: %v", err)
	}
	if role.Name != "simple" {
		t.Errorf("Name = %q, want %q", role.Name, "simple")
	}
	if !strings.Contains(role.Instructions, "simple static role") {
		t.Errorf("Instructions should be unchanged, got: %s", role.Instructions)
	}
}

func TestLoadRoleRenderedFrom_Conditionals(t *testing.T) {
	yamlContent := `
name: coder
instructions: |
  You are {{ .AgentName }}.
  {{ if .PodName }}You are in pod {{ .PodName }}.{{ else }}Standalone.{{ end }}
`
	path := writeTempFile(t, "cond.yaml", yamlContent)

	// With pod context.
	ctx := &tmpl.Context{AgentName: "coder-1", PodName: "backend"}
	role, err := LoadRoleRenderedFrom(path, ctx)
	if err != nil {
		t.Fatalf("LoadRoleRenderedFrom: %v", err)
	}
	if !strings.Contains(role.Instructions, "pod backend") {
		t.Errorf("should contain pod name, got: %s", role.Instructions)
	}

	// Without pod context (standalone).
	ctx2 := &tmpl.Context{AgentName: "coder-1"}
	role2, err := LoadRoleRenderedFrom(path, ctx2)
	if err != nil {
		t.Fatalf("LoadRoleRenderedFrom: %v", err)
	}
	if !strings.Contains(role2.Instructions, "Standalone") {
		t.Errorf("should contain 'Standalone', got: %s", role2.Instructions)
	}
}

func TestLoadRoleRenderedFrom_StandaloneZeroValues(t *testing.T) {
	yamlContent := `
name: pod-aware
instructions: |
  Index: {{ .Index }}, Count: {{ .Count }}.
  {{ if .PodName }}In pod.{{ else }}Not in pod.{{ end }}
`
	path := writeTempFile(t, "podaware.yaml", yamlContent)
	ctx := &tmpl.Context{} // standalone: all zero values

	role, err := LoadRoleRenderedFrom(path, ctx)
	if err != nil {
		t.Fatalf("LoadRoleRenderedFrom: %v", err)
	}
	if !strings.Contains(role.Instructions, "Index: 0") {
		t.Errorf("Index should be 0, got: %s", role.Instructions)
	}
	if !strings.Contains(role.Instructions, "Count: 0") {
		t.Errorf("Count should be 0, got: %s", role.Instructions)
	}
	if !strings.Contains(role.Instructions, "Not in pod") {
		t.Errorf("PodName should be empty (not in pod), got: %s", role.Instructions)
	}
}

func TestLoadRoleRenderedFrom_VariablesFieldPopulated(t *testing.T) {
	yamlContent := `
name: coder
variables:
  team:
    description: "Team name"
  env:
    description: "Env"
    default: "dev"
instructions: |
  Team {{ .Var.team }} env {{ .Var.env }}.
`
	path := writeTempFile(t, "vars.yaml", yamlContent)
	ctx := &tmpl.Context{Var: map[string]string{"team": "backend"}}

	role, err := LoadRoleRenderedFrom(path, ctx)
	if err != nil {
		t.Fatalf("LoadRoleRenderedFrom: %v", err)
	}

	if len(role.Variables) != 2 {
		t.Fatalf("Variables count = %d, want 2", len(role.Variables))
	}
	if !role.Variables["team"].Required() {
		t.Error("team should be required")
	}
	if role.Variables["env"].Required() {
		t.Error("env should be optional")
	}
}

// --- Section 6.3: Override Interaction ---

func TestOverrideBeatsTemplateRenderedValue(t *testing.T) {
	// Template renders working_dir to /foo, override sets it to /bar.
	yamlContent := `
name: coder
instructions: |
  Work.
working_dir: "/projects/{{ .Var.project }}"
model: "{{ .Var.model }}"
`
	path := writeTempFile(t, "override.yaml", yamlContent)
	ctx := &tmpl.Context{Var: map[string]string{"project": "foo", "model": "opus"}}

	role, err := LoadRoleRenderedFrom(path, ctx)
	if err != nil {
		t.Fatalf("LoadRoleRenderedFrom: %v", err)
	}

	// Verify template rendered values first.
	if role.WorkingDir != "/projects/foo" {
		t.Fatalf("pre-override WorkingDir = %q, want %q", role.WorkingDir, "/projects/foo")
	}
	if role.Model != "opus" {
		t.Fatalf("pre-override Model = %q, want %q", role.Model, "opus")
	}

	// Apply overrides — these should win over template-rendered values.
	err = ApplyOverrides(role, []string{"working_dir=/bar", "model=haiku"})
	if err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}

	if role.WorkingDir != "/bar" {
		t.Errorf("post-override WorkingDir = %q, want %q", role.WorkingDir, "/bar")
	}
	if role.Model != "haiku" {
		t.Errorf("post-override Model = %q, want %q", role.Model, "haiku")
	}
}

// --- Section 6.4: ListRoles with Templated Roles ---

func TestListRoles_WithTemplatedRoles(t *testing.T) {
	dir := t.TempDir()
	rolesDir := filepath.Join(dir, "roles")
	os.MkdirAll(rolesDir, 0o755)

	// Write a static role.
	os.WriteFile(filepath.Join(rolesDir, "static.yaml"), []byte(`
name: static
instructions: |
  A static agent.
`), 0o644)

	// Write a templated role with {{ }} expressions.
	os.WriteFile(filepath.Join(rolesDir, "templated.yaml"), []byte(`
name: templated
instructions: |
  You are {{ .AgentName }} on team {{ .Var.team }}.
`), 0o644)

	// Write a templated role with variables section.
	os.WriteFile(filepath.Join(rolesDir, "parameterized.yaml"), []byte(`
name: parameterized
variables:
  team:
    description: "Team"
instructions: |
  Team: {{ .Var.team }}.
`), 0o644)

	// Load all roles via LoadRoleFrom (like ListRoles does).
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
			// ListRoles skips invalid files — this should NOT happen for templated roles.
			t.Errorf("LoadRoleFrom(%s) failed: %v", entry.Name(), err)
			continue
		}
		roles = append(roles, role)
	}

	if len(roles) != 3 {
		t.Fatalf("got %d roles, want 3", len(roles))
	}

	// Verify templated role has raw template expressions in instructions.
	for _, role := range roles {
		if role.Name == "templated" {
			if !strings.Contains(role.Instructions, "{{ .AgentName }}") {
				t.Error("templated role instructions should contain raw {{ .AgentName }}")
			}
		}
		if role.Name == "parameterized" {
			if !strings.Contains(role.Instructions, "{{ .Var.team }}") {
				t.Error("parameterized role instructions should contain raw {{ .Var.team }}")
			}
		}
	}
}

// --- Section 9: E2E Integration Tests with testdata fixtures ---

func TestE2E_ParameterizedRole(t *testing.T) {
	// Section 9.1: Load parameterized.yaml from testdata, render with vars.
	path := filepath.Join("testdata", "roles", "parameterized.yaml")
	ctx := &tmpl.Context{
		AgentName: "coder-1",
		Var:       map[string]string{"team": "backend"},
	}

	role, err := LoadRoleRenderedFrom(path, ctx)
	if err != nil {
		t.Fatalf("LoadRoleRenderedFrom: %v", err)
	}

	if !strings.Contains(role.Instructions, "backend") {
		t.Errorf("instructions should contain 'backend', got: %s", role.Instructions)
	}
	if !strings.Contains(role.Instructions, "dev") {
		t.Errorf("instructions should contain default env 'dev', got: %s", role.Instructions)
	}
}

func TestE2E_ParameterizedRole_MissingVar(t *testing.T) {
	// Section 9.2: Load parameterized.yaml, missing required var.
	path := filepath.Join("testdata", "roles", "parameterized.yaml")
	ctx := &tmpl.Context{Var: map[string]string{}}

	_, err := LoadRoleRenderedFrom(path, ctx)
	if err == nil {
		t.Fatal("expected error for missing required variable")
	}
	if !strings.Contains(err.Error(), "team") {
		t.Errorf("error should mention 'team', got: %v", err)
	}
	if !strings.Contains(err.Error(), "--var") {
		t.Errorf("error should contain --var hint, got: %v", err)
	}
}

func TestE2E_StaticRole_BackwardCompat(t *testing.T) {
	// Section 9.5: Static role loaded with LoadRoleRendered is identical to LoadRoleFrom.
	path := filepath.Join("testdata", "roles", "static.yaml")

	roleRendered, err := LoadRoleRenderedFrom(path, &tmpl.Context{AgentName: "test"})
	if err != nil {
		t.Fatalf("LoadRoleRenderedFrom: %v", err)
	}

	roleStatic, err := LoadRoleFrom(path)
	if err != nil {
		t.Fatalf("LoadRoleFrom: %v", err)
	}

	if roleRendered.Name != roleStatic.Name {
		t.Errorf("Name mismatch: rendered=%q, static=%q", roleRendered.Name, roleStatic.Name)
	}
	if roleRendered.Instructions != roleStatic.Instructions {
		t.Errorf("Instructions mismatch: rendered=%q, static=%q", roleRendered.Instructions, roleStatic.Instructions)
	}
	if roleRendered.Description != roleStatic.Description {
		t.Errorf("Description mismatch: rendered=%q, static=%q", roleRendered.Description, roleStatic.Description)
	}
}

func TestE2E_PodAwareRole_StandaloneZeroValues(t *testing.T) {
	// Section 9.6: Pod-aware role rendered with standalone (zero-value) context.
	path := filepath.Join("testdata", "roles", "pod-aware.yaml")
	ctx := &tmpl.Context{AgentName: "solo-agent"} // no pod context

	role, err := LoadRoleRenderedFrom(path, ctx)
	if err != nil {
		t.Fatalf("LoadRoleRenderedFrom: %v", err)
	}

	if !strings.Contains(role.Instructions, "solo-agent") {
		t.Errorf("should contain agent name, got: %s", role.Instructions)
	}
	if !strings.Contains(role.Instructions, "Index: 0") {
		t.Errorf("Index should be 0, got: %s", role.Instructions)
	}
	if !strings.Contains(role.Instructions, "Count: 0") {
		t.Errorf("Count should be 0, got: %s", role.Instructions)
	}
	if !strings.Contains(role.Instructions, "Not in a pod") {
		t.Errorf("should contain 'Not in a pod', got: %s", role.Instructions)
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
