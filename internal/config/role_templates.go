package config

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// ClaudeSettingsTemplate returns the style-specific Claude settings.json.
// Unknown styles fall back to opinionated.
func ClaudeSettingsTemplate(style string) string {
	data, err := Templates.ReadFile(fmt.Sprintf("templates/styles/%s/claude/settings.json", normalizeTemplateStyle(style)))
	if err != nil {
		panic(fmt.Sprintf("embedded claude settings.json missing for style %q: %v", style, err))
	}
	return string(data)
}

// CodexRequirementsTemplate returns the style-specific Codex requirements.toml.
// Unknown styles fall back to opinionated.
func CodexRequirementsTemplate(style string) string {
	data, err := Templates.ReadFile(fmt.Sprintf("templates/styles/%s/codex/requirements.toml", normalizeTemplateStyle(style)))
	if err != nil {
		panic(fmt.Sprintf("embedded codex requirements.toml missing for style %q: %v", style, err))
	}
	return string(data)
}

// WriteSkillsTemplate materializes the embedded style-specific skills template
// into targetDir. For minimal style, this intentionally results in an empty
// directory. If force is false and targetDir is non-empty, it leaves content
// unchanged.
func WriteSkillsTemplate(style, targetDir string, force bool) error {
	style = normalizeTemplateStyle(style)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("create skills target dir: %w", err)
	}

	if !force {
		if entries, err := os.ReadDir(targetDir); err == nil && len(entries) > 0 {
			return nil
		}
	}

	if force {
		entries, err := os.ReadDir(targetDir)
		if err != nil {
			return fmt.Errorf("read skills target dir: %w", err)
		}
		for _, e := range entries {
			if err := os.RemoveAll(filepath.Join(targetDir, e.Name())); err != nil {
				return fmt.Errorf("clear skills target dir: %w", err)
			}
		}
	}

	root := fmt.Sprintf("templates/styles/%s/skills", style)
	err := fs.WalkDir(Templates, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, root)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return nil
		}
		dst := filepath.Join(targetDir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if filepath.Base(dst) == ".gitkeep" {
			return nil
		}
		data, readErr := Templates.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// No skills template tree for this style (e.g., minimal empty skills).
			return nil
		}
		return fmt.Errorf("materialize skills template: %w", err)
	}
	return nil
}

func normalizeTemplateStyle(style string) string {
	switch strings.TrimSpace(strings.ToLower(style)) {
	case templateStyleMinimal:
		return templateStyleMinimal
	default:
		return templateStyleOpinionated
	}
}
