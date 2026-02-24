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

// PodRolesDir returns <h2-dir>/pods/roles/.
func PodRolesDir() string {
	return filepath.Join(ConfigDir(), "pods", "roles")
}

// PodTemplatesDir returns <h2-dir>/pods/templates/.
func PodTemplatesDir() string {
	return filepath.Join(ConfigDir(), "pods", "templates")
}

// LoadPodRole loads a role, checking pod roles first then global roles.
// Only called when --pod is specified. Without --pod, use LoadRole() (global only).
func LoadPodRole(name string) (*Role, error) {
	// Try pod-scoped role first.
	podPath := filepath.Join(PodRolesDir(), name+".yaml")
	if _, err := os.Stat(podPath); err == nil {
		return LoadRoleFrom(podPath)
	}
	// Fall back to global role.
	return LoadRole(name)
}

// IsPodScopedRole returns true if the role exists under pods/roles/ (pod-scoped),
// false if it would fall back to the global roles/ directory.
func IsPodScopedRole(name string) bool {
	podPath := filepath.Join(PodRolesDir(), name+".yaml")
	_, err := os.Stat(podPath)
	return err == nil
}

// LoadPodRoleRendered loads a role with template rendering, checking pod roles
// first then global roles. If ctx is nil, behaves like LoadPodRole.
func LoadPodRoleRendered(name string, ctx *tmpl.Context) (*Role, error) {
	// Try pod-scoped role first.
	podPath := filepath.Join(PodRolesDir(), name+".yaml")
	if _, err := os.Stat(podPath); err == nil {
		return LoadRoleRenderedFrom(podPath, ctx)
	}
	// Fall back to global role.
	return LoadRoleRendered(name, ctx)
}

// ListPodRoles returns roles from <h2-dir>/pods/roles/.
func ListPodRoles() ([]*Role, error) {
	return listRolesFromDir(PodRolesDir())
}

// PodTemplate defines a set of agents to launch together as a pod.
type PodTemplate struct {
	PodName   string                 `yaml:"pod_name"`
	Variables map[string]tmpl.VarDef `yaml:"variables"`
	Agents    []PodTemplateAgent     `yaml:"agents"`
}

// PodTemplateAgent defines a single agent within a pod template.
type PodTemplateAgent struct {
	Name  string            `yaml:"name"`
	Role  string            `yaml:"role"`
	Count *int              `yaml:"count,omitempty"` // nil = default (1 agent), 0 = skip, N = N agents
	Vars  map[string]string `yaml:"vars"`
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
	Name  string
	Role  string
	Index int
	Count int
	Vars  map[string]string
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
				Name:  a.Name,
				Role:  a.Role,
				Index: 0,
				Count: 0,
				Vars:  a.Vars,
			})
			continue
		}

		// count >= 1 with template, or count > 1: expand and render names.
		for i := 1; i <= count; i++ {
			var name string
			if hasTemplate {
				rendered, err := tmpl.Render(a.Name, &tmpl.Context{Index: i, Count: count})
				if err != nil {
					return nil, fmt.Errorf("render agent name %q (index %d): %w", a.Name, i, err)
				}
				name = rendered
			} else {
				// Auto-append index suffix.
				name = fmt.Sprintf("%s-%d", a.Name, i)
			}

			agents = append(agents, ExpandedAgent{
				Name:  name,
				Role:  a.Role,
				Index: i,
				Count: count,
				Vars:  a.Vars,
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

// LoadPodTemplate loads a template from <h2-dir>/pods/templates/<name>.yaml.
func LoadPodTemplate(name string) (*PodTemplate, error) {
	path := filepath.Join(PodTemplatesDir(), name+".yaml")
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
// It extracts variables, validates them, renders the template, then parses.
func LoadPodTemplateRendered(name string, ctx *tmpl.Context) (*PodTemplate, error) {
	path := filepath.Join(PodTemplatesDir(), name+".yaml")
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
	renderCtx := *ctx
	renderCtx.Var = vars
	rendered, err := tmpl.Render(remaining, &renderCtx)
	if err != nil {
		return nil, fmt.Errorf("pod template %q: %w", name, err)
	}

	// Parse rendered YAML.
	var pt PodTemplate
	if err := yaml.Unmarshal([]byte(rendered), &pt); err != nil {
		return nil, fmt.Errorf("pod template %q produced invalid YAML after rendering: %w", name, err)
	}
	pt.Variables = varDefs

	return &pt, nil
}

// ListPodTemplates returns available pod templates.
func ListPodTemplates() ([]*PodTemplate, error) {
	dir := PodTemplatesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read pod templates dir: %w", err)
	}

	var templates []*PodTemplate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		tmpl, err := LoadPodTemplate(strings.TrimSuffix(entry.Name(), ".yaml"))
		if err != nil {
			continue
		}
		templates = append(templates, tmpl)
	}
	return templates, nil
}
