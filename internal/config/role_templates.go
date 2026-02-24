package config

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed templates/roles/default.yaml.tmpl templates/roles/concierge.yaml.tmpl templates/CLAUDE_AND_AGENTS.md
var Templates embed.FS

// RoleTemplate returns the embedded YAML template for the given role name.
// Falls back to "default" if no specific template exists for the name.
func RoleTemplate(name string) string {
	path := fmt.Sprintf("templates/roles/%s.yaml.tmpl", name)
	data, err := Templates.ReadFile(path)
	if err != nil {
		// Fall back to default template.
		data, err = Templates.ReadFile("templates/roles/default.yaml.tmpl")
		if err != nil {
			panic(fmt.Sprintf("embedded default role template missing: %v", err))
		}
	}
	return string(data)
}

// RoleFileExtension returns ".yaml.tmpl" if the content contains template syntax
// ({{ ), otherwise ".yaml".
func RoleFileExtension(content string) string {
	if strings.Contains(content, "{{") {
		return ".yaml.tmpl"
	}
	return ".yaml"
}

// InstructionsTemplate returns the embedded CLAUDE_AND_AGENTS.md content.
func InstructionsTemplate() string {
	data, err := Templates.ReadFile("templates/CLAUDE_AND_AGENTS.md")
	if err != nil {
		panic(fmt.Sprintf("embedded CLAUDE_AND_AGENTS.md missing: %v", err))
	}
	return string(data)
}
