# Design Plan: Codex Agent Type for h2

## Overview

Add OpenAI Codex CLI as a first-class agent type in h2, alongside the existing `ClaudeCodeType` and `GenericType`. Codex has a different architecture from Claude Code — no OTEL, no hooks, but it does offer a structured JSONL output mode via `codex exec --json` that we can parse for rich state tracking.

## 1. CodexType Implementation

### New file: `internal/session/agent/codex_type.go`

Implement the `AgentType` interface:

```go
type CodexType struct{}

func NewCodexType() *CodexType { return &CodexType{} }

func (t *CodexType) Name() string           { return "codex" }
func (t *CodexType) Command() string         { return "codex" }
func (t *CodexType) DisplayCommand() string   { return "codex" }
func (t *CodexType) OtelParser() OtelParser   { return nil }  // No OTEL support
func (t *CodexType) Collectors() CollectorSet { return CollectorSet{Otel: false, Hooks: false} }

func (t *CodexType) PrependArgs(sessionID string) []string {
    // No session-id concept in Codex. However, we may want to add
    // flags like --full-auto or --model here based on role config.
    return nil
}

func (t *CodexType) ChildEnv(cp *CollectorPorts) map[string]string {
    // No collector env vars needed (no OTEL).
    return nil
}
```

### Update `ResolveAgentType` in `agent_type.go`

```go
func ResolveAgentType(command string) AgentType {
    switch filepath.Base(command) {
    case "claude":
        return NewClaudeCodeType()
    case "codex":
        return NewCodexType()
    default:
        return NewGenericType(command)
    }
}
```

### Key decisions

- **No OTEL, no hooks**: Codex CLI has neither, so `Collectors()` returns an empty set. The `OutputCollector` (always created by `Agent.StartCollectors()`) becomes the primary state collector — same as `GenericType`.
- **PrependArgs**: Codex doesn't use `--session-id`. If we later want to pass `--full-auto` or `--model` based on role config, we can add those here.
- **ChildEnv**: No special env vars needed.

## 2. Collector Strategy

### Phase 1: OutputCollector only (MVP)

For the initial implementation, Codex agents use `OutputCollector` as their sole/primary collector. This gives us:
- Active/Idle state tracking based on PTY output
- Works immediately with zero Codex-specific code

This is the same behavior as `GenericType` agents, which is functional but provides only coarse-grained state (active vs idle based on a 2-second PTY silence threshold).

### Phase 2: JSONL Stream Collector (future enhancement)

Codex's `codex exec --json` mode emits structured JSONL events to stdout:
- `thread.started`, `turn.started`, `turn.completed`, `turn.failed`
- `item.started`, `item.updated`, `item.completed`
- Items include: agent messages, reasoning, command executions, file changes

A future `CodexJsonlCollector` could parse these events from the PTY output stream (they'd be mixed in with the terminal output) or by running Codex in `exec --json` mode where all output is JSONL.

**However**, there's a fundamental tension: h2 manages agents in a PTY for interactive attach/detach. The `--json` flag on `codex exec` is designed for non-interactive use. If we want both interactive PTY support AND structured events, we'd need one of:

1. **Parse JSONL from PTY output**: If Codex in interactive mode doesn't emit JSONL, this won't work.
2. **Sidecar process**: Run `codex exec --json` as a separate process alongside the PTY agent, but this doubles the work and diverges state.
3. **Post-hoc log parsing**: If Codex writes structured logs to a file, we could tail/parse that.

**Recommendation**: Start with OutputCollector. Investigate whether Codex interactive mode produces any parseable structured output, or if there are log files we can tail. If so, build a `CodexJsonlCollector` as a follow-up.

## 3. Command/Session Integration

### `internal/cmd/run.go`

No changes needed to `run.go` itself. The existing `--agent-type codex` flag already works because it passes the command string through to `ResolveAgentType()`. Similarly, roles with `agent_type: codex` will resolve correctly.

However, the `childArgs()` method in `session.go` currently appends Claude-specific flags (e.g., `--append-system-prompt`, `--system-prompt`, `--model`, `--permission-mode`, `--allowedTools`, `--disallowedTools`). These are Claude CLI flags and should NOT be passed to Codex.

### Changes needed in `internal/session/session.go`

The `childArgs()` method needs to be agent-type-aware. Options:

**Option A (recommended)**: Move role-to-CLI-arg mapping into the `AgentType` interface.

Add a new method to `AgentType`:

```go
// RoleArgs returns CLI args derived from role configuration.
// Each agent type maps role fields (model, instructions, permissions, etc.)
// to its own CLI flag format.
RoleArgs(cfg RoleConfig) []string
```

Where `RoleConfig` is a struct with the role fields:

```go
type RoleConfig struct {
    Model          string
    Instructions   string
    SystemPrompt   string
    PermissionMode string
    AllowedTools   []string
    DisallowedTools []string
}
```

- `ClaudeCodeType.RoleArgs()` would return `["--model", model, "--append-system-prompt", instructions, ...]`
- `CodexType.RoleArgs()` would return `["--model", model]` (Codex supports `--model`), and handle instructions differently (perhaps via stdin prompt prefix or `--cd`/other flags).

**Option B**: Keep `childArgs()` in session, but gate Claude-specific flags on `agentType.Name() == "claude"`. Simpler but less extensible.

**Recommendation**: Option A. It's the right abstraction — each agent type knows its own CLI interface.

### `internal/cmd/ls.go`

No changes needed. `ls.go` queries agent status via the Unix socket protocol, which is agent-type-agnostic. The `AgentInfo` struct already carries all the relevant fields, and fields that don't apply to Codex (like hook data, OTEL metrics) will simply be zero-valued/omitted.

### `internal/cmd/stop.go` and `internal/cmd/attach.go`

No changes needed. These operate on the Unix socket and PTY layer, which are agent-type-independent.

### `internal/cmd/peek.go`

Significant changes needed. `peek.go` is currently hardcoded to parse Claude Code's session JSONL format (`sessionRecord` with type "assistant", `contentBlock` with tool_use, etc.). This format is specific to Claude Code.

Options:
1. **Make peek agent-type-aware**: Query the agent's type via status, then dispatch to the appropriate log parser.
2. **Skip peek for Codex initially**: Return "peek not supported for codex agents" until we understand Codex's log format.
3. **Generic PTY log**: h2 could record raw PTY output to a log file and `peek` could show that for non-Claude agents.

**Recommendation**: Option 2 for MVP, with option 1 as a follow-up. The `peek` command should check agent type from session metadata and return a clear error for unsupported types. If/when we add the JSONL stream collector for Codex, we can also add a Codex-specific peek formatter.

Changes to `peek.go`:
- When resolving the log path from an agent name, check `meta.Command` (or add an `AgentType` field to `SessionMetadata`).
- If the agent type is "codex", return "peek is not yet supported for Codex agents".

## 4. Role Configuration

### Role YAML

The existing `agent_type` field in `Role` (default: "claude") already supports arbitrary strings. A Codex role would look like:

```yaml
name: codex-dev
agent_type: codex
model: o3       # Codex --model flag
instructions: |
  You are a coding agent. Write clean, tested code.
working_dir: ~/code/myproject
```

### Agent-type-specific role fields

Some role fields are Claude-specific:
- `claude_config_dir` — only relevant for Claude Code
- `permission_mode` — Claude-specific concept
- `permissions.allow/deny` — Claude-specific
- `hooks` — Claude Code hooks
- `settings` — Claude Code settings.json

These should be ignored (with a warning) when `agent_type` is not "claude". The `Role.Validate()` method should warn (not error) if Claude-specific fields are set with a non-Claude agent type.

### Codex-specific role fields

Codex has its own configuration that doesn't map to existing Claude fields:
- `--full-auto` vs `--suggest` vs `--ask` (approval modes)
- `--cd` (working directory, already handled by `working_dir`)
- `--ephemeral` (don't save session)

We have a few options:
1. **Generic `args` field**: Add `extra_args: ["--full-auto"]` to Role. Works for any agent type.
2. **Codex-specific section**: Add a `codex:` block to Role for Codex-specific config. More structured but tightly coupled.
3. **Map existing role concepts**: Map `permission_mode` to Codex approval modes (`dontAsk` → `--full-auto`, `default` → `--suggest`).

**Recommendation**: Option 1 (generic `extra_args`) for MVP. It's the most flexible and doesn't require schema changes per agent type. Option 3 is a nice follow-up for a more polished experience.

## 5. h2 Messaging

### How Claude Code receives messages

Currently, h2 delivers messages by writing text to the agent's PTY stdin. For Claude Code, this works because Claude's prompt accepts text input, and messages are formatted as `[h2 message from: sender] body`.

### How Codex receives messages

Codex interactive mode also accepts text input in its prompt. The same PTY-based delivery mechanism should work:
- The delivery loop writes `[h2 message from: sender] Read /path/to/msg.md\r` to the PTY
- Codex receives it as user input

However, there are two concerns:

1. **Idle detection for delivery**: The delivery loop in `message/delivery.go` waits for the agent to be idle before delivering normal-priority messages. With only `OutputCollector`, idle means "no PTY output for 2 seconds". This is coarser than Claude's hook-based idle detection but should work acceptably — Codex's prompt waits for input when idle, producing no output.

2. **`codex exec` mode**: If Codex is running in `exec` mode (non-interactive, processes a single prompt and exits), there's no ongoing prompt to deliver messages to. This is inherently incompatible with the message delivery model. For `exec` mode, h2 would need to either:
   - Queue messages and deliver them on relaunch
   - Not support messaging for exec-mode agents

**Recommendation**: Focus on Codex's interactive mode for messaging. The existing PTY-based delivery works without changes. Document that `codex exec` is a one-shot mode where messaging is limited to priority/interrupt messages (which send Ctrl+C first).

### How Codex sends messages

For sending messages (`h2 send`), Codex agents work the same as Claude Code — the `h2 send` CLI is available as a shell command that the agent can execute via its tool use. The `h2` binary is in the PATH, and the `H2_ACTOR` env var is set so the agent knows its own name.

The system prompt/instructions should tell Codex agents about the `h2 send` command, just as they do for Claude Code agents.

## 6. Peek Support

### Current state

`peek.go` reads Claude Code's session JSONL log file, which contains structured records of assistant messages, tool calls, and text output. The path is resolved via `config.ClaudeCodeSessionLogPath()`, which uses the Claude config dir and session ID to find the log file.

### Codex differences

Codex doesn't produce the same JSONL format. Its `--json` output is a different schema entirely (thread/turn/item events). And in interactive mode, it may not produce structured logs at all.

### Plan

1. **Add `AgentType` field to `SessionMetadata`** (in `internal/config/session.go`). Set it when writing metadata in `daemon.go`.

2. **`peek.go` dispatch**:
   ```go
   meta, err := config.ReadSessionMetadata(sessionDir)
   // ...
   switch meta.AgentType {
   case "claude", "":  // "" for backward compat
       return formatClaudeSessionLog(meta.ClaudeCodeSessionLogPath, numLines, messageChars)
   case "codex":
       return nil, fmt.Errorf("peek is not yet supported for Codex agents")
   default:
       return nil, fmt.Errorf("peek not supported for agent type %q", meta.AgentType)
   }
   ```

3. **Future**: If we add the JSONL stream collector that captures Codex events to a log file, add a `formatCodexSessionLog()` function that parses the Codex JSONL schema.

## 7. Metrics

### What Claude Code provides via OTEL
- Token counts (input, output, cache)
- Cost (USD)
- Per-model breakdowns
- API request counts
- Tool result events with tool names
- Lines added/removed

### What Codex can provide

Without OTEL or hooks, Codex agents initially have **no metrics** beyond what the OutputCollector provides (active/idle state).

### Future metrics from JSONL stream

If we build the JSONL stream collector (Phase 2 from section 2), Codex's structured output includes:
- `turn.completed` events with usage stats (if Codex reports them)
- Command execution events (what tools were used)
- File change events

### What to show in `h2 list`

For Codex agents, `h2 list` will show:
- Agent name, command, state (active/idle/exited), uptime
- No token/cost metrics (these fields are omitted when zero)
- No hook data (last tool, permission blocks)

This is the same as `GenericType` agents today, which is acceptable for MVP.

## 8. Testing Strategy

### Unit tests

1. **`agent_type_test.go`**: Add tests for `CodexType`:
   - `Name()` returns "codex"
   - `Command()` returns "codex"
   - `Collectors()` returns empty set
   - `OtelParser()` returns nil
   - `PrependArgs()` returns nil
   - `ChildEnv()` returns nil
   - `ResolveAgentType("codex")` returns `*CodexType`

2. **`role_test.go`**: Add tests for Codex role loading:
   - Role with `agent_type: codex` loads correctly
   - `GetAgentType()` returns "codex"

3. **`session_test.go`**: Test that a session created with command "codex" uses `CodexType` and OutputCollector as primary.

### Integration tests

1. **Mock Codex binary**: Create a simple shell script or Go binary that mimics Codex's interactive behavior (prints a prompt, accepts input, echoes responses). Use it to test:
   - Session creation and PTY setup
   - Message delivery via PTY
   - Idle detection via OutputCollector
   - Agent info reporting via status socket

2. **Role-based launch**: Test `h2 run --role codex-dev` with a Codex role file.

### What NOT to test in MVP

- JSONL stream parsing (Phase 2)
- Codex-specific peek formatting (Phase 2)
- Codex-specific metrics (Phase 2)

## 9. Implementation Order

1. **`CodexType` struct** + `ResolveAgentType` update + unit tests
2. **`RoleArgs()` interface method** + implementations for Claude and Codex + update `childArgs()`
3. **`SessionMetadata.AgentType`** field + write it in `daemon.go`
4. **`peek.go`** agent-type dispatch
5. **Role validation warnings** for Claude-specific fields on non-Claude agents
6. **Documentation**: Update `docs/agent-roles.md` with Codex role examples

## 10. Open Questions

1. **Does Codex interactive mode produce structured logs?** If so, where? This determines whether a JSONL collector is feasible without the `--json` flag.

2. **How does Codex handle stdin in interactive mode?** We need to confirm that the PTY-based message delivery pattern works — that pasting text into Codex's prompt is treated as user input.

3. **Should we support `codex exec` as a different mode?** `exec` is one-shot and may be useful for pod-based orchestration where you want to fire-and-forget tasks. But it's fundamentally different from the persistent agent model.

4. **How should `--full-auto` map to role config?** Is it a Codex-specific field, or should we extend the generic `permission_mode` concept?

5. **Auth**: Codex uses `codex login` for authentication. Should h2 provide auth checking similar to `IsClaudeConfigAuthenticated()`? This would go in the `CodexType` or as a standalone function.
