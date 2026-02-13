package cmd

import (
	"strings"
	"testing"
)

func TestGenerateOrchestratorRole_IncludesProtocol(t *testing.T) {
	role := GenerateOrchestratorRole("opus", "", "test plan content", "test-plan")

	// Should include all protocol sections.
	sections := []string{
		"Verification Toolkit",
		"Timeout Rules",
		"How to Test",
		"Result Format",
		"Cost Guidance",
		"metadata.json",
		"report.md",
	}
	for _, section := range sections {
		if !strings.Contains(role, section) {
			t.Errorf("orchestrator role should include %q section", section)
		}
	}
}

func TestGenerateOrchestratorRole_InjectsExtraInstructions(t *testing.T) {
	extra := "You are testing a web app.\nUse Playwright for browser testing."
	role := GenerateOrchestratorRole("opus", extra, "plan content", "web-test")

	if !strings.Contains(role, "You are testing a web app.") {
		t.Error("role should include extra_instructions content")
	}
	if !strings.Contains(role, "Use Playwright for browser testing.") {
		t.Error("role should include all extra_instructions lines")
	}
	if !strings.Contains(role, "Project-Specific Instructions") {
		t.Error("role should include Project-Specific Instructions header")
	}
}

func TestGenerateOrchestratorRole_InjectsTestPlan(t *testing.T) {
	plan := "# Test Plan: Messaging\n\n## TC-1: Send message\nSend a message and verify delivery."
	role := GenerateOrchestratorRole("opus", "", plan, "messaging")

	if !strings.Contains(role, "## Test Plan") {
		t.Error("role should include Test Plan header")
	}
	if !strings.Contains(role, "# Test Plan: Messaging") {
		t.Error("role should include test plan content")
	}
	if !strings.Contains(role, "TC-1: Send message") {
		t.Error("role should include test case content")
	}
}

func TestGenerateOrchestratorRole_NoExtraInstructions(t *testing.T) {
	role := GenerateOrchestratorRole("opus", "", "plan content", "test")

	if strings.Contains(role, "Project-Specific Instructions") {
		t.Error("role should not include Project-Specific Instructions when none provided")
	}
}

func TestGenerateOrchestratorRole_Model(t *testing.T) {
	role := GenerateOrchestratorRole("sonnet", "", "plan", "test")

	if !strings.Contains(role, "model: sonnet") {
		t.Error("role should include specified model")
	}
}

func TestGenerateOrchestratorRole_PermissionMode(t *testing.T) {
	role := GenerateOrchestratorRole("opus", "", "plan", "test")

	if !strings.Contains(role, "permission_mode: bypassPermissions") {
		t.Error("role should set permission_mode to bypassPermissions")
	}
}

func TestGenerateOrchestratorRole_YAMLStructure(t *testing.T) {
	role := GenerateOrchestratorRole("opus", "", "plan", "test")

	if !strings.HasPrefix(role, "name: qa-orchestrator") {
		t.Error("role YAML should start with name: qa-orchestrator")
	}
	if !strings.Contains(role, "instructions: |") {
		t.Error("role should use YAML block scalar for instructions")
	}
}

func TestIndentBlock(t *testing.T) {
	input := "line1\nline2\n\nline4"
	got := indentBlock(input, "  ")

	lines := strings.Split(got, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "  ") {
			t.Errorf("line %d should be indented: %q", i, line)
		}
	}
}
