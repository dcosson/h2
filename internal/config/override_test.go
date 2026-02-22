package config

import (
	"strings"
	"testing"
)

func TestApplyOverrides_SimpleString(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test"}
	err := ApplyOverrides(role, []string{"working_dir=/workspace/project"})
	if err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if role.WorkingDir != "/workspace/project" {
		t.Errorf("WorkingDir = %q, want %q", role.WorkingDir, "/workspace/project")
	}
}

func TestApplyOverrides_MultipleStrings(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test"}
	err := ApplyOverrides(role, []string{
		"working_dir=/workspace",
		"model=opus",
		"description=My agent",
	})
	if err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if role.WorkingDir != "/workspace" {
		t.Errorf("WorkingDir = %q, want %q", role.WorkingDir, "/workspace")
	}
	if role.GetModel() != "opus" {
		t.Errorf("GetModel() = %q, want %q", role.GetModel(), "opus")
	}
	if role.Description != "My agent" {
		t.Errorf("Description = %q, want %q", role.Description, "My agent")
	}
}

func TestApplyOverrides_NestedBool(t *testing.T) {
	role := &Role{
		Name:         "test",
		Instructions: "test",
		Worktree:     &WorktreeConfig{ProjectDir: "/tmp/repo", Name: "test-wt"},
	}
	err := ApplyOverrides(role, []string{"worktree.use_detached_head=true"})
	if err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if !role.Worktree.UseDetachedHead {
		t.Error("Worktree.UseDetachedHead should be true")
	}
}

func TestApplyOverrides_NestedBoolFalse(t *testing.T) {
	role := &Role{
		Name:         "test",
		Instructions: "test",
		Worktree:     &WorktreeConfig{ProjectDir: "/tmp/repo", Name: "test-wt", UseDetachedHead: true},
	}
	err := ApplyOverrides(role, []string{"worktree.use_detached_head=false"})
	if err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if role.Worktree.UseDetachedHead {
		t.Error("Worktree.UseDetachedHead should be false")
	}
}

func TestApplyOverrides_AutoInitNilWorktree(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test"}
	if role.Worktree != nil {
		t.Fatal("precondition: Worktree should be nil")
	}
	err := ApplyOverrides(role, []string{"worktree.project_dir=/tmp/repo"})
	if err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if role.Worktree == nil {
		t.Fatal("Worktree should have been auto-initialized")
	}
	if role.Worktree.ProjectDir != "/tmp/repo" {
		t.Errorf("Worktree.ProjectDir = %q, want %q", role.Worktree.ProjectDir, "/tmp/repo")
	}
}

func TestApplyOverrides_AutoInitNilHeartbeat(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test"}
	err := ApplyOverrides(role, []string{"heartbeat.message=nudge"})
	if err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if role.Heartbeat == nil {
		t.Fatal("Heartbeat should have been auto-initialized")
	}
	if role.Heartbeat.Message != "nudge" {
		t.Errorf("Heartbeat.Message = %q, want %q", role.Heartbeat.Message, "nudge")
	}
}

func TestApplyOverrides_InvalidKey(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test"}
	err := ApplyOverrides(role, []string{"nonexistent_field=value"})
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Errorf("error = %q, want it to contain 'unknown'", err.Error())
	}
}

func TestApplyOverrides_InvalidNestedKey(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test"}
	err := ApplyOverrides(role, []string{"worktree.nonexistent=value"})
	if err == nil {
		t.Fatal("expected error for unknown nested key")
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Errorf("error = %q, want it to contain 'unknown'", err.Error())
	}
}

func TestApplyOverrides_TypeMismatch_BoolField(t *testing.T) {
	role := &Role{
		Name:         "test",
		Instructions: "test",
		Worktree:     &WorktreeConfig{ProjectDir: "/tmp/repo", Name: "test-wt"},
	}
	err := ApplyOverrides(role, []string{"worktree.use_detached_head=notabool"})
	if err == nil {
		t.Fatal("expected error for bool type mismatch")
	}
	if !strings.Contains(err.Error(), "bool") {
		t.Errorf("error = %q, want it to mention 'bool'", err.Error())
	}
}

func TestApplyOverrides_NonOverridable_Name(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test"}
	err := ApplyOverrides(role, []string{"name=hacked"})
	if err == nil {
		t.Fatal("expected error for non-overridable field 'name'")
	}
	if !strings.Contains(err.Error(), "cannot be overridden") {
		t.Errorf("error = %q, want it to contain 'cannot be overridden'", err.Error())
	}
}

func TestApplyOverrides_NonOverridable_Instructions(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test"}
	err := ApplyOverrides(role, []string{"instructions=hacked"})
	if err == nil {
		t.Fatal("expected error for non-overridable field 'instructions'")
	}
}

func TestApplyOverrides_NonOverridable_Permissions(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test"}
	err := ApplyOverrides(role, []string{"permissions=hacked"})
	if err == nil {
		t.Fatal("expected error for non-overridable field 'permissions'")
	}
}

func TestApplyOverrides_NonOverridable_Hooks(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test"}
	err := ApplyOverrides(role, []string{"hooks=hacked"})
	if err == nil {
		t.Fatal("expected error for non-overridable field 'hooks'")
	}
}

func TestApplyOverrides_NonOverridable_Settings(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test"}
	err := ApplyOverrides(role, []string{"settings=hacked"})
	if err == nil {
		t.Fatal("expected error for non-overridable field 'settings'")
	}
}

func TestApplyOverrides_BadFormat_NoEquals(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test"}
	err := ApplyOverrides(role, []string{"justakeynovalue"})
	if err == nil {
		t.Fatal("expected error for missing '='")
	}
}

func TestApplyOverrides_EmptySlice(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test"}
	err := ApplyOverrides(role, nil)
	if err != nil {
		t.Fatalf("ApplyOverrides with nil: %v", err)
	}
	err = ApplyOverrides(role, []string{})
	if err != nil {
		t.Fatalf("ApplyOverrides with empty: %v", err)
	}
}

func TestApplyOverrides_ValueWithEquals(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test"}
	err := ApplyOverrides(role, []string{"working_dir=/path/with=equals"})
	if err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if role.WorkingDir != "/path/with=equals" {
		t.Errorf("WorkingDir = %q, want %q", role.WorkingDir, "/path/with=equals")
	}
}

func TestApplyOverrides_NestedStringField(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test"}
	err := ApplyOverrides(role, []string{"worktree.branch_from=develop"})
	if err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if role.Worktree == nil {
		t.Fatal("Worktree should have been auto-initialized")
	}
	if role.Worktree.BranchFrom != "develop" {
		t.Errorf("Worktree.BranchFrom = %q, want %q", role.Worktree.BranchFrom, "develop")
	}
}

func TestApplyOverrides_NestedAgentHarness(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test"}
	err := ApplyOverrides(role, []string{"agent_harness.model=sonnet"})
	if err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if role.AgentHarness == nil {
		t.Fatal("AgentHarness should have been auto-initialized")
	}
	if role.AgentHarness.Model != "sonnet" {
		t.Errorf("AgentHarness.Model = %q, want %q", role.AgentHarness.Model, "sonnet")
	}
	if role.GetModel() != "sonnet" {
		t.Errorf("GetModel() = %q, want %q", role.GetModel(), "sonnet")
	}
}

func TestApplyOverrides_NestedAgentHarnessType(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test"}
	err := ApplyOverrides(role, []string{"agent_harness.harness_type=codex"})
	if err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if role.GetHarnessType() != "codex" {
		t.Errorf("GetHarnessType() = %q, want %q", role.GetHarnessType(), "codex")
	}
}

func TestApplyOverrides_LegacyModelStillWorks(t *testing.T) {
	role := &Role{Name: "test", Instructions: "test"}
	err := ApplyOverrides(role, []string{"model=opus"})
	if err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if role.GetModel() != "opus" {
		t.Errorf("GetModel() = %q, want %q", role.GetModel(), "opus")
	}
}

func TestParseOverrides(t *testing.T) {
	overrides := []string{"working_dir=/workspace", "model=opus"}
	m, err := ParseOverrides(overrides)
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	if len(m) != 2 {
		t.Fatalf("len = %d, want 2", len(m))
	}
	if m["working_dir"] != "/workspace" {
		t.Errorf("working_dir = %q, want %q", m["working_dir"], "/workspace")
	}
	if m["model"] != "opus" {
		t.Errorf("model = %q, want %q", m["model"], "opus")
	}
}

func TestOverridesRecordedInMetadata(t *testing.T) {
	dir := t.TempDir()

	overrides := map[string]string{
		"working_dir":          "/workspace",
		"worktree.project_dir": "/tmp/repo",
	}
	meta := SessionMetadata{
		AgentName: "test-agent",
		SessionID: "test-session",
		Role:      "coder",
		Overrides: overrides,
		StartedAt: "2026-01-01T00:00:00Z",
	}

	if err := WriteSessionMetadata(dir, meta); err != nil {
		t.Fatalf("WriteSessionMetadata: %v", err)
	}

	got, err := ReadSessionMetadata(dir)
	if err != nil {
		t.Fatalf("ReadSessionMetadata: %v", err)
	}

	if len(got.Overrides) != 2 {
		t.Fatalf("Overrides len = %d, want 2", len(got.Overrides))
	}
	if got.Overrides["working_dir"] != "/workspace" {
		t.Errorf("Overrides[working_dir] = %q, want %q", got.Overrides["working_dir"], "/workspace")
	}
	if got.Overrides["worktree.project_dir"] != "/tmp/repo" {
		t.Errorf("Overrides[worktree.project_dir] = %q, want %q", got.Overrides["worktree.project_dir"], "/tmp/repo")
	}
}

func TestMetadataWithoutOverrides(t *testing.T) {
	dir := t.TempDir()

	meta := SessionMetadata{
		AgentName: "test-agent",
		SessionID: "test-session",
		StartedAt: "2026-01-01T00:00:00Z",
	}

	if err := WriteSessionMetadata(dir, meta); err != nil {
		t.Fatalf("WriteSessionMetadata: %v", err)
	}

	got, err := ReadSessionMetadata(dir)
	if err != nil {
		t.Fatalf("ReadSessionMetadata: %v", err)
	}

	if got.Overrides != nil {
		t.Errorf("Overrides should be nil when not set, got %v", got.Overrides)
	}
}
