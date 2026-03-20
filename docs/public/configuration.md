# h2 Configuration

h2 configuration is organized into four layers:

1. **Top-level config** (`config.yaml`) — global settings: bridges and per-user config
2. **Roles** (`roles/*.yaml`) — how to launch an agent: harness, model, permissions, instructions
3. **Pods** (`pods/*.yaml`) — sets of agents and bridges to launch together
4. **Profiles** (`claude-config/`, `codex-config/`) — per-account harness auth and settings

All config lives under your **h2 directory** (default `~/.h2/`, or set via `H2_DIR`).

```
~/.h2/
├── config.yaml              # Top-level config (bridges, users)
├── roles/                   # Role definitions
│   ├── default.yaml         # Static role
│   └── coder.yaml.tmpl      # Templated role
├── pods/                    # Pod definitions
│   ├── dev-team.yaml
│   └── review-squad.yaml.tmpl
├── claude-config/           # Claude Code profiles
│   ├── default/
│   │   ├── .claude.json     # OAuth credentials
│   │   ├── settings.json    # Hooks, permissions
│   │   └── CLAUDE.md        # Shared instructions
│   └── work/
│       └── ...
├── codex-config/            # Codex profiles
│   ├── default/
│   │   ├── config.toml      # API key config
│   │   ├── requirements.toml
│   │   └── AGENTS.md
│   └── work/
│       └── ...
└── sessions/                # Runtime session state (managed by h2)
```

---

## Top-Level Config (`config.yaml`)

The top-level config defines named bridge configurations and per-user settings.

### Full structure

```yaml
# Named bridge configurations
bridges:
  my-telegram:
    telegram:
      bot_token: "123456:ABC-DEF"     # Telegram bot token (required)
      chat_id: 789                     # Telegram chat ID (required)
      allowed_commands:                # Restrict which h2 commands can be invoked (optional)
        - send
        - list
      expects_response: true           # Wait for agent responses (optional)
    macos_notify:
      enabled: true                    # Enable macOS notifications (optional)

# Per-user settings (reserved for future use)
users:
  alice: {}
```

### Bridge types

| Type | Description |
|------|-------------|
| `telegram` | Send/receive h2 messages via a Telegram bot |
| `macos_notify` | Native macOS desktop notifications |

---

## Roles (`roles/*.yaml`)

A role defines how to launch an agent. Roles can be static YAML files or Go-templated (`.yaml.tmpl`).

### Complete field reference

```yaml
# --- Identity ---
role_name: coder                     # Required. Role identifier.
description: "A coding agent"        # Optional. Human-readable description.
inherits: base-role                  # Optional. Parent role name for inheritance.

# --- Agent Naming ---
# Supports Go templates and name functions.
# Functions: randomName, autoIncrement("prefix")
# Template vars: .AgentName, .RoleName, .Var.<name>, .H2Dir
agent_name: "{{ randomName }}"

# --- Harness Selection ---
agent_harness: claude_code           # claude_code | codex | generic (default: claude_code)
agent_harness_command: /path/to/claude  # Override harness binary (optional)
agent_model: claude-sonnet-4-6       # Model override (optional; empty = harness default)

# --- Account Profile ---
profile: default                     # Profile name (default: "default")
claude_code_config_path_prefix: "{{ .H2Dir }}/claude-config"  # Optional override
codex_config_path_prefix: "{{ .H2Dir }}/codex-config"         # Optional override

# --- Permissions & Approval ---
claude_permission_mode: default      # Claude Code permission mode (see table below)
codex_sandbox_mode: workspace-write  # Codex sandbox mode (see table below)
codex_ask_for_approval: on-request   # Codex approval mode (see table below)

# Permission review strategies (see dedicated section below)
permission_review:
  dcg: { ... }
  ai_reviewer: { ... }

# --- Instructions ---
# Option A: single instructions field
instructions: |
  You are a coding agent. Write tests for everything.

# Option B: split instructions (for inheritance). Concatenated in order.
# Mutually exclusive with the single "instructions" field.
instructions_intro: "You are {{ .AgentName }}, a coding agent."
instructions_body: "Write tests for everything."
instructions_additional_1: ""
instructions_additional_2: ""
instructions_additional_3: ""

# Replace the agent's entire default system prompt (use sparingly):
# system_prompt: "You are a specialized agent that ..."

# --- Working Directory ---
working_dir: "."                     # "." = launch CWD, relative = resolve against h2-dir, absolute = as-is
additional_dirs:                     # Extra directories passed to agent via --add-dir
  - ./backend
  - /data/logs

# --- Git Worktree Mode ---
worktree_enabled: false
worktree_name: feat-auth             # Defaults to agent_name
worktree_path_prefix: "{{ .H2Dir }}/worktrees"
worktree_path: ""                    # Explicit path override (replaces prefix+name)
worktree_branch_from: main           # Base branch for git worktree add
worktree_branch: feat/auth           # Branch name. Special: "<detached_head>"

# --- Automation ---
heartbeat:
  idle_timeout: "30m"                # Go duration format
  message: "Are you still working?"
  condition: ""                      # Optional shell condition

triggers:                            # Event-triggered actions (see automation section)
  - id: nudge-on-idle
    event: state_change
    state: idle
    message: "Hey, are you stuck?"
    max_firings: -1
    cooldown: 5m

schedules:                           # Time-based actions (see automation section)
  - id: daily-check
    rrule: "FREQ=DAILY;BYHOUR=9"
    message: "Time for the daily check-in"

# --- Template Variables ---
# Define parameterized variables with optional defaults.
# Variables without defaults are required at launch time.
variables:
  env:
    description: "Target environment"
    default: "dev"
  team:
    description: "Team name"
    # No default = required at launch

# --- Native Harness Config ---
hooks: {}      # Merged into Claude Code settings.json hooks (yaml node)
settings: {}   # Extra Claude Code settings.json keys (yaml node)
```

### Permission modes

**Claude Code** (`claude_permission_mode`):

| Value | Description |
|-------|-------------|
| `default` | Normal permission flow — agent asks for approval |
| `acceptEdits` | Auto-approve file edits, ask for other tools |
| `plan` | Read-only planning mode |
| `dontAsk` | Skip tools that would require permission |
| `bypassPermissions` | Auto-approve everything (use with caution) |
| `delegate` | Delegate permission decisions to h2's permission review system |

**Codex** (`codex_sandbox_mode` / `codex_ask_for_approval`):

| Sandbox | Description |
|---------|-------------|
| `read-only` | No writes allowed |
| `workspace-write` | Can write within project |
| `danger-full-access` | Unrestricted access |

| Approval | Description |
|----------|-------------|
| `untrusted` | Ask for everything |
| `on-request` | Ask only for flagged operations |
| `never` | Never ask |

### Permission review

The `permission_review` section configures two independent strategies for reviewing agent permission requests. Both can be used together — DCG handles fast rule-based checks, and the AI reviewer handles more nuanced decisions.

```yaml
permission_review:
  # DCG: Destructive Command Guard (rule-based, fast)
  # Evaluates shell commands on PreToolUse hook events.
  dcg:
    enabled: true                      # Default: false (requires explicit opt-in)
    destructive_policy: moderate       # How strictly to flag destructive commands
    privacy_policy: strict             # How strictly to flag privacy-sensitive commands
    allowlist: []                      # Glob patterns to always allow
    blocklist: []                      # Glob patterns to always deny
    enabled_packs: []                  # Only evaluate these rule packs (empty = all)
    disabled_packs: []                 # Skip these rule packs

  # AI Reviewer: LLM-based permission reviewer
  # Evaluates PermissionRequest hook events using a fast model.
  ai_reviewer:
    enabled: true                      # Default: true when instructions are present
    model: haiku                       # Model for review (default: haiku)
    # Instructions can be a single block or split for inheritance:
    instructions_intro: "You are reviewing permission requests for the coder agent."
    instructions_body: |
      ALLOW by default:
      - h2 commands (h2 send, h2 list, h2 whoami)
      - Read-only tools (Read, Glob, Grep)
      - Standard development commands (git, npm, make, pytest, etc.)
      - File operations within the project (Edit, Write)

      DENY only for:
      - System-wide destructive operations (rm -rf /, fork bombs)
      - Exfiltrating credentials or secrets

      ASK_USER for:
      - Borderline or locally destructive commands
      - Uncertain access to credentials or secrets
      - git push --force to main/master branches
```

**DCG policy levels:**

| Policy | Behavior |
|--------|----------|
| `allow-all` | Allow everything (effectively disables the check) |
| `permissive` | Only flag obviously dangerous commands |
| `moderate` | Flag commands that modify or delete files/data |
| `strict` | Flag most commands that have side effects |
| `interactive` | Flag everything and require explicit approval |

### Agent name functions

Agent names support Go template functions for generating unique names:

```yaml
# Random adjective-noun name (e.g., "calm-brook")
agent_name: "{{ randomName }}"

# Auto-incrementing with prefix (e.g., "coder-1", "coder-2")
agent_name: "{{ .RoleName }}-{{ autoIncrement .RoleName }}"

# Fixed name
agent_name: my-agent
```

Names are resolved in two passes: first the name functions resolve, then `{{ .AgentName }}` becomes available for other template fields.

### Role inheritance

Roles can inherit from a parent with `inherits: <parent-role>`:

```yaml
# Parent: roles/base-coder.yaml
role_name: base-coder
agent_harness: claude_code
claude_permission_mode: acceptEdits
instructions_body: "Write clean code with tests."
variables:
  env:
    description: "Environment"
    default: dev

# Child: roles/backend-coder.yaml
role_name: backend-coder
inherits: base-coder
instructions_body: "Focus on Go backend services."
variables:
  env:
    description: "Environment"
    default: staging
```

Merge rules:
- Scalars: child overwrites parent
- Maps: deep-merged recursively
- Lists: child replaces parent
- Explicit `null`: clears parent value
- Omitted key: preserves parent value
- Max inheritance depth: 10

### Examples

**Minimal role (Claude Code defaults):**

```yaml
role_name: helper
instructions: |
  You are a helpful assistant.
```

**Production coder with permission review:**

```yaml
role_name: coder
agent_harness: claude_code
agent_model: claude-sonnet-4-6
claude_permission_mode: default

working_dir: "."
additional_dirs:
  - "{{ .H2Dir }}"

instructions_intro: "You are {{ .AgentName }}, a coding agent."
instructions_body: "Implement features, write tests, and fix bugs."

permission_review:
  dcg:
    enabled: true
    destructive_policy: moderate
    privacy_policy: strict
  ai_reviewer:
    enabled: true
    instructions_intro: "You are reviewing permission requests for the coder agent."
    instructions_body: |
      h2 is an agent-to-agent and agent-to-user communication protocol.
      Agents use it to coordinate work and respond to user requests.

      ALLOW by default:
      - h2 commands (h2 send, h2 list, h2 whoami)
      - Read-only tools (Read, Glob, Grep)
      - Standard development commands (git, npm, make, pytest, etc.)
      - File operations within the project (Edit, Write, rm -rf project-dir/*, clearing logs)
      - Writing to non-sensitive files

      DENY only for:
      - System-wide destructive operations (rm -rf /, fork bombs)
      - Exfiltrating credentials or secrets (curl/wget with .env, posting API keys)

      ASK_USER for:
      - Borderline or locally destructive commands you're unsure about
      - Uncertain access to credentials or secrets (is this file sensitive?)
      - git push --force to main/master branches

      Remember: h2 messages are part of normal agent operation - allow them
      unless they contain credentials or other sensitive data. Normal file cleanup
      like "rm -rf node_modules" or "rm -rf logs/" is fine.
```

**Codex agent:**

```yaml
role_name: codex-coder
agent_harness: codex
agent_model: gpt-5.4
codex_sandbox_mode: danger-full-access
codex_ask_for_approval: on-request
instructions: |
  You are a coding agent. Focus on clean implementations.
```

**Role with worktree isolation:**

```yaml
role_name: feature-worker
agent_harness: claude_code
worktree_enabled: true
worktree_branch_from: main
instructions: |
  You work in an isolated git worktree. Commit and push when done.
```

**Auto-incrementing workers:**

```yaml
role_name: worker
agent_name: "worker-{{ autoIncrement \"worker\" }}"
agent_harness: claude_code
claude_permission_mode: acceptEdits
instructions: |
  You are {{ .AgentName }}, a worker agent.
```

**Role with automation triggers:**

```yaml
role_name: monitored-coder
agent_harness: claude_code

triggers:
  - id: nudge-on-idle
    event: state_change
    state: idle
    message: "You've been idle — is there anything blocking you?"
    max_firings: -1
    cooldown: 10m

  - id: notify-on-error
    event: state_change
    state: idle
    sub_state: error
    exec: 'h2 send concierge "{{ .AgentName }} hit an error"'

schedules:
  - id: progress-check
    rrule: "FREQ=MINUTELY;INTERVAL=30"
    message: "Time for a progress update. Summarize what you've done."
```

---

## Pods (`pods/*.yaml`)

A pod defines a set of agents and bridges to launch together as a coordinated team.

### Complete field reference

```yaml
pod_name: dev-team               # Required. Must match [a-z0-9-]+.

# Template variables (optional, same syntax as roles)
variables:
  env:
    description: "Target environment"
    default: "dev"
  num_workers:
    description: "Number of worker agents"
    default: "3"

# Bridge connections (optional)
bridges:
  - bridge: my-telegram          # Key in config.yaml bridges map (required)
    concierge: scheduler         # Agent in this pod to route bridge messages to (optional)

# Agents (required, at least one)
agents:
  - name: scheduler              # Agent name (required, supports templates)
    role: concierge              # Role to use (required, supports templates)
    # count: omitted = single agent
    vars:                        # Role template variables (optional)
      env: "{{ .Var.env }}"
    overrides:                   # Role field overrides (optional)
      claude_permission_mode: bypassPermissions

  - name: "worker-{{ .Index }}"  # Template with index for multiple agents
    role: coder
    count: 3                     # Spawn 3 agents: worker-0, worker-1, worker-2
    vars:
      team: backend

  - name: reviewer
    role: reviewer
    count: 1                     # Explicit 1 (same as omitting, but .Index=0, .Count=1 available)
```

### Count expansion

| `count` value | Result |
|---------------|--------|
| Omitted (nil) | 1 agent, no index suffix |
| `0` | Skip (0 agents) |
| `1` (explicit) | 1 agent; `{{ .Index }}` = 0, `{{ .Count }}` = 1 |
| `N` (N > 1) | N agents; `{{ .Index }}` = 0..N-1, `{{ .Count }}` = N |

When `count > 1` and the name doesn't contain `{{ .Index }}`, h2 auto-appends `-{{ .Index }}` to avoid name collisions.

### Overrides

Overrides let a pod customize role fields per-agent without creating a new role:

```yaml
agents:
  - name: fast-coder
    role: coder
    overrides:
      agent_model: claude-haiku-4-5
      claude_permission_mode: bypassPermissions
```

Overrides use YAML tag names as keys. Some fields cannot be overridden: `role_name`, `instructions`, `hooks`, `settings`.

### Template context

Templates in pod files have access to:

| Variable | Description |
|----------|-------------|
| `.PodName` | Pod name |
| `.Index` | Current index in count expansion |
| `.Count` | Total count in expansion |
| `.Var.<name>` | Pod-level template variable |
| `.H2Dir` | h2 directory path |

### Examples

**Simple two-agent team:**

```yaml
pod_name: pair
agents:
  - name: coder
    role: coder
  - name: reviewer
    role: reviewer
```

**Scalable worker pool with bridge:**

```yaml
pod_name: worker-pool

variables:
  num_workers:
    description: "Number of workers"
    default: "4"

bridges:
  - bridge: my-telegram
    concierge: scheduler

agents:
  - name: scheduler
    role: concierge
    vars:
      team_size: "{{ .Var.num_workers }}"

  - name: "worker-{{ .Index }}"
    role: coder
    count: "{{ .Var.num_workers }}"
    overrides:
      claude_permission_mode: acceptEdits
```

Launch with: `h2 pod up worker-pool` or `h2 pod up worker-pool --var num_workers=8`

---

## Profiles (`claude-config/`, `codex-config/`)

Profiles store per-account harness configuration. Multiple agents can share a profile for common auth credentials and base settings, while keeping configs isolated from your personal agent setup.

### How profiles work

Each agent harness has its own native config format. h2 manages these in per-profile subdirectories:

```
~/.h2/claude-config/
├── default/                 # Default profile
│   ├── .claude.json         # OAuth credentials (managed by Claude Code)
│   ├── settings.json        # Hooks, permissions allow/deny, tool rules
│   └── CLAUDE.md            # Shared instructions for all agents using this profile
└── work/                    # Named profile
    ├── .claude.json
    ├── settings.json
    └── CLAUDE.md

~/.h2/codex-config/
├── default/
│   ├── config.toml          # API key configuration
│   ├── requirements.toml    # Prefix rules, policies
│   └── AGENTS.md            # Shared instructions
└── work/
    └── ...
```

h2 tells each harness where to find its config via environment variables:
- **Claude Code**: `CLAUDE_CONFIG_DIR=<profile_dir>`
- **Codex**: `CODEX_HOME=<profile_dir>`

### Selecting a profile

Set the `profile` field in your role config:

```yaml
role_name: work-coder
profile: work          # Uses ~/.h2/claude-config/work/
```

Default is `"default"`.

### What lives in profiles

| Setting | Claude Code | Codex |
|---------|-------------|-------|
| Auth credentials | `.claude.json` (OAuth) | `config.toml` (API key) |
| Tool allow/deny | `settings.json` → `permissions` | n/a |
| Prefix rules | n/a | `requirements.toml` |
| Hooks | `settings.json` → `hooks` | n/a |
| Shared instructions | `CLAUDE.md` | `AGENTS.md` |

### Why profiles instead of abstraction

Each harness has unique rule formats (Claude Code uses glob-based tool patterns, Codex uses structured token-prefix patterns). Rather than building a lossy abstraction, h2 lets you use each harness's native config format at full fidelity.

### Instructions hierarchy

Agents receive instructions from multiple layered sources:

**Claude Code:**
1. Global `~/.claude/CLAUDE.md` — your personal instructions (applies everywhere)
2. Profile `~/.h2/claude-config/<profile>/CLAUDE.md` — shared across agents in this profile
3. Role `system_prompt` — replaces default system prompt (if set)
4. Role `instructions` — appended to default system prompt
5. Project `CLAUDE.md` — in the agent's working directory (auto-discovered)

**Codex:**
1. ~~Global `~/.codex/AGENTS.md`~~ — **not loaded** when h2 sets `CODEX_HOME`
2. Profile `~/.h2/codex-config/<profile>/AGENTS.md` — shared instructions
3. Role `instructions` — passed via `-c instructions=<json>`
4. Project `AGENTS.md` — in working directory (auto-discovered)

### What to put where

**Profile instructions** (`CLAUDE.md` / `AGENTS.md`): Shared methodology that all agents using this profile should follow — h2 messaging protocol, coding conventions, testing strategy. Generated by `h2 init` with a base set.

**Role instructions**: What this specific agent's job is, how to approach it, role-specific constraints.

**Project instructions** (in repo): Project-specific conventions, build commands, codebase context. All agents working in that directory pick these up automatically.

---

## Automation (Triggers & Schedules)

Triggers and schedules are defined in role configs and run automatically when the agent is active.

### Triggers

Fire when an event matches. One-shot by default.

```yaml
triggers:
  - id: my-trigger               # Unique ID (optional, auto-generated if omitted)
    name: "Nudge on idle"        # Human-readable label (optional)
    event: state_change          # Event type to match (required)
    state: idle                  # State filter (optional)
    sub_state: ""                # Sub-state filter (optional)
    condition: 'test -f /tmp/go' # Shell condition; fires only if exit 0 (optional)
    max_firings: -1              # -1=unlimited, 0=one-shot (default), N=fire N times
    expires_at: "+1h"            # RFC 3339 or relative "+duration" (optional)
    cooldown: 5m                 # Minimum time between firings (optional)

    # Action (exactly one of exec or message):
    exec: 'h2 send scheduler "agent is idle"'
    # message: "Hey, need help?"
    from: h2-trigger             # Sender name for messages (optional)
    priority: normal             # interrupt | normal | idle-first | idle (optional)
```

### Schedules

Fire at times defined by an RRULE (RFC 5545 recurrence rule).

```yaml
schedules:
  - id: my-schedule              # Unique ID (optional)
    name: "Hourly check"        # Human-readable label (optional)
    rrule: "FREQ=HOURLY"        # RFC 5545 recurrence rule (required)
    start: "2026-01-01T00:00:00Z"  # Start time, RFC 3339 (optional, defaults to now)
    condition: 'test "$H2_AGENT_STATE" = "idle"'  # Shell condition (optional)
    condition_mode: run_if       # run_if | stop_when | run_once_when (optional)

    # Action (exactly one of exec or message):
    message: "Time for a progress update"
    from: h2-schedule
    priority: normal
```

**Condition modes:**

| Mode | Behavior |
|------|----------|
| `run_if` | Run action only when condition passes (default) |
| `stop_when` | Run action until condition passes, then delete schedule |
| `run_once_when` | Skip until condition passes, fire once, then delete |

### Environment variables

Both triggers and schedules inject environment variables into conditions and exec actions:

| Variable | Source | Description |
|----------|--------|-------------|
| `H2_ACTOR` | Always | Agent name |
| `H2_ROLE` | Always (if set) | Role name |
| `H2_SESSION_DIR` | Always (if set) | Session directory path |
| `H2_TRIGGER_ID` | Triggers | Trigger ID |
| `H2_EVENT_TYPE` | Triggers | Event type that fired |
| `H2_EVENT_STATE` | Triggers (state_change) | Event state |
| `H2_EVENT_SUBSTATE` | Triggers (state_change) | Event sub-state |
| `H2_AGENT_STATE` | Both | Current agent state |
| `H2_AGENT_SUBSTATE` | Both | Current agent sub-state |
| `H2_SCHEDULE_ID` | Schedules | Schedule ID |
