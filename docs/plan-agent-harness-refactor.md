# Phase 2.5: Merge AgentType + AgentAdapter into Harness

## Problem

Adding a new agent integration requires implementing two separate interfaces in two different packages:

1. **AgentType** (`agent/agent_type.go`) — static config: Name, Command, BuildCommandArgs, BuildCommandEnvVars, EnsureConfigDir, NewAdapter
2. **AgentAdapter** (`agent/adapter/adapter.go`) — runtime: Name, PrepareForLaunch, Start, HandleHookEvent, Stop

These are 1:1 coupled (`NewAdapter()` on AgentType creates the AgentAdapter). Both have `Name()`. The adapter implementations live in `agent/adapter/claude/` and `agent/adapter/codex/` while the type definitions all live together in `agent/agent_type.go`. To add Codex you touch 3 packages across 2 directory trees.

Additionally, "AgentType" is confusing — the Claude LLM can be used through multiple harnesses (Claude Code CLI, future API-direct). This is really "agent harness": the CLI tool that wraps the LLM.

Currently, Agent branches on `adapter != nil` in 6 places to handle GenericType (which has no adapter). GenericType should be a proper Harness implementation — not a nil-adapter exception path.

Harness-specific config (`model`, `claude_config_dir`) lives as flat top-level fields on the Role struct, making it unclear which fields apply to which harness. These should be nested under an `agent_harness` section.

## Proposal

Merge both interfaces into a single `Harness` interface. Each harness gets its own package with all implementation in one place. GenericHarness becomes a real implementation (no-op OTEL methods, internal output-collector for state detection). Nest harness config in YAML.

### The Harness interface

```go
// package: agent/harness

type Harness interface {
    // Identity
    Name() string            // "claude_code", "codex", or "generic"
    Command() string         // executable name: "claude", "codex", or custom
    DisplayCommand() string  // for display

    // Config (called before launch)
    BuildCommandArgs(cfg CommandArgsConfig) []string
    BuildCommandEnvVars(h2Dir string) map[string]string
    EnsureConfigDir(h2Dir string) error

    // Launch (called once, before child process starts)
    PrepareForLaunch(agentName, sessionID string) (LaunchConfig, error)

    // Runtime (called after child process starts)
    Start(ctx context.Context, events chan<- monitor.AgentEvent) error
    HandleHookEvent(eventName string, payload json.RawMessage) bool
    HandleOutput()  // signal that child process produced output
    Stop()
}
```

Note: `BuildCommandEnvVars` and `EnsureConfigDir` signatures simplify from `(h2Dir, roleName string)` to `(h2Dir string)`. The harness receives its config at construction time (via `HarnessConfig`), so it no longer needs to re-load the role to find its config dir.

**HandleOutput()** is called by Agent whenever the PTY produces output. Each harness decides what to do:
- **ClaudeCodeHarness / CodexHarness**: No-op. State detection comes from OTEL/hooks.
- **GenericHarness**: Feeds an internal `outputcollector.Collector`. The collector detects idle/active state and GenericHarness.Start() bridges those state changes to the events channel.

This means the output collector moves entirely inside GenericHarness. Agent no longer owns an output collector or runs `bridgeOutputToMonitor`.

**Start() semantics**: All harnesses block until ctx is cancelled. Claude blocks reading from OTEL server, Codex blocks reading from OTEL traces, Generic blocks reading from its output collector's StateCh. The caller always wraps in `go a.harness.Start(...)`.

### HarnessConfig (passed to constructors)

```go
// package: agent/harness

// HarnessConfig holds harness-specific configuration extracted from the Role.
type HarnessConfig struct {
    HarnessType string // "claude_code", "codex", or "generic"
    Model       string // model name (shared by claude/codex; empty for generic)
    ConfigDir   string // harness-specific config dir (resolved by Role)
    Command     string // executable command (only used by generic)
}
```

Each harness constructor receives this:
- `claude.New(cfg harness.HarnessConfig, log *activitylog.Logger) *ClaudeCodeHarness`
- `codex.New(cfg harness.HarnessConfig, log *activitylog.Logger) *CodexHarness`
- `generic.New(cfg harness.HarnessConfig) *GenericHarness`

The harness stores `cfg.ConfigDir` and uses it in `BuildCommandEnvVars` and `EnsureConfigDir` instead of re-loading the role.

### GenericHarness design

GenericHarness is a proper Harness implementation — no nil checks needed in Agent:

```go
type GenericHarness struct {
    command   string
    collector *outputcollector.Collector  // created in Start()
}

func (g *GenericHarness) Name() string            { return "generic" }
func (g *GenericHarness) Command() string          { return g.command }
func (g *GenericHarness) DisplayCommand() string   { return g.command }
func (g *GenericHarness) BuildCommandArgs(cfg CommandArgsConfig) []string { return nil }
func (g *GenericHarness) BuildCommandEnvVars(h2Dir string) map[string]string { return nil }
func (g *GenericHarness) EnsureConfigDir(h2Dir string) error { return nil }
func (g *GenericHarness) PrepareForLaunch(agentName, sessionID string) (LaunchConfig, error) {
    return LaunchConfig{}, nil
}
func (g *GenericHarness) HandleHookEvent(eventName string, payload json.RawMessage) bool {
    return false
}

// Start creates the output collector and blocks, bridging state changes to
// events until ctx is cancelled. Same blocking semantics as Claude/Codex.
func (g *GenericHarness) Start(ctx context.Context, events chan<- monitor.AgentEvent) error {
    g.collector = outputcollector.New(monitor.IdleThreshold)
    for {
        select {
        case su := <-g.collector.StateCh():
            select {
            case events <- monitor.AgentEvent{
                Type:      monitor.EventStateChange,
                Timestamp: time.Now(),
                Data:      monitor.StateChangeData{State: su.State, SubState: su.SubState},
            }:
            case <-ctx.Done():
                return ctx.Err()
            }
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}

// HandleOutput feeds the internal output collector.
func (g *GenericHarness) HandleOutput() {
    if g.collector != nil {
        g.collector.NoteOutput()
    }
}

// Stop cleans up the output collector.
func (g *GenericHarness) Stop() {
    if g.collector != nil {
        g.collector.Stop()
    }
}

```

### Package layout

```
agent/harness/
    harness.go              — Harness interface, HarnessConfig, LaunchConfig,
                              CommandArgsConfig, InputSender, PTYInputSender,
                              Resolve()
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
        harness.go          — GenericHarness (output-collector-based state detection)
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

From `agent/agent.go`, delete:
- `adapter` field, `outputCollector` field
- `OtelMetrics` struct, `metrics` field, `otelLogsFile`/`otelMetricsFile` fields
- `bridgeOutputToMonitor()` method (moved into GenericHarness)
- `SetOtelLogFiles()` method (all harnesses handle OTEL internally)
- All 6 `if a.adapter != nil` branches

### Changes to Agent struct (`agent/agent.go`)

The Agent struct currently holds both `agentType AgentType` and `adapter adapter.AgentAdapter`. After the merge:

```go
type Agent struct {
    harness      harness.Harness         // single reference, replaces agentType + adapter
    agentMonitor *monitor.AgentMonitor
    cancel       context.CancelFunc

    // Hook collector: kept for backward compat Snapshot() data.
    hooksCollector *collector.HookCollector

    // Activity logger
    activityLog *activitylog.Logger

    stopCh chan struct{}
}
```

Key changes in Agent:
- `New(agentType AgentType)` → `New(h harness.Harness)`
- `a.agentType.NewAdapter(log)` → gone; the harness IS the adapter
- All method dispatch is direct — no nil checks, no branching:

```go
func (a *Agent) PrepareForLaunch(agentName, sessionID string) (harness.LaunchConfig, error) {
    if a.harness.Name() == "claude_code" {
        a.hooksCollector = collector.NewHookCollector(a.ActivityLog())
    }
    return a.harness.PrepareForLaunch(agentName, sessionID)
}

func (a *Agent) Start(ctx context.Context) {
    ctx, a.cancel = context.WithCancel(ctx)
    go a.harness.Start(ctx, a.agentMonitor.Events())
    go a.agentMonitor.Run(ctx)
}

func (a *Agent) HandleHookEvent(eventName string, payload json.RawMessage) bool {
    if a.hooksCollector != nil {
        a.hooksCollector.ProcessEvent(eventName, payload)
    }
    return a.harness.HandleHookEvent(eventName, payload)
}

func (a *Agent) HandleOutput() {
    a.harness.HandleOutput()
}

func (a *Agent) Stop() {
    // ...
    if a.cancel != nil { a.cancel() }
    a.harness.Stop()
    if a.hooksCollector != nil { a.hooksCollector.Stop() }
}

func (a *Agent) Metrics() OtelMetricsSnapshot {
    // Always read from monitor — all harnesses emit events via Start().
    // GenericHarness emits state events from output collector.
    ms := a.agentMonitor.Metrics()
    // ... bridge to OtelMetricsSnapshot
}
```

- `AgentType()` → `Harness()` returning `harness.Harness`

### Name changes

| Old | New |
|-----|-----|
| `AgentType` (interface) | `Harness` |
| `ClaudeCodeType` | `ClaudeCodeHarness` |
| `CodexType` | `CodexHarness` |
| `GenericType` | `GenericHarness` (now a proper impl, not a nil-adapter stub) |
| `NewClaudeCodeType()` | `claude.New(cfg, log)` |
| `NewCodexType()` | `codex.New(cfg, log)` |
| `NewGenericType(cmd)` | `generic.New(cfg)` |
| `ResolveAgentType(cmd)` | `harness.Resolve(cfg, log)` (takes HarnessConfig) |
| `AgentAdapter` (interface) | gone, merged into `Harness` |
| `agent_type` (YAML) | `agent_harness.harness_type` |
| `model` (YAML top-level) | `agent_harness.model` |
| `claude_config_dir` (YAML top-level) | `agent_harness.claude_config_dir` |
| `"claude"` (Name value) | `"claude_code"` |
| `"claude-code"` (adapter Name) | `"claude_code"` (unified) |
| `GetAgentType()` (on Role) | `GetHarnessConfig()` returning `HarnessConfig` |

### YAML config restructure

Harness-specific fields move into a nested `agent_harness` section:

```yaml
# New format (Claude Code)
name: my-agent
agent_harness:
  harness_type: claude_code     # "claude_code", "codex", or "generic"
  model: opus
  claude_config_dir: "{{ .H2Dir }}/claude-config/default"
instructions: |
  ...
```

```yaml
# Codex example
name: my-codex-agent
agent_harness:
  harness_type: codex
  model: o3-mini
  codex_config_dir: /path/to/codex/config
instructions: |
  ...
```

```yaml
# Generic example (custom command, no model or config dir)
name: my-script
agent_harness:
  harness_type: generic
  command: /usr/local/bin/my-agent
instructions: |
  ...
```

### Config struct changes (`config/role.go`)

```go
// AgentHarnessConfig holds harness-specific configuration.
type AgentHarnessConfig struct {
    HarnessType     string `yaml:"harness_type,omitempty"`
    Model           string `yaml:"model,omitempty"`
    Command         string `yaml:"command,omitempty"`           // generic only
    ClaudeConfigDir string `yaml:"claude_config_dir,omitempty"` // claude_code only
    CodexConfigDir  string `yaml:"codex_config_dir,omitempty"`  // codex only
}

type Role struct {
    Name         string              `yaml:"name"`
    Description  string              `yaml:"description,omitempty"`
    AgentHarness *AgentHarnessConfig `yaml:"agent_harness,omitempty"`

    // Deprecated top-level fields (backward compat)
    AgentTypeLegacy       string `yaml:"agent_type,omitempty"`
    ModelLegacy           string `yaml:"model,omitempty"`
    ClaudeConfigDirLegacy string `yaml:"claude_config_dir,omitempty"`

    // ... rest unchanged (WorkingDir, Worktree, SystemPrompt, Instructions,
    //     PermissionMode, Permissions, Heartbeat, Hooks, Settings, Variables)
}
```

Accessor methods with backward compat fallback:

```go
// GetHarnessType returns the harness type, defaulting to "claude_code".
func (r *Role) GetHarnessType() string {
    if r.AgentHarness != nil && r.AgentHarness.HarnessType != "" {
        return r.AgentHarness.HarnessType
    }
    if r.AgentTypeLegacy != "" {
        // Map old names: "claude" → "claude_code"
        if r.AgentTypeLegacy == "claude" {
            return "claude_code"
        }
        return r.AgentTypeLegacy
    }
    return "claude_code"
}

// GetModel returns the model, checking nested config first.
func (r *Role) GetModel() string {
    if r.AgentHarness != nil && r.AgentHarness.Model != "" {
        return r.AgentHarness.Model
    }
    return r.ModelLegacy
}

// GetClaudeConfigDir returns the Claude config dir, checking nested config first.
// Falls back to default shared config dir if not specified.
func (r *Role) GetClaudeConfigDir() string {
    if r.AgentHarness != nil && r.AgentHarness.ClaudeConfigDir != "" {
        return expandTilde(r.AgentHarness.ClaudeConfigDir)
    }
    if r.ClaudeConfigDirLegacy != "" {
        return expandTilde(r.ClaudeConfigDirLegacy)
    }
    return DefaultClaudeConfigDir()
}

// GetHarnessConfig returns a HarnessConfig for use by harness.Resolve().
func (r *Role) GetHarnessConfig() harness.HarnessConfig {
    htype := r.GetHarnessType()
    cfg := harness.HarnessConfig{
        HarnessType: htype,
        Model:       r.GetModel(),
    }
    switch htype {
    case "claude_code":
        cfg.ConfigDir = r.GetClaudeConfigDir()
    case "codex":
        cfg.ConfigDir = r.GetCodexConfigDir()
    case "generic":
        if r.AgentHarness != nil {
            cfg.Command = r.AgentHarness.Command
        }
    }
    return cfg
}
```

### Callers to update

| File | Change |
|------|--------|
| `session.go` | `agent.ResolveAgentType` → `harness.Resolve`, `.AgentType()` → `.Harness()`, `s.Model` → read from harness config |
| `daemon.go` | `.AgentType()` → `.Harness()`, `opts.Model` → via HarnessConfig |
| `cmd/agent_setup.go` | `role.GetAgentType` → `role.GetHarnessConfig()`, `agent.ResolveAgentType` → `harness.Resolve`, `role.Model` → `role.GetModel()` |
| `cmd/dry_run.go` | Same as agent_setup, update display strings |
| `cmd/run.go` | `--agent-type` flag → `--agent-harness` |
| `cmd/role.go` | Template strings (nested YAML), display output |
| `cmd/auth.go` | `config.DefaultClaudeConfigDir()` — unchanged, still needed |
| `config/role.go` | New `AgentHarnessConfig` struct, accessor methods, backward compat |
| `heartbeat_test.go` | Constructor call (uses generic harness now) |
| `otel_test.go` | Constructor call |
| `session_test.go` | State tests use `generic.New()` instead of `ResolveAgentType("true")` |
| E2E tests (6 files) | `agent_type:` → `agent_harness:\n  harness_type:` in YAML strings |
| Docs (10 files) | References to AgentType, update role templates |

### What stays the same

- `agent/shared/otelserver/` — unchanged, shared by claude and codex harnesses
- `agent/shared/eventstore/` — unchanged
- `agent/shared/outputcollector/` — unchanged (now owned by GenericHarness instead of Agent)
- `agent/monitor/` — unchanged
- `agent/collector/` — unchanged (hook collector)
- `agent/agent.go` — updated but same role (owns harness + monitor)
- `LaunchConfig`, `InputSender`, `PTYInputSender` — moved from `adapter/` to `harness/` but unchanged

### `harness.Resolve()` function

```go
func Resolve(cfg HarnessConfig, log *activitylog.Logger) (Harness, error) {
    switch cfg.HarnessType {
    case "claude_code", "claude":
        return claude.New(cfg, log), nil
    case "codex":
        return codex.New(cfg, log), nil
    case "generic":
        if cfg.Command == "" {
            return nil, fmt.Errorf("generic harness requires a command")
        }
        return generic.New(cfg), nil
    default:
        return nil, fmt.Errorf("unknown harness type: %q (supported: claude_code, codex, generic)", cfg.HarnessType)
    }
}
```

Unknown harness types return an error instead of silently falling through.

## Task breakdown

1. **Restructure Role YAML config** — Add `AgentHarnessConfig` struct, nested `agent_harness` YAML section, accessor methods with backward compat fallback, update role template. Update all callers of `role.Model`, `role.GetAgentType()`, `role.GetClaudeConfigDir()`.

2. **Create `agent/harness/` package with Harness interface** — Define Harness interface (with HandleOutput), HarnessConfig, move LaunchConfig, CommandArgsConfig, InputSender, PTYInputSender from current locations. Add `Resolve()` function.

3. **Create `agent/harness/claude/` package** — Merge ClaudeCodeType + ClaudeCodeAdapter into ClaudeCodeHarness. Constructor takes `HarnessConfig`. HandleOutput is no-op. Move hook_handler, otel_parser, sessionlog from adapter/claude/.

4. **Create `agent/harness/codex/` package** — Merge CodexType + CodexAdapter into CodexHarness. Constructor takes `HarnessConfig`. HandleOutput is no-op. Move launch, otel_parser from adapter/codex/.

5. **Create `agent/harness/generic/` package** — GenericHarness with internal output collector. HandleOutput feeds collector. Start() bridges collector state to events channel. No-op config/launch methods.

6. **Update Agent struct** — Replace `agentType + adapter` with single `harness` field. Remove `NewAdapter()`, all nil-adapter branches, `bridgeOutputToMonitor`, `OtelMetrics`, `SetOtelLogFiles`, `outputCollector`. Agent.HandleOutput() delegates to harness.HandleOutput().

7. **Update callers** — session.go, daemon.go, agent_setup.go, dry_run.go, run.go, role.go (cmd).

8. **Delete old packages** — Remove agent/agent_type.go, agent/adapter/.

9. **Update e2e tests and docs** — Nested YAML in test strings, update documentation references.

Task 1 can be done first (config changes, no harness dependency). Tasks 2-5 can be done in parallel after task 1 (new packages). Task 6 depends on 2-5. Task 7 depends on 6. Task 8 depends on 7. Task 9 is independent cleanup.
