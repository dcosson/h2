package sandbox

import "fmt"

// Preset defines the configuration template for a sandbox environment.
type Preset struct {
	name         string
	settings     func() map[string]any
	claudeMD     func() string
	roles        func() map[string]string
	podTemplates func() map[string]string
}

// Settings returns the settings.json content for this preset.
func (p *Preset) Settings() map[string]any {
	return p.settings()
}

// ClaudeMD returns the CLAUDE.md content for this preset (empty if none).
func (p *Preset) ClaudeMD() string {
	return p.claudeMD()
}

// Roles returns a map of role-name -> YAML content.
func (p *Preset) Roles() map[string]string {
	return p.roles()
}

// PodTemplates returns a map of template-name -> YAML content.
func (p *Preset) PodTemplates() map[string]string {
	return p.podTemplates()
}

// ValidPresets lists the names of all available presets.
var ValidPresets = []string{"empty", "hooks", "haiku", "opus"}

// GetPreset returns a Preset by name.
func GetPreset(name string) (*Preset, error) {
	switch name {
	case "empty":
		return presetEmpty(), nil
	case "hooks":
		return presetHooks(), nil
	case "haiku":
		return presetHaiku(), nil
	case "opus":
		return presetOpus(), nil
	default:
		return nil, fmt.Errorf("unknown preset %q; valid presets: %v", name, ValidPresets)
	}
}

// --- Preset: empty ---
// Minimal settings, no hooks, no roles, no CLAUDE.md.
// Purpose: baseline Claude Code testing (reproduce published scores).

func presetEmpty() *Preset {
	return &Preset{
		name:         "empty",
		settings:     emptySettings,
		claudeMD:     func() string { return "" },
		roles:        func() map[string]string { return nil },
		podTemplates: func() map[string]string { return nil },
	}
}

func emptySettings() map[string]any {
	return map[string]any{}
}

// --- Preset: hooks ---
// Standard h2 hooks, no roles, no CLAUDE.md.
// Purpose: e2e tests that need state tracking.

func presetHooks() *Preset {
	return &Preset{
		name:         "hooks",
		settings:     hooksSettings,
		claudeMD:     func() string { return "" },
		roles:        func() map[string]string { return nil },
		podTemplates: func() map[string]string { return nil },
	}
}

func hooksSettings() map[string]any {
	return map[string]any{
		"hooks": h2StandardHooks(),
	}
}

// --- Preset: haiku ---
// h2 hooks + default role with model: haiku, auto-allow permissions.
// Purpose: fast, cheap benchmark runs.

func presetHaiku() *Preset {
	return &Preset{
		name:     "haiku",
		settings: haikuSettings,
		claudeMD: func() string { return "" },
		roles:    haikuRoles,
		podTemplates: func() map[string]string { return nil },
	}
}

func haikuSettings() map[string]any {
	return map[string]any{
		"hooks": h2StandardHooks(),
	}
}

func haikuRoles() map[string]string {
	return map[string]string{
		"default": `name: default
model: haiku
permission_mode: bypassPermissions
instructions: |
  You are a coding agent. Complete the task you are given.
`,
	}
}

// --- Preset: opus ---
// h2 hooks + multiple roles (concierge, coder, reviewer), auto-allow permissions.
// Purpose: full h2 multi-agent benchmark runs.

func presetOpus() *Preset {
	return &Preset{
		name:         "opus",
		settings:     opusSettings,
		claudeMD:     opusClaudeMD,
		roles:        opusRoles,
		podTemplates: opusPodTemplates,
	}
}

func opusSettings() map[string]any {
	return map[string]any{
		"hooks": h2StandardHooks(),
	}
}

func opusClaudeMD() string {
	return `# Benchmark Agent

You are running as an agent in an h2 benchmark environment.

## Guidelines

- Focus on solving the task efficiently and correctly.
- Use h2 messaging to coordinate with other agents when in multi-agent mode.
- Commit your changes when the task is complete.
`
}

func opusRoles() map[string]string {
	return map[string]string{
		"default": `name: default
model: opus
permission_mode: bypassPermissions
instructions: |
  You are a coding agent. Complete the task you are given.
`,
		"concierge": `name: concierge
model: opus
permission_mode: bypassPermissions
instructions: |
  You are the concierge agent. You coordinate work across the team.
  Break down the task into subtasks and assign them to coder agents.
  Use h2 send to communicate with other agents.
`,
		"coder": `name: coder
model: opus
permission_mode: bypassPermissions
instructions: |
  You are a coder agent. Implement the subtask assigned to you.
  Write tests for your changes.
  When done, message the concierge with your results.
`,
		"reviewer": `name: reviewer
model: opus
permission_mode: bypassPermissions
instructions: |
  You are a code reviewer agent. Review changes from coder agents.
  Check for correctness, test coverage, and code quality.
  Provide feedback via h2 send.
`,
	}
}

func opusPodTemplates() map[string]string {
	return map[string]string{
		"benchmark": BenchmarkPodTemplate,
	}
}

// BenchmarkPodTemplate is the standard pod template for benchmark runs.
// It defines concierge, coder (x2), and reviewer agents, all using
// {{ .Var.working_dir }} so the benchmark runner can point them at the
// cloned repo for each task.
const BenchmarkPodTemplate = `pod_name: benchmark
variables:
  working_dir:
    description: "Working directory for agents (benchmark repo clone)"
    required: true
agents:
  - name: concierge
    role: concierge
    vars:
      working_dir: "{{ .Var.working_dir }}"
  - name: coder
    role: coder
    count: 2
    vars:
      working_dir: "{{ .Var.working_dir }}"
  - name: reviewer
    role: reviewer
    vars:
      working_dir: "{{ .Var.working_dir }}"
`

// --- Shared hooks ---

type hookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type hookMatcher struct {
	Matcher string      `json:"matcher"`
	Hooks   []hookEntry `json:"hooks"`
}

func h2StandardHooks() map[string][]hookMatcher {
	collectHook := hookEntry{
		Type:    "command",
		Command: "h2 hook collect",
		Timeout: 5,
	}

	permissionHook := hookEntry{
		Type:    "command",
		Command: "h2 permission-request",
		Timeout: 60,
	}

	standardEvents := []string{
		"PreToolUse",
		"PostToolUse",
		"SessionStart",
		"Stop",
		"UserPromptSubmit",
	}

	hooks := make(map[string][]hookMatcher)
	for _, event := range standardEvents {
		hooks[event] = []hookMatcher{{
			Matcher: "",
			Hooks:   []hookEntry{collectHook},
		}}
	}

	hooks["PermissionRequest"] = []hookMatcher{{
		Matcher: "",
		Hooks:   []hookEntry{permissionHook, collectHook},
	}}

	return hooks
}
