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
