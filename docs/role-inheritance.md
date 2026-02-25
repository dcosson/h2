# Role Inheritance Design

## Summary

Roles can inherit from a parent role via an `inherits` field. The child role's configuration is layered on top of the parent's: variable definitions are merged, both templates are rendered with a shared variable context, and the resulting YAML configs are deep-merged with the child's values winning.

This enables creating a base role with common configuration (harness, permissions, shared instructions) and deriving specialized roles that override specific fields or narrow/widen the variable contract.

## Architecture

### New Role Field

```yaml
# child-role.yaml.tmpl
role_name: backend-coder
inherits: coder          # references parent role by name
agent_model: claude-sonnet-4-6
instructions: |
  You specialize in backend Go services.
```

The `inherits` field is a string naming the parent role. It is resolved via the same path lookup as `LoadRole` (checks `.yaml.tmpl` then `.yaml` in the roles directory). The field is structural metadata — it is extracted before template rendering and is not present in the final `Role` struct output.

### Variable Merge Semantics

Variable definitions from parent and child are merged with the child overlaying the parent:

| Parent VarDef | Child VarDef | Result | Explanation |
|---|---|---|---|
| required (no default) | defines with default | optional | Child adds a default, making it optional |
| required (no default) | defines without default | required | Child keeps it required (can change description) |
| required (no default) | omitted | **error** | Child must define all parent required vars |
| optional (has default) | defines with different default | optional (child default) | Child overrides the default |
| optional (has default) | defines without default | required | Child removes the default, making it required |
| optional (has default) | omitted | baked-in | Parent's default is baked into parent template rendering; not exposed in the child's CLI variable contract |
| — | new child-only var | as defined | Child can introduce new variables |

**Key rule**: The child **must** explicitly define every variable that is required (no default) in the parent. Optional parent variables that the child omits are silently resolved using the parent's default during parent template rendering.

Two variable views are maintained:
- **Render var defs**: used to render templates in the inheritance chain; includes parent optional vars (with defaults) even if omitted by child.
- **Exposed var defs**: used for child-role CLI/API validation; excludes parent optional vars omitted by child so callers are not offered inherited implementation details.

### Rendering Pipeline

```mermaid
flowchart TD
    A[Load child file] --> B[Extract 'inherits' + 'variables' from child]
    B --> C{inherits set?}
    C -->|no| D[Standard rendering pipeline]
    C -->|yes| E[Load parent file]
    E --> F[Extract parent 'inherits' + 'variables']
    F --> G{Parent also inherits?}
    G -->|yes| H[Recurse: load grandparent...]
    G -->|no| I[Build var defs: render defs + exposed defs]
    H --> I
    I --> J[Validate: child defines all parent required vars]
    J --> K[Merge user-provided vars with merged defs + defaults]
    K --> L["Pass 1: Render parent template (merged vars, AgentName=placeholder)"]
    K --> M["Pass 1: Render child template (merged vars, AgentName=placeholder)"]
    L --> N[Parse parent rendered YAML to map]
    M --> O[Parse child rendered YAML to map]
    N --> P["Deep merge: parent map ← child map"]
    O --> P
    P --> Q[Extract agent_name from merged map]
    Q --> R["Pass 2: Render parent template (merged vars, AgentName=resolved)"]
    Q --> S["Pass 2: Render child template (merged vars, AgentName=resolved)"]
    R --> T[Parse parent rendered YAML to map]
    S --> U[Parse child rendered YAML to map]
    T --> V["Deep merge: parent map ← child map"]
    U --> V
    V --> W[Unmarshal merged map to Role struct]
    W --> X[Validate final Role]
```

### Detailed Steps

**1. Load and extract metadata**

Read the child's raw file. Use `extractYAMLSection` (existing function in `tmpl.go`) to pull out the `variables` block and a new `extractInherits` to pull out the `inherits` value. Both are extracted as raw strings before any template rendering.

**2. Resolve inheritance chain**

If `inherits` is set, resolve the parent role path via `resolveRolePath`. Load the parent's raw file and extract its `inherits` and `variables`. Recurse if the parent also inherits. Build an ordered chain: `[grandparent, parent, child]`.

Circular inheritance is detected by tracking visited role names during resolution.

**Depth limit**: Maximum inheritance depth of 10 to prevent runaway chains.

**3. Build variable definitions**

Walk the chain from ancestor to descendant and build two def maps:
- `renderDefs`: ancestor-to-descendant overlay (used for template rendering)
- `exposedDefs`: the child's public variable contract
  - include all child-defined vars
  - include parent required vars (child must redefine each)
  - exclude parent optional vars omitted by child

```go
// mergeVarDefs overlays child defs on top of parent defs.
func mergeVarDefs(parent, child map[string]VarDef) map[string]VarDef {
    merged := make(map[string]VarDef, len(parent)+len(child))
    for k, v := range parent {
        merged[k] = v
    }
    for k, v := range child {
        merged[k] = v
    }
    return merged
}

// buildExposedVarDefs returns the child's public variable contract.
// Parent optional vars omitted by child are intentionally hidden.
func buildExposedVarDefs(parentDefs, childDefs map[string]VarDef) map[string]VarDef {
    exposed := make(map[string]VarDef, len(childDefs))
    for k, v := range childDefs {
        exposed[k] = v
    }
    for k, v := range parentDefs {
        if v.Required() {
            if cv, ok := childDefs[k]; ok {
                exposed[k] = cv
            }
        }
    }
    return exposed
}
```

**4. Validate child covers parent required vars**

After merging, check that every variable that was required in the parent (before merging) is present in the child's variable definitions. The child doesn't need to keep it required — it can add a default — but it must acknowledge the variable exists. This prevents silent breakage when a parent adds a new required variable.

```go
func validateChildCoversParentRequired(parentDefs, childDefs map[string]VarDef) error {
    var missing []string
    for name, def := range parentDefs {
        if def.Required() {
            if _, ok := childDefs[name]; !ok {
                missing = append(missing, name)
            }
        }
    }
    // ... error with list of missing vars
}
```

**5. Merge user vars with defaults and validate**

Use:
- `renderDefs` for resolving defaults used during template execution.
- `exposedDefs` for `ValidateVars` + `ValidateNoUnknownVars` so callers only pass variables the child role explicitly exposes.

**6. Two-pass rendering**

For each pass (placeholder then resolved AgentName):
- Render each template in the chain with the **same merged context** (same vars, same AgentName)
- Parse each rendered YAML string into `map[string]interface{}`
- Deep merge the maps in chain order (ancestor first, descendant last wins)

After pass 1: extract `agent_name` from the merged map.
After pass 2: unmarshal the final merged map into `Role`, validate.

### Deep Merge Semantics

The YAML deep merge follows standard rules:

| Type | Behavior |
|---|---|
| Scalar (string, int, bool) | Child overwrites parent |
| Map/object | Recursively merge |
| List/array | Child replaces parent entirely |
| Key omitted in child | Parent value preserved |
| Key set to `null` in child | Child overwrites parent with `null` |

Lists replace rather than append because:
- `additional_dirs: [./backend]` in the child means "these are the dirs I want", not "add to parent's dirs"
- If the child wants to include parent values, they can list them explicitly
- Append semantics would make it impossible to remove a parent's list entry

**Special fields during merge:**
- `role_name`: Always taken from the child (it defines the child's identity)
- `inherits`: Stripped from merged output (not a Role struct field)
- `variables`: Not part of the YAML merge — handled separately in the variable merge step

### `yaml.Node` Fields (hooks, settings)

The `hooks` and `settings` fields are `yaml.Node` in the Role struct, and are merged using a node-aware merge path (not plain map round-tripping) so tag-sensitive YAML can be preserved in those sections.

Default behavior contract for `yaml.Node` round-tripping in inheritance:
- Mapping content is merged recursively using map semantics.
- Sequence content is replaced by child when the child provides the key.
- Scalar values are replaced by child values.
- Explicit `null` in child clears parent value.
- Omitted key in child keeps parent value.
- YAML comments are not preserved (acceptable loss).
- YAML anchors/aliases are normalized to semantic values; alias identity is not preserved.
- Standard YAML tags are allowed (for example `!!str`, `!!int`) and follow normal yaml.v3 decoding behavior.
- Custom YAML tags are preserved for `hooks`/`settings`.
- Custom YAML tags outside `hooks`/`settings` are rejected with a deterministic error because inheritance merge cannot preserve them there.

## Examples

### Base coder role (parent)

```yaml
# roles/coder.yaml.tmpl
role_name: coder
agent_harness: claude_code
permission_mode: acceptEdits

variables:
  agent_model:
    description: "Model to use"
    default: "claude-sonnet-4-6"
  team:
    description: "Team name"

instructions: |
  You are a {{ .Var.team }} coding agent using {{ .Var.agent_model }}.
  Write clean code and tests.

permission_review_agent:
  instructions: |
    ALLOW: standard dev tools (read, write, edit, test)
    DENY: destructive operations (rm -rf, force push)
```

### Specialized backend role (child)

```yaml
# roles/backend-coder.yaml.tmpl
role_name: backend-coder
inherits: coder

variables:
  # Parent's 'team' was required — provide a default to make optional
  team:
    description: "Team name"
    default: "platform"
  # Parent's 'agent_model' was optional — omitted here, so parent's
  # default ("claude-sonnet-4-6") is baked in
  #
  # New child-only variable
  service:
    description: "Service to work on"

instructions: |
  You specialize in the {{ .Var.service }} backend service.
  Focus on Go code, database queries, and API design.
```

**Result after merge** (with `--var service=auth-api`):

```yaml
role_name: backend-coder
agent_harness: claude_code         # from parent
permission_mode: acceptEdits       # from parent
instructions: |                    # from child (overwrites parent)
  You specialize in the auth-api backend service.
  Focus on Go code, database queries, and API design.
permission_review_agent:           # from parent (child didn't override)
  instructions: |
    ALLOW: standard dev tools (read, write, edit, test)
    DENY: destructive operations (rm -rf, force push)
```

Note that the parent's `instructions` field referenced `{{ .Var.team }}` and `{{ .Var.agent_model }}`, but since the child overwrote `instructions` entirely, those references don't appear in the output. If the child had NOT overridden `instructions`, the parent's template would have rendered with `team=platform` (child's default) and `agent_model=claude-sonnet-4-6` (parent's default, baked in).

### Child that extends instructions via split fields

```yaml
# roles/strict-coder.yaml.tmpl
role_name: strict-coder
inherits: coder

variables:
  team:
    description: "Team name"

# Don't override instructions — parent's instructions are preserved.
# Add extra constraints via permission_review_agent.
permission_review_agent:
  instructions: |
    ALLOW: read, write, edit only
    DENY: ALL bash commands, destructive operations
    ASK_USER: anything else
```

Here the parent's `instructions` field is preserved (rendered with the child's variable values), while only the `permission_review_agent` is overridden.

## CLI Behavior

### `h2 role list`

- Inherited roles are marked inline: `child-role ... (inherits: parent-role)`.
- Pod role listing is still grouped; inheritance markers are shown when present in pod role files too.

### `h2 role show <name>`

- Displays:
  - `Inherits: <direct-parent>` when inheritance is configured.
  - `Chain: base -> ... -> child` for multi-level inheritance.
  - `Variables` with origin tags (`[from: <role>]`) for the exposed child contract.
  - `Inherited Variables (hidden from child contract)` for inherited optional vars used at render-time but not exposed to callers.

### `h2 role check <name>`

- Validates the full inheritance graph first (parent resolution, cycle detection, depth limit, parser errors).
- On failure, emits an actionable error:
  - `role "<name>" inheritance validation failed: ...`
- On success, prints inheritance metadata (`Inherits`, `Chain`) with the normal role validity summary.

## Troubleshooting

### Unknown parent role

Example:
`role "child" inheritance validation failed: role "child" inherits unknown parent role "missing-parent"`

Fix: create the parent role in `~/.h2/roles/` (as `.yaml.tmpl` or `.yaml`) or correct the `inherits` value.

### Circular inheritance

If role A inherits B and B inherits A (directly or transitively), loading fails with a circular inheritance error.

Fix: break the cycle so inheritance forms a DAG.

### Inheritance depth limit

Inheritance depth is capped at 10.

Fix: flatten the hierarchy by collapsing intermediate roles.

### Required parent variable not redefined in child

If a parent required var is omitted in the child var definitions, load/check fails.

Fix: define that variable in the child `variables:` block (with or without a default).

### Custom YAML tags

- Custom tags are supported in `hooks` and `settings` and are preserved there.
- Custom tags outside `hooks`/`settings` are rejected in inheritance merge with a deterministic error.

Fix: move custom-tagged content into `hooks`/`settings`, or replace it with standard YAML tags/values.

## Final Contract

1. `inherits` supports multi-level static chains (not template-rendered), with max depth 10.
2. Parent roles are resolved only from global roles (`~/.h2/roles`), not pod-scoped role directories.
3. Variable contracts are split into:
   - render-time defs (full chain)
   - exposed defs (child contract shown/validated by CLI/API)
4. Deep merge semantics:
   - scalar overwrite
   - map recursive merge
   - sequence replacement
   - explicit `null` clearing
   - omitted key preservation
5. `yaml.Node` policy:
   - semantic merge for `hooks`/`settings`
   - comment/anchor identity loss is acceptable
   - custom-tag fail-hard outside `hooks`/`settings`
