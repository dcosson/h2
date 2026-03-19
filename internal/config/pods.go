package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"h2/internal/tmpl"

	"gopkg.in/yaml.v3"
)

var podNameRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// ValidatePodName checks that a pod name matches [a-z0-9-]+.
func ValidatePodName(name string) error {
	if !podNameRe.MatchString(name) {
		return fmt.Errorf("invalid pod name %q: must match [a-z0-9-]+", name)
	}
	return nil
}

// PodDir returns <h2-dir>/pods/.
func PodDir() string {
	return filepath.Join(ConfigDir(), "pods")
}

// PodTemplate defines a set of agents and bridges to launch together.
type PodTemplate struct {
	PodName   string                 `yaml:"pod_name"`
	Variables map[string]tmpl.VarDef `yaml:"variables"`
	Bridges   []PodBridge            `yaml:"bridges"`
	Agents    []PodTemplateAgent     `yaml:"agents"`
}

// PodBridge links a named bridge config to a concierge agent in the pod.
type PodBridge struct {
	Bridge    string `yaml:"bridge"`    // key into config.yaml bridges map
	Concierge string `yaml:"concierge"` // agent name in this pod; empty = no concierge
}

// PodTemplateAgent defines a single agent within a pod.
type PodTemplateAgent struct {
	Name      string            `yaml:"name"`
	Role      string            `yaml:"role"`
	Count     *int              `yaml:"count,omitempty"` // nil = default (1 agent), 0 = skip, N = N agents
	Vars      map[string]string `yaml:"vars"`
	Overrides map[string]string `yaml:"overrides"` // role field overrides (yaml tag = value)
}

// GetCount returns the effective count for this agent.
// nil (not specified) defaults to 1. Explicit 0 means skip.
func (a PodTemplateAgent) GetCount() int {
	if a.Count == nil {
		return 1
	}
	return *a.Count
}

// ExpandedAgent is a fully resolved agent after count expansion.
type ExpandedAgent struct {
	Name      string
	Role      string
	Index     int
	Count     int
	Vars      map[string]string
	Overrides map[string]string
}

// ExpandPodAgents expands count groups in a pod template into a flat list of agents.
// It handles count-based multiplication, auto-suffix for names without {{ .Index }},
// and detects name collisions after expansion.
//
// Count semantics:
//   - count omitted (nil): produce 1 agent with Index=0, Count=0
//   - count == 0: skip (produce 0 agents)
//   - count == 1 with template expressions in name: render with Index=1, Count=1
//   - count > 1: expand to N agents with Index=1..N, Count=N
//   - count < 0: treated as default (1 agent)
func ExpandPodAgents(pt *PodTemplate) ([]ExpandedAgent, error) {
	var agents []ExpandedAgent

	for _, a := range pt.Agents {
		count := a.GetCount()

		if count == 0 {
			// Explicit count: 0 — skip this agent.
			continue
		}

		if count < 0 {
			count = 1
		}

		hasTemplate := strings.Contains(a.Name, "{{")

		if count == 1 && (a.Count == nil || !hasTemplate) {
			// Default (count omitted) or count:1 without template: single agent, no index.
			agents = append(agents, ExpandedAgent{
				Name:      a.Name,
				Role:      a.Role,
				Index:     0,
				Count:     0,
				Vars:      a.Vars,
				Overrides: a.Overrides,
			})
			continue
		}

		// count >= 1 with template, or count > 1: expand and render names.
		for i := 1; i <= count; i++ {
			expandCtx := &tmpl.Context{Index: i, Count: count}

			var name string
			if hasTemplate {
				rendered, err := tmpl.Render(a.Name, expandCtx)
				if err != nil {
					return nil, fmt.Errorf("render agent name %q (index %d): %w", a.Name, i, err)
				}
				name = rendered
			} else {
				// Auto-append index suffix.
				name = fmt.Sprintf("%s-%d", a.Name, i)
			}

			// Render role if it contains template expressions.
			role := a.Role
			if strings.Contains(role, "{{") {
				rendered, err := tmpl.Render(role, expandCtx)
				if err != nil {
					return nil, fmt.Errorf("render agent role %q (index %d): %w", a.Role, i, err)
				}
				role = rendered
			}

			agents = append(agents, ExpandedAgent{
				Name:      name,
				Role:      role,
				Index:     i,
				Count:     count,
				Vars:      a.Vars,
				Overrides: a.Overrides,
			})
		}
	}

	// Check for name collisions.
	if err := checkNameCollisions(agents); err != nil {
		return nil, err
	}

	return agents, nil
}

// checkNameCollisions detects duplicate agent names after expansion.
func checkNameCollisions(agents []ExpandedAgent) error {
	seen := make(map[string]int) // name → first index in agents slice
	for i, a := range agents {
		if prev, ok := seen[a.Name]; ok {
			return fmt.Errorf("duplicate agent name %q: agent at position %d collides with agent at position %d", a.Name, i+1, prev+1)
		}
		seen[a.Name] = i
	}
	return nil
}

// ValidatePodBridges checks that pod bridge references are valid.
// bridgeNames is the set of available bridge config names from config.yaml.
// agentNames is the set of expanded agent names in the pod.
func ValidatePodBridges(bridges []PodBridge, bridgeNames map[string]bool, agentNames map[string]bool) error {
	for i, pb := range bridges {
		if pb.Bridge == "" {
			return fmt.Errorf("bridges[%d]: bridge name is required", i)
		}
		if !bridgeNames[pb.Bridge] {
			return fmt.Errorf("bridges[%d]: bridge %q not found in config", i, pb.Bridge)
		}
		if pb.Concierge != "" && !agentNames[pb.Concierge] {
			return fmt.Errorf("bridges[%d]: concierge %q does not match any agent in this pod", i, pb.Concierge)
		}
	}
	return nil
}

// OverridesToSlice converts a map[string]string to the []string format
// expected by ApplyOverrides (key=value pairs).
func OverridesToSlice(overrides map[string]string) []string {
	if len(overrides) == 0 {
		return nil
	}
	result := make([]string, 0, len(overrides))
	for k, v := range overrides {
		result = append(result, k+"="+v)
	}
	return result
}

// resolvePodPath finds the pod file for the given name, trying .yaml.tmpl first, then .yaml.
func resolvePodPath(dir, name string) string {
	tmplPath := filepath.Join(dir, name+".yaml.tmpl")
	if _, err := os.Stat(tmplPath); err == nil {
		return tmplPath
	}
	return filepath.Join(dir, name+".yaml")
}

// LoadPodTemplate loads a template from <h2-dir>/pods/<name>.yaml or <name>.yaml.tmpl.
func LoadPodTemplate(name string) (*PodTemplate, error) {
	path := resolvePodPath(PodDir(), name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read pod template: %w", err)
	}

	var pt PodTemplate
	if err := yaml.Unmarshal(data, &pt); err != nil {
		return nil, fmt.Errorf("parse pod template: %w", err)
	}
	return &pt, nil
}

// LoadPodTemplateRendered loads a pod template with template rendering.
// Tries <name>.yaml.tmpl first, then <name>.yaml.
// It extracts variables, validates them, renders the template, then parses.
func LoadPodTemplateRendered(name string, ctx *tmpl.Context) (*PodTemplate, error) {
	path := resolvePodPath(PodDir(), name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read pod template: %w", err)
	}

	return ParsePodTemplateRendered(string(data), name, ctx)
}

// ParsePodTemplateRendered parses pod template YAML text with template rendering.
// Exported for testing without filesystem.
func ParsePodTemplateRendered(yamlText string, name string, ctx *tmpl.Context) (*PodTemplate, error) {
	// Phase 1: Extract variables before rendering.
	varDefs, remaining, err := tmpl.ParseVarDefs(yamlText)
	if err != nil {
		return nil, fmt.Errorf("pod template %q: %w", name, err)
	}

	// Clone ctx.Var so we don't mutate the caller's map.
	vars := make(map[string]string, len(ctx.Var))
	for k, v := range ctx.Var {
		vars[k] = v
	}
	for k, def := range varDefs {
		if _, provided := vars[k]; !provided && def.Default != nil {
			vars[k] = *def.Default
		}
	}

	// Validate required variables.
	if err := tmpl.ValidateVars(varDefs, vars); err != nil {
		return nil, fmt.Errorf("pod template %q: %w", name, err)
	}

	// Render template with cloned vars.
	// Preserve {{ .Index }} and {{ .Count }} for count expansion by replacing
	// them with placeholders before rendering, then restoring after.
	remaining = preserveCountPlaceholders(remaining)
	renderCtx := *ctx
	renderCtx.Var = vars
	rendered, err := tmpl.Render(remaining, &renderCtx)
	if err != nil {
		return nil, fmt.Errorf("pod template %q: %w", name, err)
	}

	// Restore count placeholders so they survive into agent name fields
	// for ExpandPodAgents to render later.
	rendered = restoreCountPlaceholders(rendered)

	// Parse rendered YAML.
	var pt PodTemplate
	if err := yaml.Unmarshal([]byte(rendered), &pt); err != nil {
		return nil, fmt.Errorf("pod template %q produced invalid YAML after rendering: %w", name, err)
	}
	pt.Variables = varDefs

	return &pt, nil
}

// countPlaceholders maps Go template expressions for .Index and .Count to
// unique placeholder strings that won't be touched by tmpl.Render. This lets
// pod-level template rendering resolve {{ .Var.x }} while preserving
// {{ .Index }} and {{ .Count }} for count expansion in ExpandPodAgents.
var countPlaceholders = [][2]string{
	{"{{ .Index }}", "\x00__POD_INDEX__\x00"},
	{"{{.Index}}", "\x00__POD_INDEX__\x00"},
	{"{{ .Count }}", "\x00__POD_COUNT__\x00"},
	{"{{.Count}}", "\x00__POD_COUNT__\x00"},
}

func preserveCountPlaceholders(s string) string {
	for _, p := range countPlaceholders {
		s = strings.ReplaceAll(s, p[0], p[1])
	}
	return s
}

func restoreCountPlaceholders(s string) string {
	// Restore to canonical form with spaces.
	s = strings.ReplaceAll(s, "\x00__POD_INDEX__\x00", "{{ .Index }}")
	s = strings.ReplaceAll(s, "\x00__POD_COUNT__\x00", "{{ .Count }}")
	return s
}

// loadPodTemplateForDisplay loads a pod template with rendering using only
// default variable values. Unlike LoadPodTemplateRendered, it skips
// required-var validation so templates can be listed without providing all vars.
func loadPodTemplateForDisplay(name string) (*PodTemplate, error) {
	path := resolvePodPath(PodDir(), name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read pod template: %w", err)
	}

	// Extract variables and apply defaults only.
	varDefs, remaining, err := tmpl.ParseVarDefs(string(data))
	if err != nil {
		return nil, fmt.Errorf("pod template %q: %w", name, err)
	}

	vars := make(map[string]string)
	for k, def := range varDefs {
		if def.Default != nil {
			vars[k] = *def.Default
		}
	}

	// Render with defaults only (no required-var validation).
	// Preserve {{ .Index }}/{{ .Count }} for count expansion.
	remaining = preserveCountPlaceholders(remaining)
	ctx := &tmpl.Context{Var: vars}
	rendered, err := tmpl.Render(remaining, ctx)
	if err != nil {
		return nil, fmt.Errorf("pod template %q: %w", name, err)
	}
	rendered = restoreCountPlaceholders(rendered)

	var pt PodTemplate
	if err := yaml.Unmarshal([]byte(rendered), &pt); err != nil {
		return nil, fmt.Errorf("pod template %q produced invalid YAML after rendering: %w", name, err)
	}
	pt.Variables = varDefs

	return &pt, nil
}

// ListPodTemplates returns available pod templates.
// Parse errors are collected and returned alongside successfully loaded templates,
// so callers can display both the working templates and any broken ones.
func ListPodTemplates() ([]*PodTemplate, []error, error) {
	dir := PodDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read pods dir: %w", err)
	}

	seen := make(map[string]bool) // deduplicate foo.yaml vs foo.yaml.tmpl
	var templates []*PodTemplate
	var parseErrs []error
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		var name string
		switch {
		case strings.HasSuffix(entry.Name(), ".yaml.tmpl"):
			name = strings.TrimSuffix(entry.Name(), ".yaml.tmpl")
		case strings.HasSuffix(entry.Name(), ".yaml"):
			name = strings.TrimSuffix(entry.Name(), ".yaml")
		default:
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		tmpl, err := loadPodTemplateForDisplay(name)
		if err != nil {
			parseErrs = append(parseErrs, fmt.Errorf("%s: %w", entry.Name(), err))
			continue
		}
		templates = append(templates, tmpl)
	}
	return templates, parseErrs, nil
}
