# Design: Agent Roles & Permission Handling

## Problem

Today, launching an h2 agent requires manually assembling the right combination
of config: CLAUDE.md instructions, settings.json permissions, hook commands, model
selection, and other flags. There's no way to say "launch an architect agent" —
you have to know the exact incantation.

We also have a permission review hook (`~/.claude/hooks/review-permission.sh`)
that works but is a standalone bash script outside h2's awareness. h2 doesn't
know whether an agent is blocked on a permission request, and the permission
rules aren't tied to the agent's role.

## Concept: Roles

A **role** is a named configuration bundle that defines everything an agent needs
to run: its instructions, permissions, model, hooks, and any other Claude Code
settings. Roles live in `~/.h2/roles/` as YAML files.

When you launch an agent with a role, h2:
1. Creates a per-agent session directory
2. Generates Claude Code config files (CLAUDE.md, settings.json) from the role
3. Injects h2's standard hooks (hook collector, permission handler)
4. Launches the agent pointed at that config

```
~/.h2/roles/
├── architect.yaml
├── coder.yaml
├── reviewer.yaml
└── ops.yaml
```

### Why YAML

Role files contain long markdown strings (instructions), structured permission
rules, and nested hook configs. YAML handles multi-line strings cleanly with `|`
blocks, is human-readable, and is the standard format for config files that mix
prose with structure.

## Role File Format

```yaml
# ~/.h2/roles/architect.yaml
name: architect
description: "Designs systems and reviews architecture decisions"

# Model selection — passed to Claude Code via --model
model: opus

# Permission mode — passed to Claude Code via --permission-mode
# Valid values: default, delegate, acceptEdits, plan, dontAsk, bypassPermissions
# claude_permission_mode: plan

# System prompt — replaces Claude Code's entire default system prompt (--system-prompt).
# Use this when you need full control over the prompt. Mutually exclusive in
# practice with "instructions" — use one or the other, or both if the role needs
# a custom base prompt plus additional context appended to it.
# system_prompt: |
#   You are a specialized architecture reviewer...

# Instructions — appended to Claude Code's default system prompt (--append-system-prompt).
# This is the most common choice: the agent keeps Claude Code's built-in tool usage
# instructions and gets your role-specific guidance on top.
instructions: |
  You are an architect agent. Your responsibilities:
  - Design system architecture for requested features
  - Write design documents with clear interfaces and contracts
  - Review other agents' design proposals
  - Consider scalability, maintainability, and security

  When writing designs:
  - Start with the problem statement
  - Enumerate approaches with trade-offs
  - Propose a specific recommendation
  - Include a testing strategy

  You have access to the full codebase for reference.
  Use h2 send to communicate with other agents.

# Permissions — allow/deny enforced natively by Claude Code,
# agent section configures the AI reviewer for everything else.
permissions:
  # Always allow these tools/patterns (enforced by Claude Code itself)
  allow:
    - "Read"
    - "Glob"
    - "Grep"
    - "WebSearch"
    - "WebFetch"
    - "Write(docs/**)"
    - "Edit(docs/**)"

  # Always deny these (enforced by Claude Code itself)
  deny:
    - "Bash(rm -rf *)"
    - "Bash(sudo *)"

  # AI reviewer — handles requests not matched by allow/deny
  agent:
    enabled: true
    instructions: |
      You are reviewing permission requests for an architect agent.
      This agent designs systems and writes documentation.
      ALLOW: read-only tools, standard dev commands, writing to docs/
      DENY: destructive operations, system modifications, force pushes
      ASK_USER: anything modifying source code, publishing, or secrets

# Additional Claude Code settings (merged into settings.json)
settings:
  # Any valid settings.json keys
  enabledPlugins: {}
```

### Minimal role

```yaml
name: coder
instructions: |
  You are a coding agent. Implement features as requested.
  Write tests for all changes. Run make test before committing.
permissions:
  allow:
    - "Read"
    - "Glob"
    - "Grep"
    - "Bash"
    - "Write"
    - "Edit"
  agent:
    instructions: |
      You are reviewing permissions for a coding agent.
      ALLOW: all standard dev tools (read, write, edit, bash, grep, glob)
      DENY: destructive ops (rm -rf, sudo, force push)
      ASK_USER: anything unusual
```

### Role with custom hooks

Roles can add hooks beyond h2's standard ones:

```yaml
name: ops
model: sonnet
instructions: |
  You are an operations agent. Monitor systems and fix issues.
hooks:
  PostToolUse:
    - matcher: "Bash"
      command: "notify-on-deploy.sh"
      timeout: 10
```

## Session Directory Structure

When `h2 run --role architect --name arch-1` launches, h2 creates:

```
~/.h2/sessions/arch-1/
├── .claude/
│   └── settings.json      # Generated from role permissions + hooks + settings
└── permission-reviewer.md # Instructions for AI permission reviewer
```

Role instructions are passed directly to the Claude process via the
`--append-system-prompt` flag (not written as CLAUDE.md). This appends to
Claude Code's default system prompt, preserving its built-in tool usage
instructions. The config directory is shared across parallel agents of the
same role, so writing per-agent CLAUDE.md files would conflict.

The agent's Claude Code instance is launched with its config directory pointing
at `~/.h2/sessions/<name>/.claude/`. This isolates each agent's config while
letting h2 control the full setup.

The `permission-reviewer.md` file lives at the session root (not inside
`.claude/`) because it's consumed by `h2 permission-request`, not by Claude
Code itself.

### Generated settings.json

h2 builds the settings.json by merging:
1. Role's permission rules → `permissions.allow` / `permissions.deny`
2. Role's custom hooks → merged with h2 standard hooks
3. Role's additional settings → any extra keys
4. h2 standard hooks → always injected (hook collector, permission handler)

If `permissions.agent` is enabled, h2 also writes `permission-reviewer.md`
to the session directory from `permissions.agent.instructions`.

```json
{
  "model": "opus",
  "permissions": {
    "allow": [
      "Read",
      "Glob",
      "Grep",
      "WebSearch",
      "WebFetch",
      "Write(docs/**)",
      "Edit(docs/**)"
    ],
    "deny": [
      "Bash(rm -rf *)",
      "Bash(sudo *)"
    ]
  },
  "hooks": {
    "PreToolUse": [{"matcher": "", "hooks": [
      {"type": "command", "command": "h2 hook collect", "timeout": 5}
    ]}],
    "PostToolUse": [{"matcher": "", "hooks": [
      {"type": "command", "command": "h2 hook collect", "timeout": 5}
    ]}],
    "SessionStart": [{"matcher": "", "hooks": [
      {"type": "command", "command": "h2 hook collect", "timeout": 5}
    ]}],
    "Stop": [{"matcher": "", "hooks": [
      {"type": "command", "command": "h2 hook collect", "timeout": 5}
    ]}],
    "PermissionRequest": [{"matcher": "", "hooks": [
      {"type": "command", "command": "h2 permission-request", "timeout": 60},
      {"type": "command", "command": "h2 hook collect", "timeout": 5}
    ]}]
  }
}
```

## Permission Handling

### Current state

A standalone bash script (`~/.claude/hooks/review-permission.sh`) calls
`claude --print --model haiku` to review each permission request. It works
but has limitations:
- Rules aren't tied to agent roles (global for all agents)
- h2 doesn't know when an agent is blocked on permission
- The script is fragile (bash parsing, temp files, error handling)

### Two-layer permission model

**Layer 1: Claude Code native enforcement.** The role's `permissions.allow`
and `permissions.deny` lists go directly into settings.json. Claude Code
enforces these itself — allowed tools proceed without any hook, denied tools
are blocked before any hook fires. This is fast and deterministic.

**Layer 2: AI reviewer via `h2 permission-request`.** For permission requests
that aren't covered by the native allow/deny lists, Claude Code fires the
PermissionRequest hook. `h2 permission-request` reads the role's
`permission-reviewer.md` from the agent's session directory and passes it
to `claude --print --model haiku` along with the tool request details.

### Proposed: `h2 permission-request`

A new h2 command that handles the AI reviewer layer:

```
h2 permission-request [--agent <name>]
```

- Defaults `--agent` to `$H2_ACTOR` (same pattern as `h2 hook collect`)
- Registered as a PermissionRequest hook in every role's generated settings.json
- Reads the permission request JSON from stdin
- Looks up the agent's session directory (`~/.h2/sessions/<agent>/`)
- Reads `permission-reviewer.md` for role-specific reviewer instructions
- If `permissions.agent` is enabled: calls `claude --print --model haiku` with
  the reviewer instructions + request, returns the decision
- If `permissions.agent` is not enabled (or no reviewer instructions): returns
  empty (falls through to Claude Code's built-in permission dialog)
- Reports "blocked" state to h2 when escalating to user

### Permission decision flow

```
Claude Code encounters a permission-requiring action
  → Layer 1: checks settings.json allow/deny lists
  → if allow list match: proceed (no hook fired)
  → if deny list match: blocked (no hook fired)
  → if neither: fire PermissionRequest hook

PermissionRequest hook fires
  → h2 permission-request reads stdin JSON
  → extracts tool_name, tool_input
  → reads ~/.h2/sessions/<agent>/permission-reviewer.md

  If permissions.agent enabled (reviewer instructions exist):
    → calls claude --print --model haiku with reviewer instructions + request
    → if ALLOW: return {"decision": "allow"}
    → if DENY:  return {"decision": "deny", "reason": "..."}
    → if ASK_USER:
        → sends hook_event to h2 with "blocked_permission" state
        → returns empty (falls through to Claude Code's built-in permission dialog)
        → user must h2 attach to approve/deny

  If permissions.agent not enabled (no reviewer instructions):
    → sends hook_event to h2 with "blocked_permission" state immediately
    → returns empty (falls through to Claude Code's built-in permission dialog)
    → user must h2 attach to approve/deny
```

### Blocked state

When a permission request escalates to the user, h2 should reflect this in
the agent's status. We add a sub-state to the hook collector:

```
Agent state: active
Hook detail: "blocked (permission: Bash)"
```

This shows up in `h2 list` and the status bar. The HookCollector already
tracks the last event — we extend it to track "blocked on permission" as a
specific condition that persists until the next non-PermissionRequest event.

The status logic depends on whether `permissions.agent` is enabled:

- **Agent enabled**: When h2 permission-request fires, the agent is
  "processing permission" (not yet blocked on user). Only if the AI reviewer
  returns ASK_USER does the agent become blocked. The hook_event with
  "blocked_permission" state is sent at that point.

- **Agent not enabled**: As soon as the PermissionRequest hook fires, the
  agent is immediately blocked on the user. The hook_event with
  "blocked_permission" state is sent right away.

In both cases, the blocked state clears when the next PreToolUse event fires
(the approved tool begins executing).

Implementation: add a `blockedOnPermission bool` and `blockedToolName string`
to HookCollector. Set when we receive a "blocked_permission" hook_event from
`h2 permission-request`. Clear on any subsequent PreToolUse, UserPromptSubmit,
Stop, or other non-PermissionRequest event.

### Permission rule format

Permission rules in the role file use Claude Code's native permission syntax
and are copied directly into settings.json. Claude Code enforces these itself:

```yaml
permissions:
  allow:
    - "Read"                    # Tool name only — allow all uses
    - "Write(docs/**)"          # Tool with path pattern
    - "Bash(make *)"            # Tool with command pattern
    - "Bash(go test *)"
    - "Edit(internal/**/*.go)"
  deny:
    - "Bash(rm -rf *)"
    - "Bash(sudo *)"
    - "Bash(git push --force *)"
```

Anything not matched by allow or deny triggers the PermissionRequest hook,
where the AI reviewer (configured via `permissions.agent`) decides
ALLOW, DENY, or ASK_USER.

## Launch Flow

```
h2 run --role architect --name arch-1
  → load ~/.h2/roles/architect.yaml
  → validate role (required fields, permission syntax)
  → create ~/.h2/sessions/arch-1/
  → generate .claude/settings.json from role (permissions + hooks + settings)
  → write permission-reviewer.md from role permissions.agent.instructions
  → generate session UUID
  → ForkDaemon(name, sessionID, "claude", args, instructions)
    → daemon sets ExtraEnv with:
        H2_ACTOR=arch-1
        H2_ROLE=architect
        H2_SESSION_DIR=~/.h2/sessions/arch-1
        CLAUDE_CONFIG_DIR=~/.h2/sessions/arch-1/.claude
        (+ OTEL env vars from collector)
    → starts claude with --session-id <uuid> --append-system-prompt <instructions>
    → instructions are appended to Claude Code's default system prompt
    → claude reads settings.json from the config dir
```

### `--role` flag on `h2 run`

```
h2 run --role <role-name> [--name <agent-name>] [--detach] [-- extra-args...]
```

- `--role` loads the role file and sets up the session directory
- `--name` defaults to the role name if omitted (e.g., `--role architect` → name "architect")
- The `-- command` is optional when using `--role` — defaults to `claude`
- Extra args after `--` are appended to the claude command

### Without `--role`

The existing `h2 run --name foo -- claude` flow continues to work as-is.
No role, no session directory, no generated config. This is the escape hatch
for manual/custom setups.

## CLI Commands

### `h2 role list`

List available roles:

```
$ h2 role list
architect    Designs systems and reviews architecture decisions
coder        Implements features and writes tests
reviewer     Reviews code changes and designs
ops          Monitors systems and handles incidents
```

### `h2 role show <name>`

Display a role's full configuration:

```
$ h2 role show architect
Name:        architect
Model:       opus
Description: Designs systems and reviews architecture decisions

Instructions:
  You are an architect agent...

Permissions:
  Allow: Read, Glob, Grep, WebSearch, WebFetch, Write(docs/**), Edit(docs/**)
  Deny:  Bash(rm -rf *), Bash(sudo *)
  Agent: enabled
```

### `h2 permission-request`

Handle permission requests (designed to be called as a hook, not manually):

```
# Called by Claude Code as a PermissionRequest hook:
echo '{"tool_name":"Bash","tool_input":{"command":"make test"}}' | h2 permission-request --agent arch-1
# stdout: {"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}
```

## Implementation Plan

### Phase 1: Role loading and session setup

1. Define role YAML schema in `internal/config/role.go`
2. Role loading: search `~/.h2/roles/`, parse YAML, validate
3. Session directory creation: `~/.h2/sessions/<name>/`
4. Config generation: role → CLAUDE.md + settings.json + permission-reviewer.md
5. Update `h2 run` to accept `--role` flag
6. Update `ForkDaemon` / `RunDaemon` to pass session dir config
7. Add `h2 role list` and `h2 role show` commands

### Phase 2: Permission handling

8. Create `h2 permission-request` command
9. Read permission-reviewer.md from session dir
10. AI reviewer via `claude --print --model haiku` with reviewer instructions
11. Hook event for "blocked on permission" state
12. Update HookCollector with blocked state tracking
13. Update AgentInfo / h2 list / status bar to show blocked state

### Phase 3: Polish

14. `h2 role init` — scaffold a new role file with defaults
15. Role validation command (`h2 role check <name>`)
16. Session cleanup (`~/.h2/sessions/` garbage collection)

## Files to Create/Modify

**New files:**
- `~/.h2/roles/*.yaml` — role definitions (user-created)
- `internal/config/role.go` — role struct, loading, validation
- `internal/config/role_test.go` — role parsing tests
- `internal/cmd/role.go` — `h2 role list/show` commands
- `internal/cmd/permission_request.go` — `h2 permission-request` command

**Modified files:**
- `internal/cmd/run.go` — add `--role` flag, session dir setup
- `internal/cmd/root.go` — register role and permission-request commands
- `internal/session/daemon.go` — accept config dir, pass through env
- `internal/session/session.go` — support ExtraEnv for config dir
- `internal/session/agent/hook_collector.go` — blocked state tracking
- `internal/session/agent/agent.go` — expose blocked state
- `internal/session/message/protocol.go` — blocked field on AgentInfo
- `internal/cmd/ls.go` — display blocked state

## Decisions

1. **YAML for roles.** Markdown instructions are the primary content, and YAML's
   `|` blocks handle multi-line strings cleanly. JSON would be painful for this.

2. **Per-agent session directories.** Each agent gets isolated config in
   `~/.h2/sessions/<name>/`. This prevents agents from interfering with each
   other's settings and makes cleanup straightforward.

3. **Two-layer permissions.** Claude Code natively enforces allow/deny rules
   from settings.json (fast, deterministic). `h2 permission-request` handles
   the AI reviewer layer with role-specific instructions from
   `permission-reviewer.md`. This separates concerns cleanly: Claude Code
   handles the rules it knows, h2 handles the judgment calls.

4. **AI reviewer as fallback.** Rather than requiring exhaustive allow/deny
   rules, uncertain requests go to a fast/cheap model (haiku) for review
   with role-specific context. This is the current approach and works well
   in practice.

5. **User approves via attach.** When a permission escalates to ask_user,
   the user attaches to the agent's session to approve/deny through Claude
   Code's built-in dialog. Future work can add bridge-based approval.

6. **Roles are optional.** `h2 run -- claude` without `--role` works exactly
   as it does today. Roles are an opt-in layer for structured agent management.

7. **h2 injects standard hooks.** Every role-based agent gets h2's hook collector
   and permission handler hooks. Role-specific hooks are merged in alongside them.
   The user doesn't need to configure h2 hooks manually.
