package tmpl

import (
	"fmt"
	"sort"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// VarDef defines a template variable with optional default.
// Default is a pointer: nil means "required" (no default), non-nil means "optional".
type VarDef struct {
	Description string  `yaml:"description"`
	Default     *string `yaml:"default"`
}

// Required returns true if the variable has no default value.
func (v VarDef) Required() bool {
	return v.Default == nil
}

// Context holds all template data available during rendering.
type Context struct {
	AgentName string
	RoleName  string
	PodName   string
	Index     int
	Count     int
	H2Dir     string
	H2RootDir string
	Var       map[string]string
}

// Render processes a template string with the given context.
// Returns the rendered string or an error with source context.
func Render(templateText string, ctx *Context) (string, error) {
	t, err := template.New("").Funcs(funcMap()).Parse(templateText)
	if err != nil {
		return "", fmt.Errorf("template parse error: %w", err)
	}

	var buf strings.Builder
	if err := t.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("template execution error: %w", err)
	}
	return buf.String(), nil
}

// ParseVarDefs extracts variable definitions from raw YAML text.
// Returns the definitions and the YAML text with the variables section removed.
// The variables section must not contain template expressions.
//
// This uses string-based extraction (not full YAML parsing) so the rest of the
// document can contain template expressions like {{ if }} that would break YAML syntax.
func ParseVarDefs(yamlText string) (map[string]VarDef, string, error) {
	// Extract just the variables block as raw text.
	varsBlock, remaining := extractYAMLSection(yamlText, "variables")
	if varsBlock == "" {
		return nil, yamlText, nil
	}

	// Parse only the extracted variables block.
	// Wrap it back with the "variables:" key so it's valid YAML.
	varsYAML := "variables:\n" + varsBlock
	var wrapper struct {
		Variables map[string]VarDef `yaml:"variables"`
	}
	if err := yaml.Unmarshal([]byte(varsYAML), &wrapper); err != nil {
		return nil, "", fmt.Errorf("parse variables section: %w", err)
	}
	defs := wrapper.Variables
	if defs == nil {
		defs = map[string]VarDef{}
	}

	return defs, remaining, nil
}

// extractYAMLSection finds a top-level YAML key, extracts its indented block,
// and returns (block text, remaining text without the section).
// Returns ("", original text) if the key is not found.
func extractYAMLSection(yamlText string, key string) (string, string) {
	lines := strings.Split(yamlText, "\n")
	var remaining []string
	var block []string
	inSection := false
	prefix := key + ":"

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if !inSection {
			if trimmed == prefix || strings.HasPrefix(trimmed, prefix+" ") {
				inSection = true
				// Check for inline value (e.g., "variables: {}")
				after := strings.TrimPrefix(trimmed, prefix)
				after = strings.TrimSpace(after)
				if after != "" {
					// Inline value — treat entire value as block.
					block = append(block, "  "+after)
				}
				continue
			}
			remaining = append(remaining, line)
		} else {
			if trimmed == "" {
				// Blank lines inside the section are consumed.
				block = append(block, line)
				continue
			}
			if line != trimmed {
				// Line has leading whitespace — still in section.
				block = append(block, line)
				continue
			}
			// Non-indented, non-blank line — section is over.
			inSection = false
			remaining = append(remaining, line)
		}
	}

	return strings.Join(block, "\n"), strings.Join(remaining, "\n")
}

// ValidateVars checks that all required variables (no default) are provided.
// Returns a descriptive error listing all missing variables with descriptions.
func ValidateVars(defs map[string]VarDef, provided map[string]string) error {
	var missing []string
	for name, def := range defs {
		if def.Required() {
			if _, ok := provided[name]; !ok {
				missing = append(missing, name)
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}

	sort.Strings(missing)

	var buf strings.Builder
	buf.WriteString("required variables not provided:\n\n")
	for _, name := range missing {
		desc := defs[name].Description
		if desc != "" {
			fmt.Fprintf(&buf, "  %-16s — %s\n", name, desc)
		} else {
			fmt.Fprintf(&buf, "  %s\n", name)
		}
	}
	buf.WriteString("\nProvide them with: --var ")
	for i, name := range missing {
		if i > 0 {
			buf.WriteString(" --var ")
		}
		fmt.Fprintf(&buf, "%s=VALUE", name)
	}
	return fmt.Errorf("%s", buf.String())
}

// funcMap returns the custom template functions.
func funcMap() template.FuncMap {
	return template.FuncMap{
		"seq":       seqFunc,
		"split":     splitFunc,
		"join":      joinFunc,
		"default":   defaultFunc,
		"upper":     strings.ToUpper,
		"lower":     strings.ToLower,
		"contains":  strings.Contains,
		"trimSpace": strings.TrimSpace,
		"quote":     quoteFunc,
	}
}

// seqFunc generates an integer sequence [start, end] inclusive.
// Returns an error if the range exceeds 1000 elements.
func seqFunc(start, end int) ([]int, error) {
	if start > end {
		return nil, nil
	}
	count := end - start + 1
	if count > 1000 {
		return nil, fmt.Errorf("seq range too large: %d elements (max 1000)", count)
	}
	result := make([]int, count)
	for i := range result {
		result[i] = start + i
	}
	return result, nil
}

func splitFunc(s, sep string) []string {
	return strings.Split(s, sep)
}

func joinFunc(elems []string, sep string) string {
	return strings.Join(elems, sep)
}

// defaultFunc returns val if non-empty, otherwise fallback.
// String semantics: "0" and "false" are non-empty.
func defaultFunc(val, fallback string) string {
	if val != "" {
		return val
	}
	return fallback
}

// quoteFunc returns a YAML-safe quoted string.
func quoteFunc(s string) string {
	// Use Go %q which produces a double-quoted string with proper escaping.
	return fmt.Sprintf("%q", s)
}
