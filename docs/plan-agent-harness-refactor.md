# Phase 2.5: Merge AgentType + AgentAdapter into Harness

## Problem

Adding a new agent integration requires implementing two separate interfaces in two different packages:

1. **AgentType** (`agent/agent_type.go`) — static config: Name, Command, BuildCommandArgs, BuildCommandEnvVars, EnsureConfigDir, NewAdapter
2. **AgentAdapter** (`agent/adapter/adapter.go`) — runtime: Name, PrepareForLaunch, Start, HandleHookEvent, Stop

These are 1:1 coupled (`NewAdapter()` on AgentType creates the AgentAdapter). Both have `Name()`. The adapter implementations live in `agent/adapter/claude/` and `agent/adapter/codex/` while the type definitions all live together in `agent/agent_type.go`. To add Codex you touch 3 packages across 2 directory trees.

Additionally, "AgentType" is confusing — the Claude LLM can be used through multiple harnesses (Claude Code CLI, future API-direct). This is really "agent harness": the CLI tool that wraps the LLM.

The `GenericType` is unused — nothing creates generic agents today. Delete it entirely.

Harness-specific config (`model`, `claude_config_dir`) lives as flat top-level fields on the Role struct, making it unclear which fields apply to which harness. These should be nested under an `agent_harness` section.

## Proposal

Merge both interfaces into a single `Harness` interface. Each harness gets its own package with all implementation in one place. Delete GenericType/GenericHarness. Nest harness config in YAML.

### The Harness interface

```go
// package: agent/harness

type Harness interface {
    // Identity
    Name() string            // "claude_code" or "codex"
    Command() string         // executable name: "claude" or "codex"
    DisplayCommand() string  // for display: "claude" or "codex"

    // Config (called before launch)
    BuildCommandArgs(cfg CommandArgsConfig) []string
    BuildCommandEnvVars(h2Dir string) map[string]string
    EnsureConfigDir(h2Dir string) error

    // Launch (called once, before child process starts)
    PrepareForLaunch(agentName, sessionID string) (LaunchConfig, error)

    // Runtime (called after child process starts)
    Start(ctx context.Context, events chan<- monitor.AgentEvent) error
    HandleHookEvent(eventName string, payload json.RawMessage) bool
    Stop()
}
```

Note: `BuildCommandEnvVars` and `EnsureConfigDir` signatures simplify from `(h2Dir, roleName string)` to `(h2Dir string)`. The harness receives its config at construction time (via `HarnessConfig`), so it no longer needs to re-load the role to find its config dir.

### HarnessConfig (passed to constructors)

```go
// package: agent/harness

// HarnessConfig holds harness-specific configuration extracted from the Role.
type HarnessConfig struct {
    HarnessType string // "claude_code" or "codex"
    Model       string // model name (shared by both)
    ConfigDir   string // harness-specific config dir (resolved by Role)
}
```

Each harness constructor receives this:
- `claude.New(cfg harness.HarnessConfig, log *activitylog.Logger) *ClaudeCodeHarness`
- `codex.New(cfg harness.HarnessConfig, log *activitylog.Logger) *CodexHarness`

The harness stores `cfg.ConfigDir` and uses it in `BuildCommandEnvVars` and `EnsureConfigDir` instead of re-loading the role.

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
```

No `generic/` package — GenericType is deleted entirely.

### What gets deleted

```
agent/agent_type.go          — replaced by harness/harness.go + per-harness packages
agent/agent_type_test.go     — split into per-harness test files
agent/adapter/adapter.go     — interface merged into harness.go, types moved
agent/adapter/adapter_test.go
agent/adapter/claude/         — moved to harness/claude/
agent/adapter/codex/          — moved to harness/codex/
GenericType (struct)          — deleted, not moved
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
- `PrepareForLaunch`: calls `a.harness.PrepareForLaunch()` directly
- `Start`: calls `a.harness.Start(ctx, eventCh)` for all harnesses, plus `a.agentMonitor.Run(ctx)`. The output-collector bridge (`bridgeOutputToMonitor`) is removed — both harnesses emit proper AgentEvents.
- `HandleHookEvent`: calls `a.harness.HandleHookEvent()` directly
- `Stop`: calls `a.harness.Stop()` directly
- `AgentType()` → `Harness()` returning `harness.Harness`
- `SetOtelLogFiles` gating: can be removed entirely — both harnesses handle OTEL through their adapter's parser
- Hook collector gating: check `a.harness.Name() == "claude_code"`
- `Metrics()`: always reads from monitor (both harnesses emit metric events). Legacy `OtelMetrics` struct and `bridgeOutputToMonitor` can be deleted.

### Simplified Agent (no nil-adapter branching)

With GenericType deleted, the adapter is never nil. The 6 `if a.adapter != nil` branches in agent.go collapse:

| Current code | After merge |
|--------------|-------------|
| `if a.adapter != nil { adapter.Start(...) } else { bridgeOutputToMonitor() }` | `harness.Start(ctx, eventCh)` |
| `if a.adapter != nil { adapter.HandleHookEvent(...) }` | `harness.HandleHookEvent(...)` |
| `if a.adapter != nil { adapter.Stop() }` | `harness.Stop()` |
| `if a.adapter != nil { monitor metrics } else { OtelMetrics }` | monitor metrics always |
| `if a.adapter != nil { type-assert OtelPort }` | type-assert on harness (unchanged pattern) |
| `adapter = agentType.NewAdapter(log)` | gone — harness IS the adapter |

Also delete: `bridgeOutputToMonitor()`, `OtelMetrics` struct, `SetOtelLogFiles()`, `otelLogsFile`/`otelMetricsFile` fields.

### Name changes

| Old | New |
|-----|-----|
| `AgentType` (interface) | `Harness` |
| `ClaudeCodeType` | `ClaudeCodeHarness` |
| `CodexType` | `CodexHarness` |
| `GenericType` | deleted |
| `NewClaudeCodeType()` | `claude.New(cfg, log)` |
| `NewCodexType()` | `codex.New(cfg, log)` |
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
# New format
name: my-agent
agent_harness:
  harness_type: claude_code     # "claude_code" or "codex"
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

### Config struct changes (`config/role.go`)

```go
// AgentHarnessConfig holds harness-specific configuration.
type AgentHarnessConfig struct {
    HarnessType     string `yaml:"harness_type,omitempty"`
    Model           string `yaml:"model,omitempty"`
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

// GetCodexConfigDir returns the Codex config dir (empty if not specified).
func (r *Role) GetCodexConfigDir() string {
    if r.AgentHarness != nil && r.AgentHarness.CodexConfigDir != "" {
        return r.AgentHarness.CodexConfigDir
    }
    return ""
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
| `heartbeat_test.go` | Constructor call |
| `otel_test.go` | Constructor call |
| E2E tests (6 files) | `agent_type:` → `agent_harness:\n  harness_type:` in YAML strings |
| Docs (10 files) | References to AgentType, update role templates |

### What stays the same

- `agent/shared/otelserver/` — unchanged, shared by claude and codex harnesses
- `agent/shared/eventstore/` — unchanged
- `agent/shared/outputcollector/` — still present for `NoteOutput` signal (output tracking for status display), but no longer bridges to monitor for state detection
- `agent/monitor/` — unchanged
- `agent/collector/` — unchanged (hook collector)
- `agent/agent.go` — updated but same role (owns harness + monitor + output collector)
- `LaunchConfig`, `InputSender`, `PTYInputSender` — moved from `adapter/` to `harness/` but unchanged

### `harness.Resolve()` function

```go
func Resolve(cfg HarnessConfig, log *activitylog.Logger) (Harness, error) {
    switch cfg.HarnessType {
    case "claude_code", "claude":
        return claude.New(cfg, log), nil
    case "codex":
        return codex.New(cfg, log), nil
    default:
        return nil, fmt.Errorf("unknown harness type: %q (supported: claude_code, codex)", cfg.HarnessType)
    }
}
```

Unknown harness types return an error instead of falling through to GenericType.

## Task breakdown

1. **Restructure Role YAML config** — Add `AgentHarnessConfig` struct, nested `agent_harness` YAML section, accessor methods with backward compat fallback, update role template. Update all callers of `role.Model`, `role.GetAgentType()`, `role.GetClaudeConfigDir()`.

2. **Create `agent/harness/` package with Harness interface** — Move interface, HarnessConfig, LaunchConfig, CommandArgsConfig, InputSender, PTYInputSender from current locations. Add `Resolve()` function.

3. **Create `agent/harness/claude/` package** — Merge ClaudeCodeType + ClaudeCodeAdapter into ClaudeCodeHarness. Constructor takes `HarnessConfig`. Move hook_handler, otel_parser, sessionlog from adapter/claude/.

4. **Create `agent/harness/codex/` package** — Merge CodexType + CodexAdapter into CodexHarness. Constructor takes `HarnessConfig`. Move launch, otel_parser from adapter/codex/.

5. **Update Agent struct** — Replace `agentType + adapter` with single `harness` field. Remove `NewAdapter()`, nil-adapter branches, `bridgeOutputToMonitor`, `OtelMetrics`, `SetOtelLogFiles`. Simplify all method dispatching.

6. **Update callers** — session.go, daemon.go, agent_setup.go, dry_run.go, run.go, role.go (cmd).

7. **Delete old packages** — Remove agent/agent_type.go, agent/adapter/, GenericType.

8. **Update e2e tests and docs** — Nested YAML in test strings, update documentation references.

Task 1 can be done first (config changes, no harness dependency). Tasks 2-4 can be done in parallel after task 1 (new packages). Task 5 depends on 2-4. Task 6 depends on 5. Task 7 depends on 6. Task 8 is independent cleanup.
