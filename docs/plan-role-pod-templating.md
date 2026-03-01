# Role & Pod Template Templating System

## Overview

Add a full templating system to h2 roles and pod templates using Go's `text/template` engine. This enables parameterized roles, dynamic pod configurations, and agent multiplication patterns.

## Current State

- Role YAML files are static — loaded and used as-is after `h2 role init`
- Pod templates are simple agent lists with hardcoded names and role references
- No variable substitution, conditionals, or loops in either
- Only "templating" is one-time `fmt.Sprintf` substitution at `h2 role init` time (in `roleTemplate()` in `role.go`)

**Migration note:** The existing `h2 role init` substitution (`fmt.Sprintf` for `${name}` and `${claudeConfigDir}`) is replaced by this system. The `role init` command will generate roles using `{{ .RoleName }}` and `{{ .Var.claude_config_dir }}` syntax instead. Existing static roles (no `{{ }}` expressions) continue to work unchanged — they're valid templates that happen to produce themselves.

## Requirements

### 1. User-Defined Variables

Variables can be declared in role and pod template YAML files with optional defaults:

```yaml
# In a role file
variables:
  project_name:
    description: "The project this agent works on"
    default: "myapp"
  team:
    description: "Team name"
    # no default — required at launch time

instructions: |
  You work on {{ .Var.project_name }} for team {{ .Var.team }}.
```

**Required vs optional — how it works:**

A variable is **required** if the `default:` key is absent from its YAML definition. A variable is **optional** if `default:` is present — even if the default value is an empty string.

```yaml
variables:
  team:
    description: "Team name"
    # no `default:` key at all → REQUIRED
    # Launching without --var team=X will fail

  project_name:
    description: "Project"
    default: "myapp"
    # has `default:` → OPTIONAL, defaults to "myapp"

  prefix:
    description: "Prefix"
    default: ""
    # has `default:` (empty string) → OPTIONAL, defaults to ""
```

In Go, we detect the presence vs absence of the YAML key using a `*string` pointer:

```go
type VarDef struct {
    Description string  `yaml:"description"`
    Default     *string `yaml:"default"`  // nil = no default (required), non-nil = has default (optional)
}

func (v VarDef) Required() bool {
    return v.Default == nil
}
```

When YAML has no `default:` key, Go's YAML parser leaves `Default` as `nil` → required. When YAML has `default: ""`, Go parses it as a pointer to `""` → optional. This cleanly distinguishes "not set" from "set to empty."

**Validation rules:**
- If a variable has no `default`, it is **required** — launching an agent with a role that has unsatisfied required variables fails with a clear error listing all missing variables
- Variables can be provided via CLI: `h2 run coder --role coding --var team=backend`
- Variables can be provided via CLI on pod launch: `h2 pod launch backend --var num_coders=5`
- Pod templates can pass variables to roles via per-agent `vars` (see section 4)
- Variable values are always strings (consistent with Go templates — use template functions for conversion)

**Important constraint:** The `variables:` section itself must not contain template expressions. It is parsed before template rendering to extract defaults and validate requirements. All other sections of the YAML are rendered through the template engine.

### 2. Built-In Variables

Available automatically based on context — no declaration needed:

| Variable | Available In | Default (standalone) | Description |
|----------|-------------|---------------------|-------------|
| `.AgentName` | Roles | Agent's name | Name of the agent being launched |
| `.RoleName` | Roles | Role's name | Name of the role |
| `.PodName` | Roles, Pod templates | `""` (empty string) | Pod name; empty if not in pod |
| `.Index` | Roles, Pod template agent names | `0` | 1-based index within count group; 0 if standalone or count=1 |
| `.Count` | Roles, Pod template agent names | `0` | Total count for agent group; 0 if standalone or count=1 |
| `.H2Dir` | Roles, Pod templates | h2 dir path | Absolute path to the h2 directory |

**Zero-value note:** For standalone agents (`h2 run`), `.PodName` is `""`, `.Index` is `0`, `.Count` is `0`. Use `{{ if .PodName }}` to check pod membership. Avoid `{{ if .Index }}` since 0 is falsy — use `{{ if gt .Index 0 }}` instead.

### 3. Pod Template Agent Multiplication

Pod templates gain a `count` field that launches multiple copies of an agent with the same role:

```yaml
# Pod template: dev-team.yaml
pod_name: dev-team
agents:
  - role: concierge
    name: concierge
  - role: coding
    name: "coder-{{ .Index }}"
    count: 3
  - role: reviewer
    name: reviewer
```

This launches: `concierge`, `coder-1`, `coder-2`, `coder-3`, `reviewer`.

When `count` is set:
- The `name` field is rendered as a template for each copy, with `.Index` set to 1, 2, ..., N
- `.Count` is set to N
- If `name` doesn't contain `{{ .Index }}`, an index suffix is appended automatically (e.g., `coder` becomes `coder-1`, `coder-2`, ...)
- `count` defaults to 1 (no change from current behavior)
- `.Index` and `.Count` are available both at pod template expansion time (for agent names) and during role rendering (for instructions)

**Name collision detection:** After expanding all agents (including count groups), the system checks for duplicate agent names and fails with a clear error if any collisions are found. This catches cases like a manually-named `coder-2` colliding with `count: 3` producing `coder-1`, `coder-2`, `coder-3`.

### 4. Variable Passing from Pod Templates to Roles

Pod templates can pass variables to the roles they reference:

```yaml
# Pod template
agents:
  - role: coding
    name: "coder-{{ .Index }}"
    count: 3
    vars:
      team: backend
      project_name: h2
```

The `vars` map is merged with any CLI `--var` flags (CLI takes precedence) and passed to the role's template rendering. This is how pod templates satisfy required role variables.

### 5. Conditionals and Loops in Templates

Full Go `text/template` syntax is available. Templating applies to **all sections** of role and pod template YAML — instructions, worktree config, hooks, settings, heartbeat, etc. — since the entire YAML text is rendered before parsing.

**Conditionals in role instructions:**
```yaml
instructions: |
  You are {{ .AgentName }}.
  {{ if .PodName }}
  You are part of the {{ .PodName }} pod. Coordinate with teammates.
  {{ else }}
  You are running standalone.
  {{ end }}
```

**Dynamic worktree branch names:**
```yaml
worktree:
  project_dir: /Users/me/code/myrepo
  name: "{{ .AgentName }}-wt"
  branch_name: "feature/{{ .Var.ticket_id }}"
```

**Conditionals in pod templates:**
```yaml
agents:
  - role: concierge
    name: concierge
  {{ if .Var.include_reviewer }}
  - role: reviewer
    name: reviewer
  {{ end }}
```

**Loops in pod templates (advanced):**
```yaml
# Pod template variables
variables:
  services:
    default: "api,web,worker"

agents:
  {{ range $svc := split .Var.services "," }}
  - role: coding
    name: "{{ $svc }}-coder"
    vars:
      service: "{{ $svc }}"
  {{ end }}
```

### 6. Custom Template Functions

Beyond Go's built-in template functions:

| Function | Description | Example |
|----------|-------------|---------|
| `seq` | Generate integer sequence | `{{ range $i := seq 1 3 }}` |
| `split` | Split string by delimiter | `{{ range split .Var.list "," }}` |
| `join` | Join strings | `{{ join .Var.items "," }}` |
| `default` | Fallback value | `{{ default .Var.name "unnamed" }}` |
| `upper` / `lower` | Case conversion | `{{ upper .Var.env }}` |
| `contains` | String contains check | `{{ if contains .Var.mode "debug" }}` |
| `trimSpace` | Trim whitespace | `{{ trimSpace .Var.name }}` |
| `quote` | YAML-safe string quoting | `{{ quote .Var.value }}` |

### 7. Template Delimiters and Escaping

Use Go's standard `{{ }}` delimiters. They're widely understood and rare in natural language/markdown instructions.

To include literal `{{ }}` in output (e.g., documenting Go templates in instructions), use the standard Go template escape: `{{ "{{" }}` produces `{{`.

## Architecture

### Two-Phase Pod Rendering Pipeline

Pod templates have a two-phase rendering process:

```
Phase 1 — Pod template rendering:
  1. Load raw pod template YAML text
  2. Extract `variables:` section (plain YAML parse of just that section)
  3. Validate required pod-level variables are provided
  4. Build pod context (pod-level built-ins + CLI vars + variable defaults)
  5. Render pod template text through text/template
  6. Parse rendered YAML into PodTemplate struct

Phase 2 — Agent expansion and role rendering:
  7. Expand count groups → produce flat list of (name, role, index, count, vars)
  8. Check for agent name collisions
  9. For each agent:
     a. Load raw role YAML text
     b. Extract role's `variables:` section
     c. Merge vars: role defaults < pod template vars < CLI vars
     d. Validate required role variables
     e. Build role context (agent-level built-ins + merged vars)
     f. Render role text through text/template
     g. Parse rendered YAML into Role struct
```

**Role-only rendering** (for `h2 run`, no pod) follows steps 9a–9g directly.

### New Package: `internal/tmpl/`

```go
package tmpl

// VarDef defines a template variable with optional default.
// Default is a pointer: nil means "required" (no default), non-nil means "optional".
type VarDef struct {
    Description string  `yaml:"description"`
    Default     *string `yaml:"default"`
}

// Context holds all template data available during rendering.
type Context struct {
    AgentName string
    RoleName  string
    PodName   string
    Index     int
    Count     int
    H2Dir     string
    Var       map[string]string
}

// Render processes a YAML template string with the given context.
// Returns the rendered string or an error.
// Template parse/execute errors are wrapped with source context
// (file name, approximate line) for debuggability.
func Render(templateText string, ctx *Context) (string, error)

// ParseVarDefs extracts variable definitions from raw YAML text.
// Returns the definitions and the YAML text with the variables section removed.
// The variables section must not contain template expressions.
func ParseVarDefs(yamlText string) (map[string]VarDef, string, error)

// ValidateVars checks that all required variables (no default) are provided.
// Returns a descriptive error listing all missing variables with descriptions.
func ValidateVars(defs map[string]VarDef, provided map[string]string) error
```

### Changes to Existing Code

**`internal/config/role.go`:**
- Add `LoadRoleRendered(name string, ctx *tmpl.Context) (*Role, error)` — renders template then parses
- `LoadRole(name)` remains unchanged (nil context = no rendering, backward compat)
- `Role` struct gets `Variables map[string]tmpl.VarDef` field (parsed but not rendered)

**`internal/config/pods.go`:**
- `PodTemplateAgent` gains `Count int` and `Vars map[string]string` fields
- `LoadPodTemplate` renders template text before parsing (when context provided)
- New `ExpandPodAgents(tmpl *PodTemplate) ([]ExpandedAgent, error)` handles count multiplication and name collision detection

**`internal/cmd/agent_setup.go`:**
- Builds `tmpl.Context` from agent name, role, pod, and CLI vars
- Passes context through to role loading

**`internal/cmd/pod.go`:**
- `h2 pod launch` adds `--var key=value` flag (repeatable)
- Expands `count` agents, setting `.Index` and `.Count`
- Merges `vars` from pod template with CLI `--var` flags
- Passes merged vars to each agent's role rendering

**`internal/cmd/run.go`:**
- Adds `--var key=value` flag (repeatable)
- Passes vars to role loading via `LoadRoleRendered`

**`internal/cmd/role.go`:**
- Update `roleTemplate()` to use `{{ }}` syntax instead of `fmt.Sprintf` with `${}`

### Error Messages

**Missing required variables:**
```
Error: role "coding" requires variables that were not provided:

  team         — Team name
  project_name — The project this agent works on

Provide them with: h2 run --role coding --var team=X --var project_name=Y
```

**Template syntax errors** are wrapped with file context:
```
Error: template error in role "coding" (~/.h2/roles/coding.yaml):
  line 15: unexpected "}" in operand

Check your {{ }} template expressions for syntax errors.
```

**Agent name collisions:**
```
Error: pod template "backend" produces duplicate agent name "coder-2":
  - explicitly named agent "coder-2"
  - generated from count group (role: coding, index 2 of 3)

Rename one of the agents to avoid the collision.
```

**Invalid rendered YAML:**
```
Error: pod template "backend" produced invalid YAML after rendering:
  line 12: did not find expected '-' indicator

This may be a template indentation issue. Ensure {{ range }} loops
produce correctly indented YAML list items.
```

## Rendering Order & Precedence

1. **Pod template** rendered first (with pod-level vars + CLI vars)
2. **Count expansion** — produces flat agent list with `.Index` and `.Count` set
3. **Name collision check** — fail early if duplicate agent names
4. **Role** rendered for each agent (with agent-level built-ins + pod template vars + CLI vars)

**Variable precedence** (highest to lowest):
1. CLI `--var` flags
2. Pod template `vars` per-agent
3. Variable `default` values in the role/template definition

## Examples

### Example 1: Parameterized Role

```yaml
# ~/.h2/roles/service-coder.yaml
name: service-coder
variables:
  service:
    description: "Which microservice to work on"
  language:
    description: "Primary language"
    default: "go"

instructions: |
  You are {{ .AgentName }}, working on the {{ .Var.service }} service ({{ .Var.language }}).
  {{ if eq .Var.language "go" }}
  Use `go test ./...` to run tests and `go build ./...` to build.
  {{ else if eq .Var.language "python" }}
  Use `pytest` to run tests and `pip install -e .` to install.
  {{ end }}
```

Launch: `h2 run api-coder --role service-coder --var service=api`

### Example 2: Scaled Pod

```yaml
# ~/.h2/pods/templates/backend.yaml
pod_name: backend
variables:
  num_coders:
    default: "2"

agents:
  - role: concierge
    name: concierge
  - role: coding
    name: "coder-{{ .Index }}"
    count: {{ .Var.num_coders }}
    vars:
      team: backend
  - role: reviewer
    name: reviewer
```

Launch with defaults: `h2 pod launch backend`
Launch with 5 coders: `h2 pod launch backend --var num_coders=5`

### Example 3: Role Aware of Pod Context

```yaml
# ~/.h2/roles/coding.yaml
instructions: |
  You are {{ .AgentName }}.
  {{ if .PodName }}
  You are agent {{ .Index }}/{{ .Count }} in the {{ .PodName }} pod.
  Use `h2 list` to see your teammates and coordinate work.
  {{ end }}
```

## Files to Modify

| File | Change |
|------|--------|
| `internal/tmpl/` (new) | Template engine, variable parsing, validation, custom functions |
| `internal/tmpl/tmpl_test.go` (new) | Unit tests: rendering, validation, custom functions, edge cases |
| `internal/config/role.go` | Add `LoadRoleRendered`, Variables field, render pipeline |
| `internal/config/pods.go` | Add Count/Vars to PodTemplateAgent, `ExpandPodAgents`, template rendering |
| `internal/config/role_test.go` | Integration tests: LoadRoleRendered with variables, conditionals |
| `internal/config/pods_test.go` | Integration tests: count expansion, name collision detection, var passing |
| `internal/cmd/run.go` | Add `--var` flag, pass to `LoadRoleRendered` |
| `internal/cmd/pod.go` | Add `--var` flag, expand count agents, merge vars, pass to role rendering |
| `internal/cmd/role.go` | Update `roleTemplate()` from `fmt.Sprintf` to `{{ }}` syntax |
| `internal/cmd/agent_setup.go` | Accept and thread template context |

## Future Work (not in v1)

- **`h2 role validate` / `h2 pod validate`**: Dry-run validation command that renders with provided or sample values, shows the result, and flags YAML errors. Useful for template authors.
- **Additional template functions**: Add more as usage patterns emerge.

## Verification

1. `make build` compiles
2. `make test` passes — unit tests for `tmpl` package + integration tests for role/pod loading
3. Manual: Create a parameterized role, launch with `--var`, verify instructions are rendered
4. Manual: Create a pod template with `count`, launch, verify agents named correctly
5. Manual: Omit a required variable, verify clear error message
6. Manual: Use conditionals in instructions, verify correct branch rendered
7. Manual: Existing roles without `{{ }}` continue to work unchanged
