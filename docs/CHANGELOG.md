# Changelog

## v0.2.0

Major release that refactors agent architecture, simplifies role configuration,
and adds agent naming features. **Contains breaking changes** — see the
migration guide below.

### Breaking Changes

#### Role config field renames and removals

The role YAML schema has changed significantly. Existing role files will need
to be updated.

**Renamed fields:**

| v0.1.0 | v0.2.0 | Notes |
|---|---|---|
| `name` | `role_name` | Frees `name` for future use; clarifies intent |
| `agent_type` | `agent_harness` | Values: `claude_code` (default), `codex`, `generic` |
| `model` | `agent_model` | Empty means use agent app's own default |

**Removed fields:**

| v0.1.0 field | Replacement |
|---|---|
| `permissions.allow` | Use harness-native configs (e.g. Claude Code `allowedTools` in settings) |
| `permissions.deny` | Use harness-native configs |
| `permissions.agent` | Moved to top-level `permission_review_agent` |

**New fields:**

| Field | Description |
|---|---|
| `agent_name` | Template-rendered agent name (see Agent Naming below) |
| `agent_harness_command` | Command override for any harness |
| `agent_account_profile` | Account profile name (default: `default`) |
| `codex_sandbox_mode` | Codex `--sandbox` flag (`read-only`, `workspace-write`, `danger-full-access`) |
| `codex_ask_for_approval` | Codex `--ask-for-approval` flag (`untrusted`, `on-request`, `never`) |
| `permission_review_agent` | AI permission reviewer (replaces `permissions.agent`) |
| `claude_code_config_path` | Explicit Claude Code config path override |
| `claude_code_config_path_prefix` | Prefix for Claude Code config paths |
| `codex_config_path` | Explicit Codex config path override |
| `codex_config_path_prefix` | Prefix for Codex config paths |

#### Default model and permission flags no longer forced

h2 no longer injects default `--model` or `--permission-mode` flags when
launching agents. If these fields are empty in the role config, the agent
harness uses its own defaults. This makes agent behavior more predictable and
avoids overriding user-level configurations.

#### Permission model simplified

The unified `approval_policy` field (which mapped to each harness's native
permission flags) has been removed in favor of using harness-native fields
directly:

- **Claude Code**: Set `permission_mode` (maps to `--permission-mode`)
- **Codex**: Set `codex_ask_for_approval` (maps to `--ask-for-approval`) and
  `codex_sandbox_mode` (maps to `--sandbox`)

This gives you direct control over each harness's permission system without
an abstraction layer in between.

### Migration Guide

Update your role YAML files from v0.1.0 to v0.2.0 format:

```yaml
# v0.1.0
name: my-role
agent_type: claude
model: sonnet
permission_mode: plan
permissions:
  agent:
    enabled: true
    instructions: "Review all file writes"
instructions: |
  You are a helpful assistant.

# v0.2.0
role_name: my-role
agent_harness: claude_code
agent_model: sonnet
permission_mode: plan
permission_review_agent:
  enabled: true
  instructions: "Review all file writes"
instructions: |
  You are a helpful assistant.
```

For Codex roles:

```yaml
# v0.1.0
name: codex-role
agent_type: codex
model: gpt-5.3-codex

# v0.2.0
role_name: codex-role
agent_harness: codex
agent_model: gpt-5.3-codex
codex_ask_for_approval: on-request
codex_sandbox_mode: workspace-write
```

### Agent Naming

Roles can now specify an `agent_name` field with template functions for
automatic name generation:

- **`{{ randomName }}`** — generates a random `adjective-noun` name with
  collision detection against running agents
- **`{{ autoIncrement "prefix" }}`** — scans running agents for
  `<prefix>-1`, `<prefix>-2`, etc. and returns the next number

Examples:

```yaml
role_name: worker
agent_name: "{{ randomName }}"
instructions: |
  Your name is {{ .AgentName }}.
```

```yaml
role_name: builder
agent_name: '{{ autoIncrement "builder" }}'
instructions: |
  You are {{ .AgentName }}.  # resolves to builder-1, builder-2, etc.
```

The agent name is resolved via two-pass template rendering — first pass
resolves the `agent_name` field, second pass re-renders the full YAML with
the resolved name available as `{{ .AgentName }}`.

The CLI `--name` flag still takes precedence over the role's `agent_name`
field.

### Architecture: Agent Harness Refactor

The internal agent architecture has been significantly refactored. The
previous `AgentType` + `AgentAdapter` pattern has been replaced with a
unified `Harness` interface:

- **`harness.Harness`** interface — `BuildCommandArgs()`, `BuildCommandEnvVars()`,
  `PrepareForLaunch()`, `HandleEvent()`, `EnsureConfigDir()`
- **`harness/claude/`** — Claude Code harness implementation
- **`harness/codex/`** — Codex harness implementation
- **`harness/generic/`** — Generic command harness

The old `adapter/`, `agent_type.go`, and `AgentWrapper` have been removed.
The `Agent` struct now owns a single `Harness` directly.

Other internal refactors:
- Legacy OTEL layer removed; metrics naming unified
- `Note*` signaling methods renamed to `Signal*`
- Event flow and shared collectors unified across harnesses
- Claude hooks, OTEL, and session logs unified in a single event handler
- `OutputCollector` extracted to `shared/outputcollector/`
- `EventStore` extracted to `shared/eventstore/`
- `OTELServer` extracted to `shared/otelserver/`
- Agent monitor (`AgentMonitor`) extracted for event consumption, state
  tracking, and metrics

### Terminal and UI Improvements

- **Scroll mode**: Added PageUp/PageDown/Home/End support for navigating
  scroll history. PageUp and Home now enter scroll mode from normal mode.
- **Scroll mode exit**: Auto-exit scroll mode only triggers at bottom for
  mouse wheel and End key (not for other navigation).
- **Screen flashing fix**: Replaced erase-line with erase-to-end-of-line to
  prevent full-screen flashes during redraws.
- **Scroll regions**: Fixed scrollback for apps using scroll regions (e.g.
  Codex). `PlainHistory` removed; `ScrollHistory` used when app uses scroll
  regions.
- **Alternate scroll mode**: Added support for CSI?1007h (mouse-to-arrow
  conversion in alternate screen).
- **CSI passthrough**: Unhandled CSI sequences (PageUp, PageDown, Home, End)
  are now passed through to the child process.
- **White background fix**: Fixed background color bleed on hint text in
  live view.

### Codex Support

- Default Codex model updated to `gpt-5.3-codex`.
- Fixed Codex OTEL ingestion, event/state mapping, and debug tooling.
- Fixed OTEL trace exporter format for Codex.
- Codex config directory support with per-profile paths.
- Removed `--no-alt-screen` from Codex args (no longer needed).

### Bug Fixes

- Fixed message delivery to use resolved `H2_DIR` instead of hardcoded `~/.h2`.
- Fixed activity log and OTEL logs to use resolved `H2_DIR`.
- Prevented duplicate bridge processes with socket probe on startup.
- Fixed terminal color propagation for Codex rendering.
- Cache `TERM` and `COLORTERM` in `terminal-colors.json` for background
  launches.
- Fixed `dry-run` formatting: full args display, proper JSON encoding of
  instructions, copy-pasteable command output, correct column alignment.

### Build and Tooling

- Added Makefile with `build`, `test`, `check` (vet + staticcheck), and
  `test-coverage` targets.
- Fixed `vet` and `staticcheck` findings.

## v0.1.0

Initial release.
