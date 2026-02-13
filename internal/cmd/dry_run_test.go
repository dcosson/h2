package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
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
		Model:        "opus",
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
	if !strings.Contains(output, "<instructions: 3 lines>") {
		t.Errorf("Args should show truncated instructions placeholder, got:\n%s", output)
	}
}

// capturePrintDryRun captures stdout from printDryRun.
func capturePrintDryRun(rc *ResolvedAgentConfig) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printDryRun(rc)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}
