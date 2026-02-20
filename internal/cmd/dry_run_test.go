package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2/internal/config"
)

func TestResolveAgentConfig_Basic(t *testing.T) {
	t.Setenv("H2_DIR", "")

	role := &config.Role{
		Name:         "test-role",
		Description:  "A test role",
		Instructions: "Do testing things",
	}

	rc, err := resolveAgentConfig("test-agent", role, "", nil)
	if err != nil {
		t.Fatalf("resolveAgentConfig: %v", err)
	}

	if rc.Name != "test-agent" {
		t.Errorf("Name = %q, want %q", rc.Name, "test-agent")
	}
	if rc.Command != "claude" {
		t.Errorf("Command = %q, want %q", rc.Command, "claude")
	}
	if rc.Role != role {
		t.Error("Role should be the same pointer")
	}
	if rc.IsWorktree {
		t.Error("IsWorktree should be false")
	}
	if rc.WorkingDir == "" {
		t.Error("WorkingDir should not be empty")
	}
	if rc.EnvVars["H2_ACTOR"] != "test-agent" {
		t.Errorf("H2_ACTOR = %q, want %q", rc.EnvVars["H2_ACTOR"], "test-agent")
	}
	if rc.EnvVars["H2_ROLE"] != "test-role" {
		t.Errorf("H2_ROLE = %q, want %q", rc.EnvVars["H2_ROLE"], "test-role")
	}
}

func TestResolveAgentConfig_WithPod(t *testing.T) {
	t.Setenv("H2_DIR", "")

	role := &config.Role{
		Name:         "test-role",
		Instructions: "Test instructions",
	}

	rc, err := resolveAgentConfig("test-agent", role, "my-pod", nil)
	if err != nil {
		t.Fatalf("resolveAgentConfig: %v", err)
	}

	if rc.Pod != "my-pod" {
		t.Errorf("Pod = %q, want %q", rc.Pod, "my-pod")
	}
	if rc.EnvVars["H2_POD"] != "my-pod" {
		t.Errorf("H2_POD = %q, want %q", rc.EnvVars["H2_POD"], "my-pod")
	}
}

func TestResolveAgentConfig_NoPodEnvWhenEmpty(t *testing.T) {
	t.Setenv("H2_DIR", "")

	role := &config.Role{
		Name:         "test-role",
		Instructions: "Test instructions",
	}

	rc, err := resolveAgentConfig("test-agent", role, "", nil)
	if err != nil {
		t.Fatalf("resolveAgentConfig: %v", err)
	}

	if _, ok := rc.EnvVars["H2_POD"]; ok {
		t.Error("H2_POD should not be set when pod is empty")
	}
}

func TestResolveAgentConfig_GeneratesName(t *testing.T) {
	t.Setenv("H2_DIR", "")

	role := &config.Role{
		Name:         "test-role",
		Instructions: "Test instructions",
	}

	rc, err := resolveAgentConfig("", role, "", nil)
	if err != nil {
		t.Fatalf("resolveAgentConfig: %v", err)
	}

	if rc.Name == "" {
		t.Error("Name should be auto-generated")
	}
}

func TestResolveAgentConfig_Overrides(t *testing.T) {
	t.Setenv("H2_DIR", "")

	role := &config.Role{
		Name:         "test-role",
		Instructions: "Test instructions",
	}

	overrides := []string{"model=opus", "description=custom"}
	rc, err := resolveAgentConfig("test-agent", role, "", overrides)
	if err != nil {
		t.Fatalf("resolveAgentConfig: %v", err)
	}

	if len(rc.Overrides) != 2 {
		t.Errorf("Overrides count = %d, want 2", len(rc.Overrides))
	}
}

func TestResolveAgentConfig_Worktree(t *testing.T) {
	t.Setenv("H2_DIR", "")

	role := &config.Role{
		Name:         "test-role",
		Instructions: "Test instructions",
		Worktree: &config.WorktreeConfig{
			ProjectDir: "/tmp/repo",
			Name:       "test-wt",
		},
	}

	rc, err := resolveAgentConfig("test-agent", role, "", nil)
	if err != nil {
		t.Fatalf("resolveAgentConfig: %v", err)
	}

	if !rc.IsWorktree {
		t.Error("IsWorktree should be true")
	}
	if !strings.Contains(rc.WorkingDir, "test-wt") {
		t.Errorf("WorkingDir = %q, should contain %q", rc.WorkingDir, "test-wt")
	}
}

func TestResolveAgentConfig_ChildArgsIncludeInstructions(t *testing.T) {
	t.Setenv("H2_DIR", "")

	role := &config.Role{
		Name:         "test-role",
		Instructions: "Do the thing\nLine 2",
	}

	rc, err := resolveAgentConfig("test-agent", role, "", nil)
	if err != nil {
		t.Fatalf("resolveAgentConfig: %v", err)
	}

	// Should have --session-id placeholder and --append-system-prompt.
	foundSessionID := false
	foundAppend := false
	for i, arg := range rc.ChildArgs {
		if arg == "--session-id" {
			foundSessionID = true
		}
		if arg == "--append-system-prompt" && i+1 < len(rc.ChildArgs) {
			foundAppend = true
			if rc.ChildArgs[i+1] != "Do the thing\nLine 2" {
				t.Errorf("--append-system-prompt value = %q, want %q", rc.ChildArgs[i+1], "Do the thing\nLine 2")
			}
		}
	}
	if !foundSessionID {
		t.Error("ChildArgs should contain --session-id")
	}
	if !foundAppend {
		t.Error("ChildArgs should contain --append-system-prompt")
	}
}

func TestResolveAgentConfig_NoInstructionsNoAppendFlag(t *testing.T) {
	t.Setenv("H2_DIR", "")

	role := &config.Role{
		Name:         "test-role",
		Instructions: "",
	}

	rc, err := resolveAgentConfig("test-agent", role, "", nil)
	if err != nil {
		t.Fatalf("resolveAgentConfig: %v", err)
	}

	for _, arg := range rc.ChildArgs {
		if arg == "--append-system-prompt" {
			t.Error("ChildArgs should NOT contain --append-system-prompt when instructions are empty")
		}
	}
}

func TestResolveAgentConfig_Heartbeat(t *testing.T) {
	t.Setenv("H2_DIR", "")

	role := &config.Role{
		Name:         "test-role",
		Instructions: "Test",
		Heartbeat: &config.HeartbeatConfig{
			IdleTimeout: "30s",
			Message:     "Still there?",
			Condition:   "idle",
		},
	}

	rc, err := resolveAgentConfig("test-agent", role, "", nil)
	if err != nil {
		t.Fatalf("resolveAgentConfig: %v", err)
	}

	if rc.Heartbeat.IdleTimeout.String() != "30s" {
		t.Errorf("Heartbeat.IdleTimeout = %q, want %q", rc.Heartbeat.IdleTimeout.String(), "30s")
	}
	if rc.Heartbeat.Message != "Still there?" {
		t.Errorf("Heartbeat.Message = %q, want %q", rc.Heartbeat.Message, "Still there?")
	}
}

func TestPrintDryRun_BasicOutput(t *testing.T) {
	t.Setenv("H2_DIR", "")

	role := &config.Role{
		Name:         "test-role",
		Description:  "A test role",
		ModelLegacy:  "opus",
		Instructions: "Do testing things\nWith multiple lines",
	}

	rc, err := resolveAgentConfig("test-agent", role, "my-pod", []string{"model=opus"})
	if err != nil {
		t.Fatalf("resolveAgentConfig: %v", err)
	}

	output := capturePrintDryRun(rc)

	// Check key sections are present.
	checks := []string{
		"Agent: test-agent",
		"Role: test-role",
		"Description: A test role",
		"Model: opus",
		"Instructions: (2 lines)",
		"Do testing things",
		"Command: claude",
		"H2_ACTOR=test-agent",
		"H2_ROLE=test-role",
		"H2_POD=my-pod",
		"Overrides: model=opus",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output should contain %q, got:\n%s", check, output)
		}
	}
}

func TestPrintDryRun_LongInstructionsTruncated(t *testing.T) {
	t.Setenv("H2_DIR", "")

	// Build instructions with 15 lines.
	var lines []string
	for i := 0; i < 15; i++ {
		lines = append(lines, fmt.Sprintf("Line %d of instructions", i+1))
	}
	role := &config.Role{
		Name:         "test-role",
		Instructions: strings.Join(lines, "\n"),
	}

	rc, err := resolveAgentConfig("test-agent", role, "", nil)
	if err != nil {
		t.Fatalf("resolveAgentConfig: %v", err)
	}

	output := capturePrintDryRun(rc)

	if !strings.Contains(output, "Instructions: (15 lines)") {
		t.Errorf("should show line count, got:\n%s", output)
	}
	if !strings.Contains(output, "... (5 more lines)") {
		t.Errorf("should show truncation message, got:\n%s", output)
	}
	// Lines 1-10 should be shown.
	if !strings.Contains(output, "Line 1 of instructions") {
		t.Errorf("should show first line, got:\n%s", output)
	}
	if !strings.Contains(output, "Line 10 of instructions") {
		t.Errorf("should show line 10, got:\n%s", output)
	}
	// Line 11+ should not be shown.
	if strings.Contains(output, "Line 11 of instructions") {
		t.Errorf("should NOT show line 11, got:\n%s", output)
	}
}

func TestPrintDryRun_Permissions(t *testing.T) {
	t.Setenv("H2_DIR", "")

	enabled := true
	role := &config.Role{
		Name:         "test-role",
		Instructions: "Test",
		Permissions: config.Permissions{
			Allow: []string{"Read", "Write"},
			Deny:  []string{"Bash"},
			Agent: &config.PermissionAgent{
				Enabled:      &enabled,
				Instructions: "Review carefully",
			},
		},
	}

	rc, err := resolveAgentConfig("test-agent", role, "", nil)
	if err != nil {
		t.Fatalf("resolveAgentConfig: %v", err)
	}

	output := capturePrintDryRun(rc)

	checks := []string{
		"Permissions:",
		"Allow: Read, Write",
		"Deny: Bash",
		"Agent Reviewer: true",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output should contain %q, got:\n%s", check, output)
		}
	}
}

func TestPrintDryRun_Heartbeat(t *testing.T) {
	t.Setenv("H2_DIR", "")

	role := &config.Role{
		Name:         "test-role",
		Instructions: "Test",
		Heartbeat: &config.HeartbeatConfig{
			IdleTimeout: "1m",
			Message:     "ping",
			Condition:   "idle",
		},
	}

	rc, err := resolveAgentConfig("test-agent", role, "", nil)
	if err != nil {
		t.Fatalf("resolveAgentConfig: %v", err)
	}

	output := capturePrintDryRun(rc)

	checks := []string{
		"Heartbeat:",
		"Idle Timeout: 1m0s",
		"Message: ping",
		"Condition: idle",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output should contain %q, got:\n%s", check, output)
		}
	}
}

func TestPrintDryRun_WorktreeLabel(t *testing.T) {
	t.Setenv("H2_DIR", "")

	role := &config.Role{
		Name:         "test-role",
		Instructions: "Test",
		Worktree: &config.WorktreeConfig{
			ProjectDir: "/tmp/repo",
			Name:       "test-wt",
		},
	}

	rc, err := resolveAgentConfig("test-agent", role, "", nil)
	if err != nil {
		t.Fatalf("resolveAgentConfig: %v", err)
	}

	output := capturePrintDryRun(rc)

	if !strings.Contains(output, "(worktree)") {
		t.Errorf("should indicate worktree mode, got:\n%s", output)
	}
}

func TestPrintDryRun_InstructionsArgTruncated(t *testing.T) {
	t.Setenv("H2_DIR", "")

	role := &config.Role{
		Name:         "test-role",
		Instructions: "Line 1\nLine 2\nLine 3",
	}

	rc, err := resolveAgentConfig("test-agent", role, "", nil)
	if err != nil {
		t.Fatalf("resolveAgentConfig: %v", err)
	}

	output := capturePrintDryRun(rc)

	// The Args line should show a truncated placeholder, not the full instructions.
	if !strings.Contains(output, "<3 lines>") {
		t.Errorf("Args should show truncated multiline placeholder, got:\n%s", output)
	}
}

func TestResolveAgentConfig_ChildArgsIncludeSystemPrompt(t *testing.T) {
	t.Setenv("H2_DIR", "")

	role := &config.Role{
		Name:         "test-role",
		SystemPrompt: "You are a custom agent.\nDo custom things.",
	}

	rc, err := resolveAgentConfig("test-agent", role, "", nil)
	if err != nil {
		t.Fatalf("resolveAgentConfig: %v", err)
	}

	found := false
	for i, arg := range rc.ChildArgs {
		if arg == "--system-prompt" && i+1 < len(rc.ChildArgs) {
			found = true
			if rc.ChildArgs[i+1] != "You are a custom agent.\nDo custom things." {
				t.Errorf("--system-prompt value = %q, want %q", rc.ChildArgs[i+1], "You are a custom agent.\nDo custom things.")
			}
		}
	}
	if !found {
		t.Error("ChildArgs should contain --system-prompt")
	}
}

func TestResolveAgentConfig_ChildArgsIncludeNewFields(t *testing.T) {
	t.Setenv("H2_DIR", "")

	role := &config.Role{
		Name:           "test-role",
		Instructions:   "Do work",
		ModelLegacy:    "opus",
		PermissionMode: "plan",
		Permissions: config.Permissions{
			Allow: []string{"Read", "Write"},
			Deny:  []string{"Bash(rm *)"},
		},
	}

	rc, err := resolveAgentConfig("test-agent", role, "", nil)
	if err != nil {
		t.Fatalf("resolveAgentConfig: %v", err)
	}

	checks := map[string]string{
		"--model":           "opus",
		"--permission-mode": "plan",
		"--allowedTools":    "Read,Write",
		"--disallowedTools": "Bash(rm *)",
	}
	for flag, want := range checks {
		found := false
		for i, arg := range rc.ChildArgs {
			if arg == flag && i+1 < len(rc.ChildArgs) {
				found = true
				if rc.ChildArgs[i+1] != want {
					t.Errorf("%s value = %q, want %q", flag, rc.ChildArgs[i+1], want)
				}
			}
		}
		if !found {
			t.Errorf("ChildArgs should contain %s", flag)
		}
	}
}

func TestPrintDryRun_SystemPromptTruncated(t *testing.T) {
	t.Setenv("H2_DIR", "")

	// Build system prompt with 15 lines.
	var lines []string
	for i := 0; i < 15; i++ {
		lines = append(lines, fmt.Sprintf("System line %d", i+1))
	}
	role := &config.Role{
		Name:         "test-role",
		SystemPrompt: strings.Join(lines, "\n"),
	}

	rc, err := resolveAgentConfig("test-agent", role, "", nil)
	if err != nil {
		t.Fatalf("resolveAgentConfig: %v", err)
	}

	output := capturePrintDryRun(rc)

	if !strings.Contains(output, "System Prompt: (15 lines)") {
		t.Errorf("should show system prompt line count, got:\n%s", output)
	}
	if !strings.Contains(output, "... (5 more lines)") {
		t.Errorf("should show truncation message, got:\n%s", output)
	}
	if !strings.Contains(output, "System line 1") {
		t.Errorf("should show first line, got:\n%s", output)
	}
	if strings.Contains(output, "System line 11") {
		t.Errorf("should NOT show line 11, got:\n%s", output)
	}
}

func TestPrintDryRun_PermissionMode(t *testing.T) {
	t.Setenv("H2_DIR", "")

	role := &config.Role{
		Name:           "test-role",
		Instructions:   "Test",
		PermissionMode: "plan",
	}

	rc, err := resolveAgentConfig("test-agent", role, "", nil)
	if err != nil {
		t.Fatalf("resolveAgentConfig: %v", err)
	}

	output := capturePrintDryRun(rc)

	if !strings.Contains(output, "Permission Mode: plan") {
		t.Errorf("should show permission mode, got:\n%s", output)
	}
}

func TestPrintDryRun_SystemPromptArgTruncated(t *testing.T) {
	t.Setenv("H2_DIR", "")

	role := &config.Role{
		Name:         "test-role",
		SystemPrompt: "Line 1\nLine 2\nLine 3",
	}

	rc, err := resolveAgentConfig("test-agent", role, "", nil)
	if err != nil {
		t.Fatalf("resolveAgentConfig: %v", err)
	}

	output := capturePrintDryRun(rc)

	if !strings.Contains(output, "<3 lines>") {
		t.Errorf("Args should show truncated multiline placeholder, got:\n%s", output)
	}
}

func TestPrintPodDryRun_Header(t *testing.T) {
	t.Setenv("H2_DIR", "")

	agents := []*ResolvedAgentConfig{
		{
			Name:    "coder-1",
			Role:    &config.Role{Name: "coder", Instructions: "Code stuff"},
			Command: "claude",
			Pod:     "my-pod",
			EnvVars: map[string]string{"H2_ACTOR": "coder-1", "H2_POD": "my-pod"},
		},
		{
			Name:    "coder-2",
			Role:    &config.Role{Name: "coder", Instructions: "Code stuff"},
			Command: "claude",
			Pod:     "my-pod",
			EnvVars: map[string]string{"H2_ACTOR": "coder-2", "H2_POD": "my-pod"},
		},
		{
			Name:    "reviewer",
			Role:    &config.Role{Name: "reviewer", Instructions: "Review stuff"},
			Command: "claude",
			Pod:     "my-pod",
			EnvVars: map[string]string{"H2_ACTOR": "reviewer", "H2_POD": "my-pod"},
		},
	}

	output := captureStdout(func() {
		printPodDryRun("backend", "my-pod", agents)
	})

	checks := []string{
		"Pod: my-pod",
		"Template: backend",
		"Agents: 3",
		"--- Agent 1/3 ---",
		"Agent: coder-1",
		"--- Agent 2/3 ---",
		"Agent: coder-2",
		"--- Agent 3/3 ---",
		"Agent: reviewer",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output should contain %q, got:\n%s", check, output)
		}
	}
	// Roles summary should contain both roles.
	if !strings.Contains(output, "Roles:") {
		t.Errorf("should show Roles line, got:\n%s", output)
	}
}

func TestPrintPodDryRun_RoleScopeAndVars(t *testing.T) {
	t.Setenv("H2_DIR", "")

	agents := []*ResolvedAgentConfig{
		{
			Name:       "worker",
			Role:       &config.Role{Name: "coder", Instructions: "Code"},
			Command:    "claude",
			Pod:        "my-pod",
			EnvVars:    map[string]string{"H2_ACTOR": "worker"},
			RoleScope:  "pod",
			MergedVars: map[string]string{"team": "backend", "env": "prod"},
		},
	}

	output := captureStdout(func() {
		printPodDryRun("test-tmpl", "my-pod", agents)
	})

	checks := []string{
		"Role Scope: pod",
		"Variables:",
		"team=backend",
		"env=prod",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output should contain %q, got:\n%s", check, output)
		}
	}
}

func TestPrintPodDryRun_GlobalRoleScope(t *testing.T) {
	t.Setenv("H2_DIR", "")

	agents := []*ResolvedAgentConfig{
		{
			Name:      "worker",
			Role:      &config.Role{Name: "default", Instructions: "Default"},
			Command:   "claude",
			Pod:       "my-pod",
			EnvVars:   map[string]string{"H2_ACTOR": "worker"},
			RoleScope: "global",
		},
	}

	output := captureStdout(func() {
		printPodDryRun("test-tmpl", "my-pod", agents)
	})

	if !strings.Contains(output, "Role Scope: global") {
		t.Errorf("should show global role scope, got:\n%s", output)
	}
}

func TestPodDryRun_WithFixtures(t *testing.T) {
	h2Root := setupPodTestEnv(t)

	// Create a pod template with 2 agents.
	tmplContent := `pod_name: test-pod
agents:
  - name: builder
    role: default
  - name: tester
    role: default
`
	os.WriteFile(filepath.Join(h2Root, "pods", "templates", "simple.yaml"), []byte(tmplContent), 0o644)

	// Create the default role.
	roleContent := "name: default\ninstructions: |\n  Do work.\n"
	os.WriteFile(filepath.Join(h2Root, "roles", "default.yaml"), []byte(roleContent), 0o644)

	// Execute via the cobra command with --dry-run.
	output := captureStdout(func() {
		cmd := newPodLaunchCmd()
		cmd.SetArgs([]string{"--dry-run", "simple"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	checks := []string{
		"Pod: test-pod",
		"Template: simple",
		"Agents: 2",
		"Agent: builder",
		"Agent: tester",
		"Role: default",
		"Role Scope: global",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output should contain %q, got:\n%s", check, output)
		}
	}
}

func TestPodDryRun_WithCountExpansion(t *testing.T) {
	h2Root := setupPodTestEnv(t)

	tmplContent := `pod_name: count-pod
agents:
  - name: worker
    role: default
    count: 3
`
	os.WriteFile(filepath.Join(h2Root, "pods", "templates", "counted.yaml"), []byte(tmplContent), 0o644)

	roleContent := "name: default\ninstructions: |\n  Do work.\n"
	os.WriteFile(filepath.Join(h2Root, "roles", "default.yaml"), []byte(roleContent), 0o644)

	output := captureStdout(func() {
		cmd := newPodLaunchCmd()
		cmd.SetArgs([]string{"--dry-run", "counted"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	checks := []string{
		"Agents: 3",
		"Agent: worker-1",
		"Agent: worker-2",
		"Agent: worker-3",
		"--- Agent 1/3 ---",
		"--- Agent 3/3 ---",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output should contain %q, got:\n%s", check, output)
		}
	}
}

func TestPodDryRun_PodScopedRole(t *testing.T) {
	h2Root := setupPodTestEnv(t)

	tmplContent := `pod_name: scope-test
agents:
  - name: agent-a
    role: special
  - name: agent-b
    role: default
`
	os.WriteFile(filepath.Join(h2Root, "pods", "templates", "scoped.yaml"), []byte(tmplContent), 0o644)

	// Pod-scoped role.
	podRoleContent := "name: special\ninstructions: |\n  Pod-scoped instructions.\n"
	os.WriteFile(filepath.Join(h2Root, "pods", "roles", "special.yaml"), []byte(podRoleContent), 0o644)

	// Global role.
	globalRoleContent := "name: default\ninstructions: |\n  Global instructions.\n"
	os.WriteFile(filepath.Join(h2Root, "roles", "default.yaml"), []byte(globalRoleContent), 0o644)

	output := captureStdout(func() {
		cmd := newPodLaunchCmd()
		cmd.SetArgs([]string{"--dry-run", "scoped"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// agent-a should show pod scope, agent-b should show global scope.
	// Split output by agent sections.
	sections := strings.Split(output, "--- Agent")
	if len(sections) < 3 {
		t.Fatalf("expected 2 agent sections, got %d sections", len(sections)-1)
	}

	agentASection := sections[1]
	agentBSection := sections[2]

	if !strings.Contains(agentASection, "Role Scope: pod") {
		t.Errorf("agent-a should have pod role scope, got:\n%s", agentASection)
	}
	if !strings.Contains(agentBSection, "Role Scope: global") {
		t.Errorf("agent-b should have global role scope, got:\n%s", agentBSection)
	}
}

func TestPodDryRun_WithVars(t *testing.T) {
	h2Root := setupPodTestEnv(t)

	tmplContent := `pod_name: var-pod
agents:
  - name: worker
    role: default
    vars:
      team: backend
`
	os.WriteFile(filepath.Join(h2Root, "pods", "templates", "withvars.yaml"), []byte(tmplContent), 0o644)

	roleContent := "name: default\ninstructions: |\n  Do work.\n"
	os.WriteFile(filepath.Join(h2Root, "roles", "default.yaml"), []byte(roleContent), 0o644)

	output := captureStdout(func() {
		cmd := newPodLaunchCmd()
		cmd.SetArgs([]string{"--dry-run", "--var", "env=prod", "withvars"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// Should show merged vars: team from template, env from CLI.
	checks := []string{
		"Variables:",
		"team=backend",
		"env=prod",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output should contain %q, got:\n%s", check, output)
		}
	}
}

// --- h2 run --dry-run end-to-end tests ---

func TestRunDryRun_WithVarFlags(t *testing.T) {
	h2Root := setupPodTestEnv(t)

	// Create a role with template variables in instructions.
	roleContent := `name: configurable
instructions: |
  You are working on the {{ .Var.project }} project.
  Environment: {{ .Var.env }}
`
	os.WriteFile(filepath.Join(h2Root, "roles", "configurable.yaml"), []byte(roleContent), 0o644)

	output := captureStdout(func() {
		cmd := newRunCmd()
		cmd.SetArgs([]string{"--dry-run", "--role", "configurable", "--name", "test-agent", "--var", "project=h2", "--var", "env=staging"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	checks := []string{
		"Agent: test-agent",
		"Role: configurable",
		"h2 project",     // template var resolved in instructions
		"Environment: staging", // template var resolved in instructions
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output should contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunDryRun_WithOverride(t *testing.T) {
	h2Root := setupPodTestEnv(t)

	roleContent := "name: overridable\nmodel: haiku\ninstructions: |\n  Test.\n"
	os.WriteFile(filepath.Join(h2Root, "roles", "overridable.yaml"), []byte(roleContent), 0o644)

	output := captureStdout(func() {
		cmd := newRunCmd()
		cmd.SetArgs([]string{"--dry-run", "--role", "overridable", "--name", "test-agent", "--override", "model=opus"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	checks := []string{
		"Agent: test-agent",
		"Model: opus",      // overridden
		"Overrides: model=opus",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output should contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunDryRun_WithPodEnvVars(t *testing.T) {
	h2Root := setupPodTestEnv(t)

	roleContent := "name: default\ninstructions: |\n  Work.\n"
	os.WriteFile(filepath.Join(h2Root, "roles", "default.yaml"), []byte(roleContent), 0o644)

	output := captureStdout(func() {
		cmd := newRunCmd()
		cmd.SetArgs([]string{"--dry-run", "--role", "default", "--name", "test-agent", "--pod", "my-pod"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if !strings.Contains(output, "H2_POD=my-pod") {
		t.Errorf("output should contain H2_POD=my-pod, got:\n%s", output)
	}
}

func TestRunDryRun_NoSideEffects(t *testing.T) {
	h2Root := setupPodTestEnv(t)

	roleContent := "name: default\ninstructions: |\n  Work.\n"
	os.WriteFile(filepath.Join(h2Root, "roles", "default.yaml"), []byte(roleContent), 0o644)

	// Record state before dry-run.
	sessionsDir := filepath.Join(h2Root, "sessions")
	entriesBefore, _ := os.ReadDir(sessionsDir)

	_ = captureStdout(func() {
		cmd := newRunCmd()
		cmd.SetArgs([]string{"--dry-run", "--role", "default", "--name", "no-side-effects"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// Verify no session directory was created.
	entriesAfter, _ := os.ReadDir(sessionsDir)
	if len(entriesAfter) != len(entriesBefore) {
		t.Errorf("dry-run created session dir entries: before=%d, after=%d", len(entriesBefore), len(entriesAfter))
	}

	// Verify no socket was created.
	socketsDir := filepath.Join(h2Root, "sockets")
	socketEntries, _ := os.ReadDir(socketsDir)
	for _, entry := range socketEntries {
		if strings.Contains(entry.Name(), "no-side-effects") {
			t.Errorf("dry-run created a socket file: %s", entry.Name())
		}
	}
}

func TestRunDryRun_RequiresRole(t *testing.T) {
	setupPodTestEnv(t)

	cmd := newRunCmd()
	cmd.SetArgs([]string{"--dry-run", "--agent-type", "claude"})
	err := cmd.Execute()

	if err == nil {
		t.Fatal("expected error when using --dry-run without a role")
	}
	if !strings.Contains(err.Error(), "--dry-run requires a role") {
		t.Errorf("expected error about --dry-run requiring a role, got: %v", err)
	}
}

func TestRunDryRun_InvalidDefaultRoleShowsValidationError(t *testing.T) {
	h2Root := setupPodTestEnv(t)

	// Create an invalid default role (missing both instructions and system_prompt).
	roleContent := "name: default\ndescription: broken role\n"
	os.WriteFile(filepath.Join(h2Root, "roles", "default.yaml"), []byte(roleContent), 0o644)

	cmd := newRunCmd()
	cmd.SetArgs([]string{"--dry-run"})
	err := cmd.Execute()

	if err == nil {
		t.Fatal("expected error for invalid default role")
	}
	// Should show the validation error, NOT the generic "no default role found" message.
	if strings.Contains(err.Error(), "no default role found") {
		t.Errorf("should show validation error, not 'no default role found', got: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid role") {
		t.Errorf("should contain validation error details, got: %v", err)
	}
}

func TestRunDryRun_MissingDefaultRoleShowsFriendlyMessage(t *testing.T) {
	setupPodTestEnv(t)

	// No role files created — default role doesn't exist.
	cmd := newRunCmd()
	cmd.SetArgs([]string{"--dry-run"})
	err := cmd.Execute()

	if err == nil {
		t.Fatal("expected error for missing default role")
	}
	if !strings.Contains(err.Error(), "no default role found") {
		t.Errorf("should show friendly 'no default role found' message, got: %v", err)
	}
}

// capturePrintDryRun captures stdout from printDryRun.
func capturePrintDryRun(rc *ResolvedAgentConfig) string {
	return captureStdout(func() {
		printDryRun(rc)
	})
}

// captureStdout captures stdout from a function call.
func captureStdout(fn func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}
