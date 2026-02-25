package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"h2/internal/config"
	"h2/internal/tmpl"
)

func TestRoleTemplate_UsesTemplateSyntax(t *testing.T) {
	for _, name := range []string{"default", "concierge", "custom"} {
		tmplText := config.RoleTemplate(name)

		if !strings.Contains(tmplText, "{{ .RoleName }}") {
			t.Errorf("roleTemplate(%q): should contain {{ .RoleName }}", name)
		}
		if !strings.Contains(tmplText, "{{ .H2Dir }}") {
			t.Errorf("roleTemplate(%q): should contain {{ .H2Dir }}", name)
		}
		// Should not contain old fmt.Sprintf placeholders.
		if strings.Contains(tmplText, "%s") || strings.Contains(tmplText, "%v") {
			t.Errorf("roleTemplate(%q): should not contain %%s or %%v placeholders", name)
		}
	}
}

// stubNameFuncs returns template functions that stub out randomName and autoIncrement
// for testing purposes.
func stubNameFuncs() template.FuncMap {
	return template.FuncMap{
		"randomName":    func() string { return "test-agent" },
		"autoIncrement": func(prefix string) int { return 1 },
	}
}

func TestRoleTemplate_ValidGoTemplate(t *testing.T) {
	// Generated role templates must be renderable with variables and name functions.
	for _, name := range []string{"default", "concierge"} {
		tmplText := config.RoleTemplate(name)

		// Parse out variables section and set defaults (mimics LoadRoleRendered flow).
		defs, remaining, err := tmpl.ParseVarDefs(tmplText)
		if err != nil {
			t.Fatalf("ParseVarDefs(%q): %v", name, err)
		}
		vars := make(map[string]string)
		for vName, def := range defs {
			if def.Default != nil {
				vars[vName] = *def.Default
			}
		}

		ctx := &tmpl.Context{
			RoleName:  name,
			AgentName: "test-agent",
			H2Dir:     "/tmp/test-h2",
			Var:       vars,
		}

		rendered, err := tmpl.RenderWithExtraFuncs(remaining, ctx, stubNameFuncs())
		if err != nil {
			t.Fatalf("roleTemplate(%q): Render failed: %v", name, err)
		}
		// Name may be quoted in the YAML template: name: "default"
		if !strings.Contains(rendered, name) {
			t.Errorf("roleTemplate(%q): rendered should contain '%s'", name, name)
		}
	}
}

func TestRoleTemplate_RenderedIsValidRole(t *testing.T) {
	// After rendering via LoadRoleWithNameResolution, the output should be a valid Role.
	for _, name := range []string{"default", "concierge"} {
		tmplText := config.RoleTemplate(name)

		// Write template to temp file and load via LoadRoleWithNameResolution.
		path := filepath.Join(t.TempDir(), name+".yaml")
		if err := os.WriteFile(path, []byte(tmplText), 0o644); err != nil {
			t.Fatal(err)
		}

		ctx := &tmpl.Context{
			RoleName: name,
			H2Dir:    "/tmp/test-h2",
		}
		role, _, err := config.LoadRoleWithNameResolution(
			path, ctx, stubNameFuncs(), "", func() string { return "fallback-agent" },
		)
		if err != nil {
			t.Fatalf("LoadRoleWithNameResolution %q: %v", name, err)
		}
		if role.RoleName != name {
			t.Errorf("role.RoleName = %q, want %q", role.RoleName, name)
		}
	}
}

// findRoleFile locates a role file by name, checking both .yaml.tmpl and .yaml extensions.
func findRoleFile(t *testing.T, rolesDir, name string) string {
	t.Helper()
	for _, ext := range []string{".yaml.tmpl", ".yaml"} {
		p := filepath.Join(rolesDir, name+ext)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Fatalf("role file %q not found in %s", name, rolesDir)
	return ""
}

// setupRoleTestH2Dir creates a temp h2 directory, sets H2_DIR to point at it,
// and resets the resolve cache so ConfigDir() picks it up.
func setupRoleTestH2Dir(t *testing.T) string {
	t.Helper()

	config.ResetResolveCache()
	t.Cleanup(config.ResetResolveCache)

	h2Dir := filepath.Join(t.TempDir(), "myh2")
	for _, sub := range []string{"roles", "sessions", "sockets", "claude-config/default", "codex-config/default"} {
		if err := os.MkdirAll(filepath.Join(h2Dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.WriteMarker(h2Dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("H2_DIR", h2Dir)

	return h2Dir
}

func TestRoleInitCmd_GeneratesTemplateFile(t *testing.T) {
	setupRoleTestH2Dir(t)

	cmd := newRoleInitCmd()
	cmd.SetArgs([]string{"default"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("role init failed: %v", err)
	}

	// The generated file should contain template syntax, not resolved values.
	// Templates with {{ }} syntax get written as .yaml.tmpl.
	h2Dir := config.ConfigDir()
	path := findRoleFile(t, filepath.Join(h2Dir, "roles"), "default")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated role: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "{{ .RoleName }}") {
		t.Error("generated role should contain {{ .RoleName }}")
	}
	if !strings.Contains(content, "{{ .H2Dir }}") {
		t.Error("generated role should contain {{ .H2Dir }}")
	}
}

func TestRoleInitCmd_ConciergeGeneratesTemplateFile(t *testing.T) {
	setupRoleTestH2Dir(t)

	cmd := newRoleInitCmd()
	cmd.SetArgs([]string{"concierge"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("role init concierge failed: %v", err)
	}

	h2Dir := config.ConfigDir()
	path := findRoleFile(t, filepath.Join(h2Dir, "roles"), "concierge")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated role: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "{{ .RoleName }}") {
		t.Error("generated concierge role should contain {{ .RoleName }}")
	}
	if !strings.Contains(content, "{{ .H2Dir }}") {
		t.Error("generated concierge role should contain {{ .H2Dir }}")
	}
}

func TestRoleInitCmd_RefusesOverwrite(t *testing.T) {
	h2Dir := setupRoleTestH2Dir(t)

	// Create a role file first.
	rolePath := filepath.Join(h2Dir, "roles", "default.yaml")
	if err := os.WriteFile(rolePath, []byte("role_name: default\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newRoleInitCmd()
	cmd.SetArgs([]string{"default"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when role already exists")
	}
}

func TestRoleInitThenList_ShowsRole(t *testing.T) {
	setupRoleTestH2Dir(t)

	cmd := newRoleInitCmd()
	cmd.SetArgs([]string{"default"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("role init failed: %v", err)
	}

	roles, err := config.ListRoles()
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	if len(roles) == 0 {
		t.Fatal("expected at least one role, got none")
	}

	found := false
	for _, r := range roles {
		if r.RoleName == "default" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected role named 'default' in list, got: %v", roles)
	}
}

func TestRoleShowCmd_PlainYAML(t *testing.T) {
	h2Dir := setupRoleTestH2Dir(t)

	roleContent := `role_name: simple
description: A simple role
agent_model: sonnet
instructions: |
  You are a simple agent.
`
	os.WriteFile(filepath.Join(h2Dir, "roles", "simple.yaml"), []byte(roleContent), 0o644)

	output := captureStdout(func() {
		cmd := newRoleShowCmd()
		cmd.SetArgs([]string{"simple"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("role show failed: %v", err)
		}
	})

	checks := []string{
		"Role:        simple",
		"Model:       sonnet",
		"Description: A simple role",
		"You are a simple agent.",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output should contain %q, got:\n%s", check, output)
		}
	}
	// No Variables section for a role without variables.
	if strings.Contains(output, "Variables:") {
		t.Errorf("output should not contain Variables section, got:\n%s", output)
	}
}

func TestRoleShowCmd_TemplateFile(t *testing.T) {
	h2Dir := setupRoleTestH2Dir(t)

	roleContent := `variables:
  agent_harness:
    description: "Agent harness to use"
    default: "claude_code"
  agent_model:
    description: "Agent model to use"
    default: "sonnet"
  working_dir:
    description: "Working directory"
    default: "."

role_name: {{ .RoleName }}
description: A default agent
agent_harness: {{ .Var.agent_harness }}
agent_model: {{ .Var.agent_model }}
working_dir: {{ .Var.working_dir }}
instructions: |
  You are {{ .AgentName }}.
`
	os.WriteFile(filepath.Join(h2Dir, "roles", "default.yaml.tmpl"), []byte(roleContent), 0o644)

	output := captureStdout(func() {
		cmd := newRoleShowCmd()
		cmd.SetArgs([]string{"default"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("role show failed: %v", err)
		}
	})

	// Should render the template and show role info.
	checks := []string{
		"Role:        default",
		"Description: A default agent",
		"Model:       sonnet",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output should contain %q, got:\n%s", check, output)
		}
	}

	// Should show Variables section.
	varChecks := []string{
		"Variables:",
		"agent_harness",
		`"Agent harness to use"`,
		`(default: "claude_code")`,
		"agent_model",
		`"Agent model to use"`,
		"working_dir",
		`(default: ".")`,
	}
	for _, check := range varChecks {
		if !strings.Contains(output, check) {
			t.Errorf("output should contain %q, got:\n%s", check, output)
		}
	}
}

func TestRoleShowCmd_VariablesSection(t *testing.T) {
	h2Dir := setupRoleTestH2Dir(t)

	roleContent := `variables:
  team:
    description: "Team name"
  env:
    description: "Environment"
    default: "dev"

role_name: testrole
instructions: |
  Team: {{ .Var.team }}, Env: {{ .Var.env }}
`
	os.WriteFile(filepath.Join(h2Dir, "roles", "testrole.yaml.tmpl"), []byte(roleContent), 0o644)

	output := captureStdout(func() {
		cmd := newRoleShowCmd()
		cmd.SetArgs([]string{"testrole"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("role show failed: %v", err)
		}
	})

	// Required var should show "(required)".
	if !strings.Contains(output, "(required)") {
		t.Errorf("output should show (required) for team var, got:\n%s", output)
	}
	// Optional var should show default.
	if !strings.Contains(output, `(default: "dev")`) {
		t.Errorf("output should show default value for env var, got:\n%s", output)
	}
}

func TestRoleListCmd_ShowsVariableCount(t *testing.T) {
	h2Dir := setupRoleTestH2Dir(t)

	// Role with 2 variables.
	roleContent := `variables:
  team:
    description: "Team"
  env:
    description: "Env"
    default: "dev"

role_name: configurable
description: A configurable role
instructions: |
  Team: {{ .Var.team }}
`
	os.WriteFile(filepath.Join(h2Dir, "roles", "configurable.yaml.tmpl"), []byte(roleContent), 0o644)

	// Role with 1 variable.
	roleContent2 := `variables:
  mode:
    description: "Mode"
    default: "normal"

role_name: simple
description: A simple role
instructions: |
  Mode: {{ .Var.mode }}
`
	os.WriteFile(filepath.Join(h2Dir, "roles", "simple.yaml.tmpl"), []byte(roleContent2), 0o644)

	// Plain role with no variables.
	roleContent3 := `role_name: plain
description: A plain role
instructions: |
  Hello.
`
	os.WriteFile(filepath.Join(h2Dir, "roles", "plain.yaml"), []byte(roleContent3), 0o644)

	output := captureStdout(func() {
		cmd := newRoleListCmd()
		if err := cmd.Execute(); err != nil {
			t.Fatalf("role list failed: %v", err)
		}
	})

	// Check variable counts.
	if !strings.Contains(output, "(2 variables)") {
		t.Errorf("output should contain '(2 variables)', got:\n%s", output)
	}
	if !strings.Contains(output, "(1 variable)") {
		t.Errorf("output should contain '(1 variable)', got:\n%s", output)
	}
	// Plain role should NOT have variable info.
	plainLine := ""
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "plain") {
			plainLine = line
			break
		}
	}
	if strings.Contains(plainLine, "variable") {
		t.Errorf("plain role should not show variable count, got line: %s", plainLine)
	}
}

func TestRoleListCmd_ShowsInheritanceParent(t *testing.T) {
	h2Dir := setupRoleTestH2Dir(t)

	os.WriteFile(filepath.Join(h2Dir, "roles", "base.yaml"), []byte(`
role_name: base
description: Base role
instructions: base
`), 0o644)
	os.WriteFile(filepath.Join(h2Dir, "roles", "child.yaml"), []byte(`
role_name: child
inherits: base
description: Child role
instructions: child
`), 0o644)

	output := captureStdout(func() {
		cmd := newRoleListCmd()
		if err := cmd.Execute(); err != nil {
			t.Fatalf("role list failed: %v", err)
		}
	})
	if !strings.Contains(output, "child") || !strings.Contains(output, "(inherits: base)") {
		t.Fatalf("list output should include inheritance marker for child, got:\n%s", output)
	}
}

func TestRoleShowCmd_ShowsInheritanceAndVariableOrigins(t *testing.T) {
	h2Dir := setupRoleTestH2Dir(t)

	os.WriteFile(filepath.Join(h2Dir, "roles", "base.yaml.tmpl"), []byte(`
role_name: base
variables:
  team:
    description: "Team"
  model:
    description: "Model"
    default: "sonnet"
instructions: |
  Base {{ .Var.team }} {{ .Var.model }}
`), 0o644)
	os.WriteFile(filepath.Join(h2Dir, "roles", "child.yaml.tmpl"), []byte(`
role_name: child
inherits: base
variables:
  team:
    description: "Team"
    default: "platform"
  service:
    description: "Service"
instructions: |
  Child {{ .Var.service }}
`), 0o644)

	output := captureStdout(func() {
		cmd := newRoleShowCmd()
		cmd.SetArgs([]string{"child"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("role show failed: %v", err)
		}
	})

	checks := []string{
		"Role:        child",
		"Inherits:    base",
		"Chain:       base -> child",
		"Variables:",
		"team",
		"[from: child]",
		"service",
		"Inherited Variables (hidden from child contract):",
		"model",
		"[from: base]",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("output should contain %q, got:\n%s", check, output)
		}
	}
}

func TestRoleCheckCmd_TemplateFile(t *testing.T) {
	h2Dir := setupRoleTestH2Dir(t)

	roleContent := `variables:
  env:
    description: "Environment"
    default: "dev"

role_name: checkme
instructions: |
  Env: {{ .Var.env }}
`
	os.WriteFile(filepath.Join(h2Dir, "roles", "checkme.yaml.tmpl"), []byte(roleContent), 0o644)

	cmd := newRoleCheckCmd()
	cmd.SetArgs([]string{"checkme"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("role check should succeed for template file: %v", err)
	}
}

func TestRoleCheckCmd_ValidatesInheritanceChain(t *testing.T) {
	h2Dir := setupRoleTestH2Dir(t)

	os.WriteFile(filepath.Join(h2Dir, "roles", "base.yaml"), []byte(`
role_name: base
instructions: base
`), 0o644)
	os.WriteFile(filepath.Join(h2Dir, "roles", "child.yaml"), []byte(`
role_name: child
inherits: base
instructions: child
`), 0o644)

	output := captureStdout(func() {
		cmd := newRoleCheckCmd()
		cmd.SetArgs([]string{"child"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("role check should succeed: %v", err)
		}
	})
	if !strings.Contains(output, "Inherits:    base") || !strings.Contains(output, "Chain:       base -> child") {
		t.Fatalf("check output should include inheritance info, got:\n%s", output)
	}
}

func TestRoleCheckCmd_InheritanceErrorActionable(t *testing.T) {
	h2Dir := setupRoleTestH2Dir(t)

	os.WriteFile(filepath.Join(h2Dir, "roles", "child.yaml"), []byte(`
role_name: child
inherits: missing-parent
instructions: child
`), 0o644)

	cmd := newRoleCheckCmd()
	cmd.SetArgs([]string{"child"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected inheritance validation error")
	}
	if !strings.Contains(err.Error(), "inheritance validation failed") {
		t.Fatalf("error should be actionable, got: %v", err)
	}
	if !strings.Contains(err.Error(), "missing-parent") {
		t.Fatalf("error should include missing parent name, got: %v", err)
	}
}

func TestOldDollarBraceRolesStillLoad(t *testing.T) {
	// Old roles with ${name} syntax should load fine — ${name} is just literal text.
	yamlContent := `
role_name: old-style
instructions: |
  You are ${name}, a ${name} agent.
`
	path := filepath.Join(t.TempDir(), "old-style.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	role, err := config.LoadRoleFrom(path)
	if err != nil {
		t.Fatalf("LoadRoleFrom: %v", err)
	}
	if !strings.Contains(role.Instructions, "${name}") {
		t.Error("old ${name} syntax should appear literally in instructions")
	}
}
