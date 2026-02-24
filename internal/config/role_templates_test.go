package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRoleTemplate_Default(t *testing.T) {
	tmpl := RoleTemplate("default")
	if tmpl == "" {
		t.Fatal("default template is empty")
	}
	// Should contain key role fields (template markers are fine).
	if !strings.Contains(tmpl, "role_name:") {
		t.Error("default template missing role_name field")
	}
	if !strings.Contains(tmpl, "instructions_intro:") {
		t.Error("default template missing instructions_intro field")
	}
	if !strings.Contains(tmpl, "agent_harness") {
		t.Error("default template missing agent_harness field")
	}
	if !strings.Contains(tmpl, "variables:") {
		t.Error("default template missing variables section")
	}
}

func TestRoleTemplate_Concierge(t *testing.T) {
	tmpl := RoleTemplate("concierge")
	if tmpl == "" {
		t.Fatal("concierge template is empty")
	}
	if !strings.Contains(tmpl, "role_name:") {
		t.Error("concierge template missing role_name field")
	}
	if !strings.Contains(tmpl, "concierge") {
		t.Error("concierge template should mention 'concierge'")
	}
}

func TestRoleTemplate_Unknown_FallsBackToDefault(t *testing.T) {
	tmpl := RoleTemplate("nonexistent-role-xyz")
	defaultTmpl := RoleTemplate("default")
	if tmpl != defaultTmpl {
		t.Error("unknown role name should fall back to default template")
	}
}

func TestRoleTemplate_Default_IsValidYAMLAfterStubbing(t *testing.T) {
	// The template uses Go template syntax, so we can't parse it directly.
	// But we can verify that the variables section is valid YAML.
	tmpl := RoleTemplate("default")
	// Extract just the variables section.
	lines := strings.Split(tmpl, "\n")
	var varsLines []string
	inVars := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "variables:" {
			inVars = true
			varsLines = append(varsLines, line)
			continue
		}
		if inVars {
			if trimmed == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				varsLines = append(varsLines, line)
			} else {
				break
			}
		}
	}
	if len(varsLines) == 0 {
		t.Fatal("could not find variables section in template")
	}
	varsYAML := strings.Join(varsLines, "\n")
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(varsYAML), &parsed); err != nil {
		t.Fatalf("variables section is not valid YAML: %v", err)
	}
}

func TestInstructionsTemplate(t *testing.T) {
	content := InstructionsTemplate()
	if content == "" {
		t.Fatal("instructions template is empty")
	}
	if !strings.Contains(content, "h2 Messaging Protocol") {
		t.Error("instructions template missing h2 Messaging Protocol section")
	}
	if !strings.Contains(content, "h2 send") {
		t.Error("instructions template missing h2 send command")
	}
	if !strings.Contains(content, "h2 list") {
		t.Error("instructions template missing h2 list command")
	}
}
