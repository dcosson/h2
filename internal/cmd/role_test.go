package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2/internal/config"
	"h2/internal/tmpl"
)

func TestRoleTemplate_UsesTemplateSyntax(t *testing.T) {
	for _, name := range []string{"default", "concierge", "custom"} {
		tmplText := roleTemplate(name)

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

func TestRoleTemplate_ValidGoTemplate(t *testing.T) {
	// Generated role templates must be renderable by tmpl.Render.
	for _, name := range []string{"default", "concierge"} {
		tmplText := roleTemplate(name)
		ctx := &tmpl.Context{
			RoleName: name,
			H2Dir:    "/tmp/test-h2",
		}

		rendered, err := tmpl.Render(tmplText, ctx)
		if err != nil {
			t.Fatalf("roleTemplate(%q): Render failed: %v", name, err)
		}
		if !strings.Contains(rendered, "name: "+name) {
			t.Errorf("roleTemplate(%q): rendered should contain 'name: %s'", name, name)
		}
		if !strings.Contains(rendered, "/tmp/test-h2/claude-config/default") {
			t.Errorf("roleTemplate(%q): rendered should contain resolved claude_config_dir", name)
		}
	}
}

func TestRoleTemplate_RenderedIsValidRole(t *testing.T) {
	// After rendering, the output should be loadable as a valid Role.
	for _, name := range []string{"default", "concierge"} {
		tmplText := roleTemplate(name)
		ctx := &tmpl.Context{
			RoleName: name,
			H2Dir:    "/tmp/test-h2",
		}

		rendered, err := tmpl.Render(tmplText, ctx)
		if err != nil {
			t.Fatalf("Render(%q): %v", name, err)
		}

		// Write to temp file and load as role.
		path := filepath.Join(t.TempDir(), name+".yaml")
		if err := os.WriteFile(path, []byte(rendered), 0o644); err != nil {
			t.Fatal(err)
		}

		role, err := config.LoadRoleFrom(path)
		if err != nil {
			t.Fatalf("LoadRoleFrom rendered %q: %v", name, err)
		}
		if role.Name != name {
			t.Errorf("role.Name = %q, want %q", role.Name, name)
		}
		if role.ClaudeConfigDir != "/tmp/test-h2/claude-config/default" {
			t.Errorf("role.ClaudeConfigDir = %q, want %q", role.ClaudeConfigDir, "/tmp/test-h2/claude-config/default")
		}
	}
}

// setupRoleTestH2Dir creates a temp h2 directory, sets H2_DIR to point at it,
// and resets the resolve cache so ConfigDir() picks it up.
func setupRoleTestH2Dir(t *testing.T) string {
	t.Helper()

	config.ResetResolveCache()
	t.Cleanup(config.ResetResolveCache)

	h2Dir := filepath.Join(t.TempDir(), "myh2")
	for _, sub := range []string{"roles", "sessions", "sockets", "claude-config/default"} {
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
	h2Dir := config.ConfigDir()
	path := filepath.Join(h2Dir, "roles", "default.yaml")
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
	path := filepath.Join(h2Dir, "roles", "concierge.yaml")
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
	if err := os.WriteFile(rolePath, []byte("name: default\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newRoleInitCmd()
	cmd.SetArgs([]string{"default"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when role already exists")
	}
}

func TestOldDollarBraceRolesStillLoad(t *testing.T) {
	// Old roles with ${name} syntax should load fine — ${name} is just literal text.
	yamlContent := `
name: old-style
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
