package config

import (
	"embed"
	"fmt"
	"strings"
)

const (
	templateStyleOpinionated = "opinionated"
	templateStyleMinimal     = "minimal"
)

//go:embed templates/**
var Templates embed.FS

// RoleTemplate returns the embedded YAML template for the given role name.
// Falls back to "default" if no specific template exists for the name.
func RoleTemplate(name string) string {
	return RoleTemplateWithStyle(name, templateStyleOpinionated)
}

// RoleTemplateWithStyle returns the embedded YAML template for the given role
// and style. Unknown role names fall back to default role within the same
// style; unknown styles fall back to opinionated.
func RoleTemplateWithStyle(name, style string) string {
	path := fmt.Sprintf("templates/styles/%s/roles/%s.yaml.tmpl", normalizeTemplateStyle(style), name)
	data, err := Templates.ReadFile(path)
	if err != nil {
		// Fall back to default template.
		data, err = Templates.ReadFile(fmt.Sprintf("templates/styles/%s/roles/default.yaml.tmpl", normalizeTemplateStyle(style)))
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
	return InstructionsTemplateWithStyle(templateStyleOpinionated)
}

// InstructionsTemplateWithStyle returns style-specific shared instructions.
// Unknown styles fall back to opinionated.
func InstructionsTemplateWithStyle(style string) string {
	data, err := Templates.ReadFile(fmt.Sprintf("templates/styles/%s/CLAUDE_AND_AGENTS.md", normalizeTemplateStyle(style)))
	if err != nil {
		panic(fmt.Sprintf("embedded CLAUDE_AND_AGENTS.md missing: %v", err))
	}
	return string(data)
}

// ConfigTemplate returns the style-specific config.yaml template.
// Unknown styles fall back to opinionated.
func ConfigTemplate(style string) string {
	data, err := Templates.ReadFile(fmt.Sprintf("templates/styles/%s/config.yaml", normalizeTemplateStyle(style)))
	if err != nil {
		panic(fmt.Sprintf("embedded config.yaml missing for style %q: %v", style, err))
	}
	return string(data)
}

func normalizeTemplateStyle(style string) string {
	switch strings.TrimSpace(strings.ToLower(style)) {
	case templateStyleMinimal:
		return templateStyleMinimal
	default:
		return templateStyleOpinionated
	}
}
