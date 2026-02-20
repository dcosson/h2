# Phase 2.5: Merge AgentType + AgentAdapter into Harness

## Problem

Adding a new agent integration requires implementing two separate interfaces in two different packages:

1. **AgentType** (`agent/agent_type.go`) — static config: Name, Command, BuildCommandArgs, BuildCommandEnvVars, EnsureConfigDir, NewAdapter
2. **AgentAdapter** (`agent/adapter/adapter.go`) — runtime: Name, PrepareForLaunch, Start, HandleHookEvent, Stop

These are 1:1 coupled (`NewAdapter()` on AgentType creates the AgentAdapter). Both have `Name()`. The adapter implementations live in `agent/adapter/claude/` and `agent/adapter/codex/` while the type definitions all live together in `agent/agent_type.go`. To add Codex you touch 3 packages across 2 directory trees.

Additionally, "AgentType" is confusing — the Claude LLM can be used through multiple harnesses (Claude Code CLI, future API-direct). This is really "agent harness": the CLI tool that wraps the LLM.

## Proposal

Merge both interfaces into a single `Harness` interface. Each harness gets its own package with all implementation in one place.

### The Harness interface

```go
// package: agent/harness

type Harness interface {
    // Identity
    Name() string            // e.g. "claude_code", "codex", "generic"
    Command() string         // executable name, e.g. "claude", "codex"
    DisplayCommand() string  // for display, e.g. "claude", "codex"

    // Config (called before launch)
    BuildCommandArgs(cfg CommandArgsConfig) []string
    BuildCommandEnvVars(h2Dir, roleName string) map[string]string
    EnsureConfigDir(h2Dir, roleName string) error

    // Launch (called once, before child process starts)
    PrepareForLaunch(agentName, sessionID string) (LaunchConfig, error)

    // Runtime (called after child process starts)
    Start(ctx context.Context, events chan<- monitor.AgentEvent) error
    HandleHookEvent(eventName string, payload json.RawMessage) bool
    Stop()
}
```

**GenericHarness** implements Start/HandleHookEvent/Stop as no-ops. The Agent struct's existing output-collector-to-monitor bridge handles generic agent state detection, unchanged.

### Package layout

```
agent/harness/
    harness.go              — Harness interface, LaunchConfig, CommandArgsConfig,
                              InputSender, PTYInputSender, ResolveHarness()
    claude/
        harness.go          — ClaudeCodeHarness (merges ClaudeCodeType + ClaudeCodeAdapter)
        hook_handler.go     — (moved from adapter/claude/)
        hook_handler_test.go
        otel_parser.go      — (moved from adapter/claude/)
        otel_parser_test.go
        sessionlog.go       — (moved from adapter/claude/)
        sessionlog_test.go
        harness_test.go     — (merges adapter/claude/adapter_test.go + agent_type_test.go Claude tests)
    codex/
        harness.go          — CodexHarness (merges CodexType + CodexAdapter)
        launch.go           — BuildLaunchConfig (moved from adapter/codex/)
        launch_test.go
        otel_parser.go      — (moved from adapter/codex/)
        otel_parser_test.go
        harness_test.go
        integration_test.go
    generic/
        harness.go          — GenericHarness (no-ops for runtime methods)
        harness_test.go
```

### What gets deleted

```
agent/agent_type.go          — replaced by harness/harness.go + per-harness packages
agent/agent_type_test.go     — split into per-harness test files
agent/adapter/adapter.go     — interface merged into harness.go, types moved
agent/adapter/adapter_test.go
agent/adapter/claude/         — moved to harness/claude/
agent/adapter/codex/          — moved to harness/codex/
```

### Changes to Agent struct (`agent/agent.go`)

The Agent struct currently holds both `agentType AgentType` and `adapter adapter.AgentAdapter`. After the merge:

```go
type Agent struct {
    harness      harness.Harness         // single reference, replaces agentType + adapter
    agentMonitor *monitor.AgentMonitor
    // ... rest unchanged
}
```

Key changes in Agent:
- `New(agentType AgentType)` → `New(h harness.Harness)`
- `a.agentType.NewAdapter(log)` → gone; the harness IS the adapter
- `PrepareForLaunch`: calls `a.harness.PrepareForLaunch()` directly (no nil adapter check needed — GenericHarness returns empty LaunchConfig)
- `Start`: calls `a.harness.Start(ctx, eventCh)` for all harnesses (GenericHarness.Start is a no-op; output bridge still runs separately)
- `HandleHookEvent`: calls `a.harness.HandleHookEvent()` directly
- `Stop`: calls `a.harness.Stop()` directly
- `AgentType()` → `Harness()` returning `harness.Harness`
- `SetOtelLogFiles` gating: check `a.harness.Name()` instead of `a.agentType.Name()`
- Hook collector gating: check `a.harness.Name() == "claude_code"` (note: renamed from "claude-code")

### Name changes

| Old | New |
|-----|-----|
| `AgentType` (interface) | `Harness` |
| `ClaudeCodeType` | `ClaudeCodeHarness` |
| `CodexType` | `CodexHarness` |
| `GenericType` | `GenericHarness` |
| `NewClaudeCodeType()` | `claude.New()` (in own package now) |
| `NewCodexType()` | `codex.New()` (in own package now) |
| `NewGenericType(cmd)` | `generic.New(cmd)` |
| `ResolveAgentType(cmd)` | `harness.Resolve(cmd)` |
| `AgentAdapter` (interface) | gone, merged into `Harness` |
| `agent_type` (YAML) | `agent_harness` |
| `"claude"` (Name value) | `"claude_code"` |
| `"claude-code"` (adapter Name) | `"claude_code"` (unified) |
| `GetAgentType()` (on Role) | `GetAgentHarness()` |
| `--agent-type` (CLI flag) | `--agent-harness` |

### YAML backward compatibility

The `agent_type` field in role YAML files must continue to work. In `config/role.go`:

```go
type Role struct {
    AgentHarness    string `yaml:"agent_harness,omitempty"`
    AgentTypeLegacy string `yaml:"agent_type,omitempty"` // deprecated, use agent_harness
}

func (r *Role) GetAgentHarness() string {
    if r.AgentHarness != "" {
        return r.AgentHarness
    }
    if r.AgentTypeLegacy != "" {
        return r.AgentTypeLegacy
    }
    return "claude"
}
```

Also accept `"claude"` as an alias for `"claude_code"` in `harness.Resolve()`.

### Callers to update

| File | Change |
|------|--------|
| `session.go` | `agent.ResolveAgentType` → `harness.Resolve`, `.AgentType()` → `.Harness()` |
| `daemon.go` | `.AgentType()` → `.Harness()` |
| `cmd/agent_setup.go` | `role.GetAgentType` → `GetAgentHarness`, `agent.ResolveAgentType` → `harness.Resolve` |
| `cmd/dry_run.go` | Same as agent_setup |
| `cmd/run.go` | `--agent-type` flag → `--agent-harness` |
| `cmd/role.go` | Template strings, display output |
| `config/role.go` | Struct field, yaml tag, method |
| `heartbeat_test.go` | Constructor call |
| `otel_test.go` | Constructor call |
| E2E tests (6 files) | `agent_type:` → `agent_harness:` in YAML strings |
| Docs (10 files) | References to AgentType |

### What stays the same

- `agent/shared/otelserver/` — unchanged, shared by claude and codex harnesses
- `agent/shared/eventstore/` — unchanged
- `agent/shared/outputcollector/` — unchanged
- `agent/monitor/` — unchanged
- `agent/collector/` — unchanged (hook collector)
- `agent/agent.go` — updated but same role (owns harness + monitor + output collector)
- `LaunchConfig`, `InputSender`, `PTYInputSender` — moved from `adapter/` to `harness/` but unchanged

### `claude_config_dir` in role YAML

With the harness owning its config dir via `EnsureConfigDir` and `BuildCommandEnvVars`, the top-level `claude_config_dir` role field becomes harness-specific config. Two options:

**A) Leave it as-is.** It's only used by `auth.go` and the Claude harness reads it via the Role. Simple, no breakage.

**B) Nest under harness config.** Add a `claude:` section to role YAML for Claude-specific overrides. Overkill for one field right now.

Recommend **A** for now — leave `claude_config_dir` as a top-level role field. Document that it only applies to the claude_code harness.

## Task breakdown

1. **Create `agent/harness/` package with Harness interface** — Move interface, LaunchConfig, CommandArgsConfig, InputSender, PTYInputSender from current locations. Add `Resolve()` function.

2. **Create `agent/harness/claude/` package** — Merge ClaudeCodeType + ClaudeCodeAdapter into ClaudeCodeHarness. Move hook_handler, otel_parser, sessionlog from adapter/claude/.

3. **Create `agent/harness/codex/` package** — Merge CodexType + CodexAdapter into CodexHarness. Move launch, otel_parser from adapter/codex/.

4. **Create `agent/harness/generic/` package** — GenericHarness with no-op runtime methods.

5. **Update Agent struct** — Replace `agentType + adapter` with single `harness` field. Remove `NewAdapter()` call. Update all method dispatching.

6. **Update callers** — session.go, daemon.go, agent_setup.go, dry_run.go, run.go, role.go (cmd + config).

7. **Rename YAML field and CLI flag** — `agent_type` → `agent_harness` with backward compat. `--agent-type` → `--agent-harness`.

8. **Delete old packages** — Remove agent/agent_type.go, agent/adapter/.

9. **Update e2e tests and docs** — Mechanical rename in test YAML strings and documentation.

Tasks 1-4 can be done in parallel (new packages, no callers yet). Task 5 depends on 1-4. Tasks 6-7 depend on 5. Task 8 depends on 6. Task 9 is independent cleanup.
