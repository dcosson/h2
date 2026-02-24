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
role_name: architect
description: "Designs systems"
agent_model: opus
instructions: |
  You are an architect agent.
  Design system architecture.
permission_review_agent:
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

	if role.RoleName != "architect" {
		t.Errorf("RoleName = %q, want %q", role.RoleName, "architect")
	}
	if role.Description != "Designs systems" {
		t.Errorf("Description = %q, want %q", role.Description, "Designs systems")
	}
	if role.GetModel() != "opus" {
		t.Errorf("GetModel() = %q, want %q", role.GetModel(), "opus")
	}
	if role.PermissionReviewAgent == nil {
		t.Fatal("PermissionReviewAgent is nil")
	}
	if !role.PermissionReviewAgent.IsEnabled() {
		t.Error("PermissionReviewAgent should be enabled")
	}
	if role.PermissionReviewAgent.Instructions == "" {
		t.Error("PermissionReviewAgent instructions should not be empty")
	}
}

func TestLoadRoleFrom_MinimalRole(t *testing.T) {
	yaml := `
role_name: coder
instructions: |
  You are a coding agent.
`
	path := writeTempFile(t, "coder.yaml", yaml)

	role, err := LoadRoleFrom(path)
	if err != nil {
		t.Fatalf("LoadRoleFrom: %v", err)
	}

	if role.RoleName != "coder" {
		t.Errorf("RoleName = %q, want %q", role.RoleName, "coder")
	}
	if role.GetModel() != "" {
		t.Errorf("GetModel() = %q, want empty", role.GetModel())
	}
	if role.PermissionReviewAgent != nil {
		t.Error("PermissionReviewAgent should be nil for minimal role")
	}
}

func TestLoadRoleFrom_ValidationError(t *testing.T) {
	// Missing role_name.
	yaml := `
instructions: |
  Some instructions.
`
	path := writeTempFile(t, "bad.yaml", yaml)
	_, err := LoadRoleFrom(path)
	if err == nil {
		t.Fatal("expected error for missing role_name")
	}
}

func TestPermissionReviewAgent_IsEnabled(t *testing.T) {
	// Explicit enabled: true
	tr := true
	pa := &PermissionReviewAgent{Enabled: &tr, Instructions: "test"}
	if !pa.IsEnabled() {
		t.Error("should be enabled when Enabled=true")
	}

	// Explicit enabled: false
	fa := false
	pa2 := &PermissionReviewAgent{Enabled: &fa, Instructions: "test"}
	if pa2.IsEnabled() {
		t.Error("should be disabled when Enabled=false")
	}

	// Implicit: instructions present → enabled
	pa3 := &PermissionReviewAgent{Instructions: "test"}
	if !pa3.IsEnabled() {
		t.Error("should be enabled when instructions present")
	}

	// Implicit: no instructions → disabled
	pa4 := &PermissionReviewAgent{}
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
role_name: architect
instructions: |
  Architect agent.
`), 0o644)

	os.WriteFile(filepath.Join(rolesDir, "coder.yaml"), []byte(`
role_name: coder
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
	setupFakeHome(t)

	role := &Role{
		RoleName:     "architect",
		AgentModel:   "opus",
		Instructions: "You are an architect agent.\nDesign systems.\n",
		PermissionReviewAgent: &PermissionReviewAgent{
			Instructions: "Review permissions for architect.\nALLOW: read-only\n",
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
	if string(reviewerData) != role.PermissionReviewAgent.Instructions {
		t.Errorf("permission-reviewer.md content = %q, want %q", string(reviewerData), role.PermissionReviewAgent.Instructions)
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
	setupFakeHome(t)

	role := &Role{
		RoleName:     "coder",
		Instructions: "Code stuff.\n",
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
		name       string
		claudeJSON string
		want       bool
		wantErr    bool
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
	ResetResolveCache()
	t.Cleanup(ResetResolveCache)

	// Create a real temp h2 dir so ResolveDir works (walk-up from CWD
	// would otherwise find the real h2 dir).
	h2Dir := t.TempDir()
	WriteMarker(h2Dir)
	t.Setenv("H2_DIR", h2Dir)

	// Use a fixed path for HOME so tilde expansion tests are deterministic.
	t.Setenv("HOME", "/Users/testuser")
	t.Setenv("H2_ROOT_DIR", "/Users/testuser/.h2")
	t.Setenv("H2_ACTOR", "")

	tests := []struct {
		name             string
		claudeConfigPath string
		want             string
	}{
		{
			name:             "default when not specified",
			claudeConfigPath: "",
			want:             filepath.Join(h2Dir, "claude-config", "default"),
		},
		{
			name:             "absolute path",
			claudeConfigPath: "/custom/path/to/config",
			want:             "/custom/path/to/config",
		},
		{
			name:             "tilde expansion",
			claudeConfigPath: "~/my-claude-config",
			want:             "/Users/testuser/my-claude-config",
		},
		{
			name:             "relative path within h2",
			claudeConfigPath: "/Users/testuser/.h2/claude-config/custom",
			want:             "/Users/testuser/.h2/claude-config/custom",
		},
		{
			name:             "tilde only means system default",
			claudeConfigPath: "~/",
			want:             "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role := &Role{
				RoleName:             "test",
				ClaudeCodeConfigPath: tt.claudeConfigPath,
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
role_name: scheduler
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
role_name: simple
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
	role := &Role{RoleName: "test"}
	got, err := role.ResolveWorkingDir("/my/cwd")
	if err != nil {
		t.Fatalf("ResolveWorkingDir: %v", err)
	}
	if got != "/my/cwd" {
		t.Errorf("ResolveWorkingDir() = %q, want %q", got, "/my/cwd")
	}
}

func TestResolveWorkingDir_Dot(t *testing.T) {
	role := &Role{RoleName: "test", WorkingDir: "."}
	got, err := role.ResolveWorkingDir("/my/cwd")
	if err != nil {
		t.Fatalf("ResolveWorkingDir: %v", err)
	}
	if got != "/my/cwd" {
		t.Errorf("ResolveWorkingDir(\".\") = %q, want %q", got, "/my/cwd")
	}
}

func TestResolveWorkingDir_Absolute(t *testing.T) {
	role := &Role{RoleName: "test", WorkingDir: "/some/absolute/path"}
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

	role := &Role{RoleName: "test", WorkingDir: "projects/myapp"}
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
role_name: worker
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
		RoleName:   "test",
		WorkingDir: "projects/myapp",
		Worktree:   &WorktreeConfig{ProjectDir: "/tmp/repo", Name: "test-wt"},
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
		RoleName:   "test",
		WorkingDir: ".",
		Worktree:   &WorktreeConfig{ProjectDir: "/tmp/repo", Name: "test-wt"},
	}
	if err := role2.Validate(); err != nil {
		t.Errorf("worktree + working_dir='.' should be allowed: %v", err)
	}

	// worktree + empty working_dir should be OK.
	role3 := &Role{
		RoleName: "test",
		Worktree: &WorktreeConfig{ProjectDir: "/tmp/repo", Name: "test-wt"},
	}
	if err := role3.Validate(); err != nil {
		t.Errorf("worktree + empty working_dir should be allowed: %v", err)
	}
}

func TestValidate_WorktreeMissingProjectDir(t *testing.T) {
	role := &Role{
		RoleName: "test",
		Worktree: &WorktreeConfig{Name: "test-wt"},
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
		RoleName: "test",
		Worktree: &WorktreeConfig{ProjectDir: "/tmp/repo"},
	}
	err := role.Validate()
	if err == nil {
		t.Fatal("expected error for missing worktree name")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error = %q, want it to contain 'name'", err.Error())
	}
}

func TestLoadRoleFrom_QuotedTemplateValues(t *testing.T) {
	// Quoted {{ }} values should be valid YAML and parse correctly.
	yaml := `
role_name: "{{ .RoleName }}"
claude_code_config_path: "{{ .H2Dir }}/claude-config/default"
instructions: |
  You are a {{ .RoleName }} agent.
`
	path := writeTempFile(t, "quoted.yaml", yaml)

	role, err := LoadRoleFrom(path)
	if err != nil {
		t.Fatalf("LoadRoleFrom: %v", err)
	}
	if role.RoleName != "{{ .RoleName }}" {
		t.Errorf("RoleName = %q, want %q", role.RoleName, "{{ .RoleName }}")
	}
	if role.ClaudeCodeConfigPath != "{{ .H2Dir }}/claude-config/default" {
		t.Errorf("ClaudeCodeConfigPath = %q, want %q", role.ClaudeCodeConfigPath, "{{ .H2Dir }}/claude-config/default")
	}
}

// --- LoadRoleRendered tests ---

func TestLoadRoleRenderedFrom_BasicRendering(t *testing.T) {
	yamlContent := `
role_name: coder
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
role_name: coder
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
role_name: coder
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
role_name: coder
instructions: |
  Code.
agent_model: "{{ .Var.model }}"
`
	path := writeTempFile(t, "model.yaml", yamlContent)
	ctx := &tmpl.Context{Var: map[string]string{"model": "haiku"}}

	role, err := LoadRoleRenderedFrom(path, ctx)
	if err != nil {
		t.Fatalf("LoadRoleRenderedFrom: %v", err)
	}

	if role.GetModel() != "haiku" {
		t.Errorf("GetModel() = %q, want %q", role.GetModel(), "haiku")
	}
}

func TestLoadRoleRenderedFrom_HeartbeatRendering(t *testing.T) {
	yamlContent := `
role_name: scheduler
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
role_name: coder
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
role_name: coder
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
role_name: coder
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
role_name: simple
instructions: |
  A simple static role.
`
	path := writeTempFile(t, "static.yaml", yamlContent)
	ctx := &tmpl.Context{AgentName: "agent-1"}

	role, err := LoadRoleRenderedFrom(path, ctx)
	if err != nil {
		t.Fatalf("LoadRoleRenderedFrom: %v", err)
	}
	if role.RoleName != "simple" {
		t.Errorf("RoleName = %q, want %q", role.RoleName, "simple")
	}
	if !strings.Contains(role.Instructions, "simple static role") {
		t.Errorf("Instructions should be unchanged, got: %s", role.Instructions)
	}
}

func TestLoadRoleRenderedFrom_Conditionals(t *testing.T) {
	yamlContent := `
role_name: coder
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
role_name: pod-aware
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
role_name: coder
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
role_name: coder
instructions: |
  Work.
working_dir: "/projects/{{ .Var.project }}"
agent_model: "{{ .Var.model }}"
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
	if role.GetModel() != "opus" {
		t.Fatalf("pre-override GetModel() = %q, want %q", role.GetModel(), "opus")
	}

	// Apply overrides — these should win over template-rendered values.
	err = ApplyOverrides(role, []string{"working_dir=/bar", "agent_model=haiku"})
	if err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}

	if role.WorkingDir != "/bar" {
		t.Errorf("post-override WorkingDir = %q, want %q", role.WorkingDir, "/bar")
	}
	if role.GetModel() != "haiku" {
		t.Errorf("post-override GetModel() = %q, want %q", role.GetModel(), "haiku")
	}
}

// --- Section 6.4: ListRoles with Templated Roles ---

func TestListRoles_WithTemplatedRoles(t *testing.T) {
	dir := t.TempDir()
	rolesDir := filepath.Join(dir, "roles")
	os.MkdirAll(rolesDir, 0o755)

	// Write a static role.
	os.WriteFile(filepath.Join(rolesDir, "static.yaml"), []byte(`
role_name: static
instructions: |
  A static agent.
`), 0o644)

	// Write a templated role with {{ }} expressions.
	os.WriteFile(filepath.Join(rolesDir, "templated.yaml"), []byte(`
role_name: templated
instructions: |
  You are {{ .AgentName }} on team {{ .Var.team }}.
`), 0o644)

	// Write a templated role with variables section.
	os.WriteFile(filepath.Join(rolesDir, "parameterized.yaml"), []byte(`
role_name: parameterized
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
		if role.RoleName == "templated" {
			if !strings.Contains(role.Instructions, "{{ .AgentName }}") {
				t.Error("templated role instructions should contain raw {{ .AgentName }}")
			}
		}
		if role.RoleName == "parameterized" {
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

	if roleRendered.RoleName != roleStatic.RoleName {
		t.Errorf("Name mismatch: rendered=%q, static=%q", roleRendered.RoleName, roleStatic.RoleName)
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

// --- Validate tests for system_prompt and permission_mode ---

func TestValidate_InstructionsOnly(t *testing.T) {
	role := &Role{RoleName: "test", Instructions: "Do stuff"}
	if err := role.Validate(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidate_SystemPromptOnly(t *testing.T) {
	role := &Role{RoleName: "test", SystemPrompt: "You are a custom agent."}
	if err := role.Validate(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidate_BothInstructionsAndSystemPrompt(t *testing.T) {
	role := &Role{RoleName: "test", Instructions: "Do stuff", SystemPrompt: "Custom prompt"}
	if err := role.Validate(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidate_SplitInstructionsOnly(t *testing.T) {
	role := &Role{RoleName: "test", InstructionsIntro: "Intro", InstructionsBody: "Body"}
	if err := role.Validate(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidate_InstructionsAndSplitMutuallyExclusive(t *testing.T) {
	role := &Role{RoleName: "test", Instructions: "Do stuff", InstructionsIntro: "Intro"}
	err := role.Validate()
	if err == nil {
		t.Fatal("expected error when both instructions and split fields are set")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually exclusive error, got: %v", err)
	}
}

func TestValidate_PermissionReviewAgentInstructionsMutuallyExclusive(t *testing.T) {
	role := &Role{
		RoleName: "test",
		PermissionReviewAgent: &PermissionReviewAgent{
			Instructions:      "single",
			InstructionsIntro: "intro",
		},
	}
	err := role.Validate()
	if err == nil {
		t.Fatal("expected error when permission_review_agent has both instructions and split fields")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually exclusive error, got: %v", err)
	}
}

func TestValidate_NeitherInstructionsNorSplit(t *testing.T) {
	role := &Role{RoleName: "test"}
	if err := role.Validate(); err != nil {
		t.Fatalf("expected no error when neither is set, got: %v", err)
	}
}

func TestValidate_PermissionMode_Valid(t *testing.T) {
	for _, mode := range ValidPermissionModes {
		role := &Role{RoleName: "test", PermissionMode: mode}
		if err := role.Validate(); err != nil {
			t.Errorf("expected no error for permission_mode %q, got: %v", mode, err)
		}
	}
}

func TestValidate_PermissionMode_Invalid(t *testing.T) {
	role := &Role{RoleName: "test", PermissionMode: "yolo"}
	err := role.Validate()
	if err == nil {
		t.Fatal("expected error for invalid permission_mode")
	}
	if !strings.Contains(err.Error(), "invalid permission_mode") {
		t.Errorf("expected 'invalid permission_mode' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "yolo") {
		t.Errorf("error should contain the invalid value 'yolo', got: %v", err)
	}
	// Should list valid options.
	if !strings.Contains(err.Error(), "default") || !strings.Contains(err.Error(), "bypassPermissions") {
		t.Errorf("error should list valid options, got: %v", err)
	}
}

func TestValidate_PermissionMode_Empty(t *testing.T) {
	role := &Role{RoleName: "test", PermissionMode: ""}
	if err := role.Validate(); err != nil {
		t.Fatalf("empty permission_mode should be valid, got: %v", err)
	}
}

func TestLoadRoleFrom_SystemPromptField(t *testing.T) {
	yaml := `
role_name: custom
system_prompt: |
  You are a completely custom agent with no default behavior.
`
	path := writeTempFile(t, "custom.yaml", yaml)
	role, err := LoadRoleFrom(path)
	if err != nil {
		t.Fatalf("LoadRoleFrom: %v", err)
	}
	if !strings.Contains(role.SystemPrompt, "completely custom agent") {
		t.Errorf("SystemPrompt not loaded, got: %q", role.SystemPrompt)
	}
	if role.Instructions != "" {
		t.Errorf("Instructions should be empty, got: %q", role.Instructions)
	}
}

func TestLoadRoleFrom_PermissionModeField(t *testing.T) {
	yaml := `
role_name: permissive
instructions: |
  Do stuff.
permission_mode: bypassPermissions
`
	path := writeTempFile(t, "permissive.yaml", yaml)
	role, err := LoadRoleFrom(path)
	if err != nil {
		t.Fatalf("LoadRoleFrom: %v", err)
	}
	if role.PermissionMode != "bypassPermissions" {
		t.Errorf("PermissionMode = %q, want %q", role.PermissionMode, "bypassPermissions")
	}
}

func TestLoadRoleFrom_InvalidPermissionMode(t *testing.T) {
	yaml := `
role_name: bad
instructions: |
  Do stuff.
permission_mode: invalid_mode
`
	path := writeTempFile(t, "bad.yaml", yaml)
	_, err := LoadRoleFrom(path)
	if err == nil {
		t.Fatal("expected error for invalid permission_mode")
	}
	if !strings.Contains(err.Error(), "invalid permission_mode") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- CodexAskForApproval validation tests ---

func TestValidate_CodexAskForApproval_Valid(t *testing.T) {
	for _, val := range ValidCodexAskForApproval {
		role := &Role{RoleName: "test", CodexAskForApproval: val}
		if err := role.Validate(); err != nil {
			t.Errorf("expected no error for codex_ask_for_approval %q, got: %v", val, err)
		}
	}
}

func TestValidate_CodexAskForApproval_Invalid(t *testing.T) {
	role := &Role{RoleName: "test", CodexAskForApproval: "yolo"}
	err := role.Validate()
	if err == nil {
		t.Fatal("expected error for invalid codex_ask_for_approval")
	}
	if !strings.Contains(err.Error(), "invalid codex_ask_for_approval") {
		t.Errorf("expected 'invalid codex_ask_for_approval' in error, got: %v", err)
	}
}

func TestValidate_CodexAskForApproval_Empty(t *testing.T) {
	role := &Role{RoleName: "test", CodexAskForApproval: ""}
	if err := role.Validate(); err != nil {
		t.Fatalf("empty codex_ask_for_approval should be valid, got: %v", err)
	}
}

func TestValidate_CodexSandboxMode_Valid(t *testing.T) {
	for _, mode := range ValidCodexSandboxModes {
		role := &Role{RoleName: "test", CodexSandboxMode: mode}
		if err := role.Validate(); err != nil {
			t.Errorf("expected no error for codex_sandbox_mode %q, got: %v", mode, err)
		}
	}
}

func TestValidate_CodexSandboxMode_Invalid(t *testing.T) {
	role := &Role{RoleName: "test", CodexSandboxMode: "yolo"}
	err := role.Validate()
	if err == nil {
		t.Fatal("expected error for invalid codex_sandbox_mode")
	}
	if !strings.Contains(err.Error(), "invalid codex_sandbox_mode") {
		t.Errorf("expected 'invalid codex_sandbox_mode' in error, got: %v", err)
	}
}

func TestValidate_CodexSandboxMode_Empty(t *testing.T) {
	role := &Role{RoleName: "test", CodexSandboxMode: ""}
	if err := role.Validate(); err != nil {
		t.Fatalf("empty codex_sandbox_mode should be valid, got: %v", err)
	}
}

func TestLoadRoleFrom_CodexAskForApprovalField(t *testing.T) {
	yaml := `
role_name: test
codex_ask_for_approval: on-request
codex_sandbox_mode: workspace-write
`
	path := writeTempFile(t, "codex.yaml", yaml)
	role, err := LoadRoleFrom(path)
	if err != nil {
		t.Fatalf("LoadRoleFrom: %v", err)
	}
	if role.CodexAskForApproval != "on-request" {
		t.Errorf("CodexAskForApproval = %q, want %q", role.CodexAskForApproval, "on-request")
	}
	if role.CodexSandboxMode != "workspace-write" {
		t.Errorf("CodexSandboxMode = %q, want %q", role.CodexSandboxMode, "workspace-write")
	}
}

func TestLoadRoleFrom_InvalidCodexAskForApproval(t *testing.T) {
	yaml := `
role_name: bad
codex_ask_for_approval: invalid_policy
`
	path := writeTempFile(t, "bad-codex.yaml", yaml)
	_, err := LoadRoleFrom(path)
	if err == nil {
		t.Fatal("expected error for invalid codex_ask_for_approval")
	}
	if !strings.Contains(err.Error(), "invalid codex_ask_for_approval") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoadRoleFrom_InvalidCodexSandboxMode(t *testing.T) {
	yaml := `
role_name: bad
codex_sandbox_mode: invalid_mode
`
	path := writeTempFile(t, "bad-sandbox.yaml", yaml)
	_, err := LoadRoleFrom(path)
	if err == nil {
		t.Fatal("expected error for invalid codex_sandbox_mode")
	}
	if !strings.Contains(err.Error(), "invalid codex_sandbox_mode") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- Harness config tests ---

func TestGetHarnessType_Default(t *testing.T) {
	role := &Role{RoleName: "test"}
	if got := role.GetHarnessType(); got != "claude_code" {
		t.Errorf("GetHarnessType() = %q, want %q", got, "claude_code")
	}
}

func TestGetHarnessType_ExplicitConfig(t *testing.T) {
	role := &Role{RoleName: "test", AgentHarness: "codex"}
	if got := role.GetHarnessType(); got != "codex" {
		t.Errorf("GetHarnessType() = %q, want %q", got, "codex")
	}
}

func TestGetAgentType_MapsClaudeCodeToClaude(t *testing.T) {
	role := &Role{RoleName: "test", AgentHarness: "claude_code"}
	if got := role.GetAgentType(); got != "" {
		t.Errorf("GetAgentType() = %q, want empty explicit command", got)
	}
}

func TestGetAgentType_GenericWithCommand(t *testing.T) {
	role := &Role{
		RoleName:            "test",
		AgentHarness:        "generic",
		AgentHarnessCommand: "/usr/local/bin/my-agent",
	}
	if got := role.GetAgentType(); got != "/usr/local/bin/my-agent" {
		t.Errorf("GetAgentType() = %q, want %q", got, "/usr/local/bin/my-agent")
	}
}

func TestGetModel(t *testing.T) {
	role := &Role{RoleName: "test", AgentModel: "sonnet"}
	if got := role.GetModel(); got != "sonnet" {
		t.Errorf("GetModel() = %q, want %q", got, "sonnet")
	}
}

func TestGetClaudeConfigDir_ExplicitPath(t *testing.T) {
	role := &Role{RoleName: "test", ClaudeCodeConfigPath: "/new/config/dir"}
	if got := role.GetClaudeConfigDir(); got != "/new/config/dir" {
		t.Errorf("GetClaudeConfigDir() = %q, want %q", got, "/new/config/dir")
	}
}

func TestGetCodexConfigDir(t *testing.T) {
	// Not set → defaults to codex-config/default.
	role := &Role{RoleName: "test"}
	wantDefault := filepath.Join(ConfigDir(), "codex-config", "default")
	if got := role.GetCodexConfigDir(); got != wantDefault {
		t.Errorf("GetCodexConfigDir() = %q, want %q", got, wantDefault)
	}

	// Set via explicit codex path.
	role2 := &Role{RoleName: "test", CodexConfigPath: "/codex/config"}
	if got := role2.GetCodexConfigDir(); got != "/codex/config" {
		t.Errorf("GetCodexConfigDir() = %q, want %q", got, "/codex/config")
	}
}

// --- LoadRoleWithNameResolution tests ---

func TestLoadRoleWithNameResolution_CLINameSkipsTwoPass(t *testing.T) {
	yaml := `
role_name: test
agent_name: ignored-template-name
instructions: |
  You are {{ .AgentName }}.
`
	path := writeTempFile(t, "cli-name.yaml", yaml)
	ctx := &tmpl.Context{RoleName: "test", H2Dir: "/tmp/h2"}

	role, name, err := LoadRoleWithNameResolution(path, ctx, nil, "cli-provided", func() string {
		t.Fatal("generateFallback should not be called when CLI name is provided")
		return ""
	})
	if err != nil {
		t.Fatalf("LoadRoleWithNameResolution: %v", err)
	}
	if name != "cli-provided" {
		t.Errorf("name = %q, want %q", name, "cli-provided")
	}
	if !strings.Contains(role.Instructions, "cli-provided") {
		t.Errorf("instructions should contain CLI name: %q", role.Instructions)
	}
}

func TestLoadRoleWithNameResolution_AgentNameFromRole(t *testing.T) {
	yaml := `
role_name: test
agent_name: my-static-agent
instructions: |
  You are {{ .AgentName }}.
`
	path := writeTempFile(t, "static-name.yaml", yaml)
	ctx := &tmpl.Context{RoleName: "test", H2Dir: "/tmp/h2"}

	role, name, err := LoadRoleWithNameResolution(path, ctx, nil, "", func() string {
		t.Fatal("generateFallback should not be called when agent_name is set")
		return ""
	})
	if err != nil {
		t.Fatalf("LoadRoleWithNameResolution: %v", err)
	}
	if name != "my-static-agent" {
		t.Errorf("name = %q, want %q", name, "my-static-agent")
	}
	if !strings.Contains(role.Instructions, "my-static-agent") {
		t.Errorf("instructions should contain resolved name: %q", role.Instructions)
	}
}

func TestLoadRoleWithNameResolution_RandomName(t *testing.T) {
	yaml := `
role_name: test
agent_name: '{{ randomName }}'
instructions: |
  You are {{ .AgentName }}.
`
	path := writeTempFile(t, "random-name.yaml", yaml)
	ctx := &tmpl.Context{RoleName: "test", H2Dir: "/tmp/h2"}
	nameFuncs := tmpl.NameFuncs(func() string { return "bright-hare" }, nil)

	role, name, err := LoadRoleWithNameResolution(path, ctx, nameFuncs, "", func() string {
		t.Fatal("generateFallback should not be called when agent_name uses randomName")
		return ""
	})
	if err != nil {
		t.Fatalf("LoadRoleWithNameResolution: %v", err)
	}
	if name != "bright-hare" {
		t.Errorf("name = %q, want %q", name, "bright-hare")
	}
	if !strings.Contains(role.Instructions, "bright-hare") {
		t.Errorf("instructions should contain random name: %q", role.Instructions)
	}
}

func TestLoadRoleWithNameResolution_AutoIncrement(t *testing.T) {
	yaml := `
role_name: test
agent_name: '{{ autoIncrement "worker" }}'
instructions: |
  You are {{ .AgentName }}.
`
	path := writeTempFile(t, "autoincr-name.yaml", yaml)
	ctx := &tmpl.Context{RoleName: "test", H2Dir: "/tmp/h2"}
	nameFuncs := tmpl.NameFuncs(nil, []string{"worker-1", "worker-3"})

	role, name, err := LoadRoleWithNameResolution(path, ctx, nameFuncs, "", func() string {
		t.Fatal("generateFallback should not be called")
		return ""
	})
	if err != nil {
		t.Fatalf("LoadRoleWithNameResolution: %v", err)
	}
	if name != "worker-4" {
		t.Errorf("name = %q, want %q", name, "worker-4")
	}
	if !strings.Contains(role.Instructions, "worker-4") {
		t.Errorf("instructions should contain auto-incremented name: %q", role.Instructions)
	}
}

func TestLoadRoleWithNameResolution_FallbackWhenNoAgentName(t *testing.T) {
	yaml := `
role_name: test
instructions: |
  You are {{ .AgentName }}.
`
	path := writeTempFile(t, "no-agent-name.yaml", yaml)
	ctx := &tmpl.Context{RoleName: "test", H2Dir: "/tmp/h2"}

	fallbackCalled := false
	role, name, err := LoadRoleWithNameResolution(path, ctx, nil, "", func() string {
		fallbackCalled = true
		return "fallback-name"
	})
	if err != nil {
		t.Fatalf("LoadRoleWithNameResolution: %v", err)
	}
	if !fallbackCalled {
		t.Error("expected generateFallback to be called")
	}
	if name != "fallback-name" {
		t.Errorf("name = %q, want %q", name, "fallback-name")
	}
	if !strings.Contains(role.Instructions, "fallback-name") {
		t.Errorf("instructions should contain fallback name: %q", role.Instructions)
	}
}

func TestLoadRoleWithNameResolution_CircularReference(t *testing.T) {
	yaml := `
role_name: test
agent_name: '{{ .AgentName }}-suffix'
instructions: test
`
	path := writeTempFile(t, "circular.yaml", yaml)
	ctx := &tmpl.Context{RoleName: "test", H2Dir: "/tmp/h2"}

	_, _, err := LoadRoleWithNameResolution(path, ctx, nil, "", func() string { return "x" })
	if err == nil {
		t.Fatal("expected error for circular agent_name reference")
	}
	if !strings.Contains(err.Error(), "circular reference") {
		t.Errorf("error = %q, want it to mention 'circular reference'", err.Error())
	}
}

func TestLoadRoleWithNameResolution_NameFuncsCached(t *testing.T) {
	// Both passes should get the same randomName value (caching).
	yaml := `
role_name: test
agent_name: '{{ randomName }}'
instructions: |
  You are {{ .AgentName }} running {{ randomName }}.
`
	path := writeTempFile(t, "cached-funcs.yaml", yaml)
	ctx := &tmpl.Context{RoleName: "test", H2Dir: "/tmp/h2"}

	calls := 0
	nameFuncs := tmpl.NameFuncs(func() string {
		calls++
		return "cached-name"
	}, nil)

	role, name, err := LoadRoleWithNameResolution(path, ctx, nameFuncs, "", func() string {
		return "unused"
	})
	if err != nil {
		t.Fatalf("LoadRoleWithNameResolution: %v", err)
	}
	if name != "cached-name" {
		t.Errorf("name = %q, want %q", name, "cached-name")
	}
	// randomName in instructions should also be "cached-name" (cached).
	if !strings.Contains(role.Instructions, "cached-name running cached-name") {
		t.Errorf("instructions should use cached name: %q", role.Instructions)
	}
	// Generator should only be called once (cached).
	if calls != 1 {
		t.Errorf("expected 1 generate call (cached), got %d", calls)
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
