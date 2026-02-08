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

# Model selection
model: opus

# Instructions — becomes CLAUDE.md content
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

# Permission rules for automated permission handling
permissions:
  # Always allow these tools/patterns
  allow:
    - "Read"
    - "Glob"
    - "Grep"
    - "WebSearch"
    - "WebFetch"
    - "Write(docs/**)"
    - "Edit(docs/**)"

  # Always deny these
  deny:
    - "Bash(rm -rf *)"
    - "Bash(sudo *)"

  # Everything else: ask user (via attach)

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
│   ├── CLAUDE.md          # Generated from role instructions
│   └── settings.json      # Generated from role permissions + hooks + settings
└── workspace -> /path/to/project   # Symlink to working directory
```

The agent's Claude Code instance is launched with its config directory pointing
at `~/.h2/sessions/<name>/.claude/`. This isolates each agent's config while
letting h2 control the full setup.

### Generated settings.json

h2 builds the settings.json by merging:
1. Role's permission rules → `permissions.allow` / `permissions.deny`
2. Role's custom hooks → merged with h2 standard hooks
3. Role's additional settings → any extra keys
4. h2 standard hooks → always injected (hook collector, permission handler)

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

### Proposed: `h2 permission-request`

A new h2 command that handles permission requests as a first-class feature:

```
h2 permission-request [--agent <name>]
```

- Registered as a PermissionRequest hook in every role's generated settings.json
- Reads the permission request JSON from stdin (same as any hook)
- Checks the agent's role permission rules (allow/deny lists)
- For uncertain cases, uses `claude --print --model haiku` for review (same
  approach as the current script, but with role-specific context)
- Returns the decision on stdout in Claude Code's hook format
- Reports "blocked" state to h2 when escalating to user

### Permission decision flow

```
Claude Code fires PermissionRequest hook
  → h2 permission-request reads stdin JSON
  → extracts tool_name, tool_input
  → checks role's allow list → if match: return {"decision": "allow"}
  → checks role's deny list  → if match: return {"decision": "deny", "reason": "..."}
  → calls claude --print --model haiku with role context for review
  → if ALLOW: return {"decision": "allow"}
  → if DENY:  return {"decision": "deny", "reason": "..."}
  → if ASK_USER:
      → sends hook_event to h2 with "blocked_permission" state
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

Implementation: add a `blockedOnPermission bool` and `blockedToolName string`
to HookCollector. Set when we see a PermissionRequest that escalates to user.
Clear on any subsequent event (UserPromptSubmit, PreToolUse, Stop, etc.).

### Permission rule format

Permission rules in the role file use the same glob syntax as Claude Code's
settings.json permissions:

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

Rules are checked in order: allow first, then deny. If neither matches,
the request goes to the AI reviewer (claude --print), which can return
ALLOW, DENY, or ASK_USER.

## Launch Flow

```
h2 run --role architect --name arch-1
  → load ~/.h2/roles/architect.yaml
  → validate role (required fields, permission syntax)
  → create ~/.h2/sessions/arch-1/
  → generate .claude/CLAUDE.md from role instructions
  → generate .claude/settings.json from role (permissions + hooks + settings)
  → generate session UUID
  → ForkDaemon(name, sessionID, "claude", ["--append-system-prompt", "<role context>"])
    → daemon sets ExtraEnv with:
        H2_ACTOR=arch-1
        H2_ROLE=architect
        CLAUDE_CONFIG_DIR=~/.h2/sessions/arch-1/.claude
        (+ OTEL env vars from collector)
    → starts claude with --session-id <uuid>
    → claude reads CLAUDE.md and settings.json from the session dir
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
4. Config generation: role → CLAUDE.md + settings.json
5. Update `h2 run` to accept `--role` flag
6. Update `ForkDaemon` / `RunDaemon` to pass session dir config
7. Add `h2 role list` and `h2 role show` commands

### Phase 2: Permission handling

8. Create `h2 permission-request` command
9. Permission rule matching (allow/deny patterns)
10. AI reviewer fallback (claude --print --model haiku)
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

3. **Permission handling in h2.** Moving from a standalone bash script to
   `h2 permission-request` gives us: role-aware rules, blocked state tracking,
   and a cleaner implementation (Go instead of bash/jq).

4. **AI reviewer as fallback.** Rather than requiring exhaustive allow/deny
   rules, uncertain requests go to a fast/cheap model (haiku) for review.
   This is the current approach and works well in practice.

5. **User approves via attach.** When a permission escalates to ask_user,
   the user attaches to the agent's session to approve/deny through Claude
   Code's built-in dialog. Future work can add bridge-based approval.

6. **Roles are optional.** `h2 run -- claude` without `--role` works exactly
   as it does today. Roles are an opt-in layer for structured agent management.

7. **h2 injects standard hooks.** Every role-based agent gets h2's hook collector
   and permission handler hooks. Role-specific hooks are merged in alongside them.
   The user doesn't need to configure h2 hooks manually.
