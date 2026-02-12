# Role & Pod Template Templating — Testing & Conformance

Companion to `plan-role-pod-templating.md`. Defines all test cases needed to verify the templating system before merging.

## 1. Variable Definition Parsing (`internal/tmpl/`)

### 1.1 Required vs Optional Detection

| Test | YAML Input | Expected |
|------|-----------|----------|
| No default key → required | `team:\n  description: "Team"` | `Default == nil`, `Required() == true` |
| String default → optional | `team:\n  default: "backend"` | `Default == ptr("backend")`, `Required() == false` |
| Empty string default → optional | `team:\n  default: ""` | `Default == ptr("")`, `Required() == false` |
| Description only | `team:\n  description: "Team"` | Description populated, still required |
| No description | `team: {}` | Description empty, still required |

### 1.2 ParseVarDefs Extraction

| Test | Input | Expected |
|------|-------|----------|
| Extracts variables section | YAML with `variables:` + `instructions:` | Returns VarDef map + remaining YAML without `variables:` |
| No variables section | YAML with only `instructions:` | Returns empty map + original YAML unchanged |
| Empty variables section | `variables: {}` | Returns empty map |
| Multiple variables | 3 vars, mix of required/optional | All 3 parsed correctly |
| Variables section has no template expressions | `variables:` with plain YAML | Parses successfully |

## 2. Variable Validation (`internal/tmpl/`)

### 2.1 ValidateVars

| Test | Defined Vars | Provided Vars | Expected |
|------|-------------|---------------|----------|
| All required provided | `team` (required) | `{"team": "backend"}` | No error |
| Required missing | `team` (required) | `{}` | Error listing `team` |
| Multiple required missing | `team`, `project` (both required) | `{}` | Error listing both |
| Optional not provided → uses default | `env` (default: "dev") | `{}` | No error |
| Extra vars provided (not defined) | `team` (required) | `{"team": "x", "extra": "y"}` | No error (extras ignored) |
| Mix of required/optional, required satisfied | `team` (req), `env` (opt) | `{"team": "x"}` | No error |

### 2.2 Error Message Format

| Test | Expected |
|------|----------|
| Single missing var | Error includes var name and description |
| Multiple missing vars | Error lists all missing vars, sorted alphabetically |
| Error includes CLI hint | Message contains `--var team=X` suggestion |

## 3. Template Rendering (`internal/tmpl/`)

### 3.1 Basic Substitution

| Test | Template | Context | Expected Output |
|------|----------|---------|-----------------|
| Built-in AgentName | `Hello {{ .AgentName }}` | AgentName: "coder-1" | `Hello coder-1` |
| Built-in RoleName | `Role: {{ .RoleName }}` | RoleName: "coding" | `Role: coding` |
| Built-in PodName | `Pod: {{ .PodName }}` | PodName: "backend" | `Pod: backend` |
| Built-in Index | `#{{ .Index }}` | Index: 2 | `#2` |
| Built-in Count | `of {{ .Count }}` | Count: 5 | `of 5` |
| Built-in H2Dir | `Dir: {{ .H2Dir }}` | H2Dir: "/home/.h2" | `Dir: /home/.h2` |
| User variable | `Team: {{ .Var.team }}` | Var: {"team": "backend"} | `Team: backend` |
| Multiple vars | `{{ .Var.a }} {{ .Var.b }}` | Var: {"a":"x","b":"y"} | `x y` |
| No template expressions | `plain text` | any | `plain text` (passthrough) |

### 3.2 Conditionals

| Test | Template | Context | Expected |
|------|----------|---------|----------|
| if true | `{{ if .PodName }}yes{{ end }}` | PodName: "x" | `yes` |
| if false (empty string) | `{{ if .PodName }}yes{{ end }}` | PodName: "" | `` |
| if/else | `{{ if .PodName }}pod{{ else }}standalone{{ end }}` | PodName: "" | `standalone` |
| if eq | `{{ if eq .Var.lang "go" }}go{{ end }}` | Var: {"lang":"go"} | `go` |
| if gt for Index | `{{ if gt .Index 0 }}indexed{{ end }}` | Index: 0 | `` |
| if gt for Index (positive) | `{{ if gt .Index 0 }}indexed{{ end }}` | Index: 1 | `indexed` |
| Nested if | `{{ if .PodName }}{{ if gt .Count 1 }}multi{{ end }}{{ end }}` | PodName:"x", Count:3 | `multi` |

### 3.3 Loops

| Test | Template | Context | Expected |
|------|----------|---------|----------|
| range with seq | `{{ range $i := seq 1 3 }}{{ $i }} {{ end }}` | — | `1 2 3 ` |
| range with split | `{{ range split .Var.list "," }}[{{ . }}]{{ end }}` | Var: {"list":"a,b,c"} | `[a][b][c]` |

### 3.4 Error Handling

| Test | Template | Expected |
|------|----------|----------|
| Syntax error | `{{ if }}` | Error mentioning template syntax |
| Undefined variable | `{{ .Var.nonexistent }}` | Renders as `<no value>` or empty (Go template default) |
| Unclosed block | `{{ if .PodName }}no end` | Error mentioning unclosed action |
| Error includes source context | any broken template | Error message includes file name if provided |

## 4. Custom Template Functions (`internal/tmpl/`)

| Function | Test | Input | Expected |
|----------|------|-------|----------|
| `seq` | Basic range | `seq 1 3` | `[1, 2, 3]` |
| `seq` | Single element | `seq 5 5` | `[5]` |
| `seq` | Start > end | `seq 3 1` | `[]` (empty) |
| `split` | Comma-separated | `split "a,b,c" ","` | `["a","b","c"]` |
| `split` | No delimiter found | `split "abc" ","` | `["abc"]` |
| `split` | Empty string | `split "" ","` | `[""]` |
| `join` | Basic join | `join ["a","b"] ","` | `"a,b"` |
| `default` | Value present | `default "hello" "fallback"` | `"hello"` |
| `default` | Value empty | `default "" "fallback"` | `"fallback"` |
| `upper` | Basic | `upper "hello"` | `"HELLO"` |
| `lower` | Basic | `lower "HELLO"` | `"hello"` |
| `contains` | Match | `contains "hello world" "world"` | `true` |
| `contains` | No match | `contains "hello" "world"` | `false` |
| `trimSpace` | Leading/trailing | `trimSpace "  hi  "` | `"hi"` |
| `quote` | Plain string | `quote "hello"` | `"\"hello\""` |
| `quote` | String with quotes | `quote "say \"hi\""` | Properly escaped |

## 5. Pod Template Expansion (`internal/config/`)

### 5.1 Count Expansion

| Test | Template Agents | Expected Expanded |
|------|----------------|-------------------|
| No count (default 1) | `name: coder, role: coding` | 1 agent: `coder` |
| count: 1 explicit | `name: coder, count: 1` | 1 agent: `coder` (no index suffix) |
| count: 3 with Index | `name: "coder-{{ .Index }}", count: 3` | 3 agents: `coder-1`, `coder-2`, `coder-3` |
| count: 3 without Index | `name: coder, count: 3` | 3 agents: `coder-1`, `coder-2`, `coder-3` (auto-suffix) |
| count: 0 | `name: coder, count: 0` | 0 agents (skipped) |
| Mixed agents | concierge (count 1) + coder (count 3) + reviewer (count 1) | 5 agents total |

### 5.2 Name Collision Detection

| Test | Template Agents | Expected |
|------|----------------|----------|
| No collision | `coder-1`, `coder-2`, `reviewer` | No error |
| Explicit collision with count group | `coder-2` (explicit) + `coder-{{ .Index }}` (count 3) | Error naming both agents |
| Two count groups collide | `worker-{{ .Index }}` (count 2) + `worker-{{ .Index }}` (count 2) | Error (worker-1 and worker-2 duplicated) |
| No collision despite similar names | `coder` (count 1) + `coder-helper` | No error |

### 5.3 Variable Passing to Roles

| Test | Pod Template | CLI Vars | Expected Role Context |
|------|-------------|----------|----------------------|
| Pod vars passed through | agent `vars: {team: backend}` | — | Role gets `Var["team"] = "backend"` |
| CLI overrides pod vars | agent `vars: {team: backend}` | `--var team=frontend` | Role gets `Var["team"] = "frontend"` |
| Pod vars + role defaults | agent `vars: {team: x}`, role default `env: dev` | — | Role gets both `team=x` and `env=dev` |
| CLI overrides role defaults | role default `env: dev` | `--var env=prod` | Role gets `env=prod` |

## 6. Role Loading with Context (`internal/config/`)

### 6.1 LoadRoleRendered

| Test | Role YAML | Context | Expected |
|------|----------|---------|----------|
| Variables rendered in instructions | `instructions: "Hi {{ .AgentName }}"` | AgentName: "coder" | Role.Instructions == "Hi coder" |
| Variables rendered in worktree config | `worktree:\n  branch_name: "feat/{{ .Var.ticket }}"` | Var: {"ticket":"123"} | Worktree.BranchName == "feat/123" |
| Required var provided | `variables: {team: {}}`, `{{ .Var.team }}` | Var: {"team":"x"} | Renders successfully |
| Required var missing | `variables: {team: {}}`, `{{ .Var.team }}` | Var: {} | Error listing `team` |
| No context (nil) = no rendering | Role with `{{ .AgentName }}` | nil | `{{ .AgentName }}` left as-is (backward compat) |

### 6.2 Backward Compatibility

| Test | Expected |
|------|----------|
| Existing role with no `{{ }}` expressions | Loads identically to current behavior |
| Existing role with no `variables:` section | Loads without error |
| `LoadRole(name)` (old API) | Still works, no rendering applied |

## 7. CLI Integration (`internal/cmd/`)

### 7.1 `h2 run --var`

| Test | Command | Expected |
|------|---------|----------|
| Single var | `h2 run --var team=backend` | Var map: `{"team":"backend"}` |
| Multiple vars | `--var team=backend --var env=prod` | Both vars in map |
| Value with equals sign | `--var query=a=b` | `{"query":"a=b"}` (split on first `=`) |
| Missing equals sign | `--var team` | Error: invalid var format |
| Empty value | `--var team=` | `{"team":""}` |

### 7.2 `h2 pod launch --var`

| Test | Command | Expected |
|------|---------|----------|
| Pod-level vars | `h2 pod launch backend --var num_coders=5` | Pod template rendered with var |
| Vars propagated to roles | `--var team=backend` | Roles receive the var |
| Vars override pod template vars | Template has `vars: {team: x}`, CLI has `--var team=y` | Role gets `team=y` |

## 8. Two-Phase Pod Rendering Pipeline

### 8.1 Phase 1: Pod Template Rendering

| Test | Expected |
|------|----------|
| Pod variables extracted before rendering | `variables:` section parsed from raw YAML |
| Pod template rendered with vars | `{{ .Var.num_coders }}` in count field resolves |
| Rendered YAML parses correctly | Valid PodTemplate struct after rendering |
| Invalid rendered YAML | Error with helpful message about template indentation |

### 8.2 Phase 2: Agent Expansion + Role Rendering

| Test | Expected |
|------|----------|
| Count expanded after pod rendering | Agents list has correct count of entries |
| .Index and .Count set per agent | Each agent in count group gets correct values |
| Role rendered per-agent | Each agent's role gets its own context |
| Role rendering fails for one agent | Error identifies which agent failed |

## 9. End-to-End Integration Tests

These test the full pipeline from YAML file on disk through to the final Role/PodTemplate structs.

### 9.1 Parameterized Role E2E

```
Given: Role file with variables (team required, env optional default "dev")
When:  LoadRoleRendered("test-role", ctx) with Var: {"team": "backend"}
Then:  - Instructions contain "backend" and "dev"
       - No error
```

### 9.2 Parameterized Role Missing Var E2E

```
Given: Role file with required variable "team"
When:  LoadRoleRendered("test-role", ctx) with Var: {}
Then:  - Error message lists "team"
       - Error includes --var hint
```

### 9.3 Pod with Count E2E

```
Given: Pod template with count: 3 agent using "coder-{{ .Index }}"
When:  Load and expand pod template
Then:  - 3 expanded agents: coder-1, coder-2, coder-3
       - Each has Index 1/2/3 and Count 3
```

### 9.4 Pod Vars to Role E2E

```
Given: Pod template with per-agent vars: {team: backend}
       Role with required variable "team" and instructions using {{ .Var.team }}
When:  Full pipeline: load pod → expand → render each role
Then:  - Role instructions contain "backend"
       - No missing-variable error
```

### 9.5 Backward Compat E2E

```
Given: Existing role file with no {{ }} expressions and no variables: section
When:  LoadRoleRendered("existing-role", ctx)
Then:  - Identical to LoadRole("existing-role")
       - No errors, no changes to field values
```

## 10. Edge Cases

| Test | Scenario | Expected |
|------|----------|----------|
| Empty template | `""` | Returns `""` |
| Template with only whitespace | `"  \n  "` | Returns `"  \n  "` |
| Very long template | 100KB instructions | Renders without error |
| Variable value contains `{{ }}` | Var: {"x": "{{ not a template }}"} | Literal `{{ not a template }}` in output (Go templates auto-escape data) |
| Variable name with special chars | `my-var` with hyphen | Must use `index .Var "my-var"` syntax (document this) |
| Count as template expression | `count: {{ .Var.n }}` where n="3" | Parses as int 3 after rendering |
| Count as template, non-numeric | `count: {{ .Var.n }}` where n="abc" | YAML parse error (clear message) |
| Nested template delimiters | Instructions mentioning Go templates | Use `{{ "{{" }}` escape |
| Unicode in variables | Var: {"name": "日本語"} | Renders correctly |

## 11. `h2 role init` Migration

| Test | Expected |
|------|----------|
| `h2 role init myname` generates `{{ }}` syntax | Role file uses `{{ .RoleName }}` not `${name}` |
| Generated role is valid template | LoadRoleRendered succeeds on the generated file |
| Old `${name}` roles still load | No `${}` processing happens — treated as literal text |

## Test File Locations

| File | Tests |
|------|-------|
| `internal/tmpl/tmpl_test.go` | Sections 1–4: parsing, validation, rendering, functions |
| `internal/config/role_test.go` | Section 6: LoadRoleRendered, backward compat |
| `internal/config/pods_test.go` | Sections 5, 8: count expansion, name collision, var passing, two-phase rendering |
| `internal/cmd/run_test.go` or `cmd_test.go` | Section 7.1: --var flag parsing |
| `internal/cmd/pod_test.go` or `cmd_test.go` | Section 7.2: pod launch --var |

## Conformance Checklist

Before merging, all of the following must pass:

- [ ] `make build` compiles without errors
- [ ] `make test` — all unit and integration tests pass
- [ ] Manual: `h2 run --name test --role <parameterized-role> --var team=backend` — agent starts with rendered instructions
- [ ] Manual: `h2 run --role <role-with-required-var>` (no --var) — fails with clear error listing missing variables
- [ ] Manual: `h2 pod launch <template-with-count>` — correct number of agents launched with correct names
- [ ] Manual: `h2 pod launch <template> --var num_coders=5` — count responds to variable
- [ ] Manual: existing roles (no templates) load and behave identically to before
- [ ] Manual: `h2 role init newrole` — generates role with `{{ }}` syntax, loads correctly
