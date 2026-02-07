# Design: `h2 hook` Command & Hook Collector

## Problem

h2 has limited visibility into what Claude Code is doing between OTEL events. OTEL gives us token counts and API request activity, but Claude Code's hook system exposes much richer lifecycle events — tool use, notifications, session start/end, subagent activity, idle prompts, etc. We want to capture these as a parallel data source to OTEL.

## Concept: Collectors

OTEL is one "collector" — it runs an HTTP server, sets env vars on the child process, and accumulates metrics. The hook collector is another: it provides a CLI command (`h2 hook`) that Claude Code invokes as a hook handler, and it reports events back to the h2 session over the existing Unix socket.

Different agent types or setups enable different collectors. A Claude Code agent gets both OTEL and hooks. A generic shell agent might get neither. Future agents might have their own collectors.

```
Agent (Session)
├── OTEL Collector     — env vars, HTTP server, parses OTLP
├── Hook Collector     — `h2 hook` CLI command, receives hook JSON on stdin
└── (future)           — file watcher, log tailer, etc.
```

## Agent Types

### The problem with "just a command"

Today, `h2 run --name concierge -- claude` treats `claude` as an opaque executable.
But h2 already has significant domain knowledge about Claude Code: it injects
`--session-id`, sets OTEL env vars, parses OTEL events with a Claude-specific
parser, and (with this proposal) would handle Claude Code hooks. This knowledge
is scattered across `Session.childArgs()` (hardcoded `s.Command == "claude"`
check), `Session.New()` (hardcoded `ClaudeCodeHelper`), and the OTEL parser.

As we add more integration points (hooks, collector authority, launch config),
we need a first-class concept: **agent types**. `claude` in `h2 run claude` is
not just a command to exec — it's an enum of a supported agent type, and h2 has
domain knowledge of how to run it.

### AgentType interface

`AgentType` replaces `AgentHelper` and covers the full agent lifecycle:

```go
// AgentType defines how h2 launches, monitors, and interacts with a specific
// kind of agent. Each supported agent (Claude Code, generic shell, future types)
// implements this interface.
type AgentType interface {
    // Name returns the agent type identifier (e.g. "claude", "generic").
    Name() string

    // --- Launch ---

    // Command returns the executable to run.
    Command() string

    // PrependArgs returns extra args to inject before the user's args.
    // e.g. Claude returns ["--session-id", uuid] when sessionID is set.
    PrependArgs(sessionID string) []string

    // ChildEnv returns extra environment variables for the child process.
    // Called after collectors are started so it can include OTEL endpoints, etc.
    ChildEnv(collectors *CollectorPorts) map[string]string

    // --- Collectors ---

    // Collectors returns which collectors this agent type supports.
    Collectors() CollectorSet

    // OtelParser returns the parser for this agent's OTEL events.
    // Returns nil if OTEL is not supported or no parsing is needed.
    OtelParser() OtelParser

    // --- Display ---

    // DisplayCommand returns the command name for display purposes.
    // e.g. "claude" even if the actual binary is "/usr/local/bin/claude".
    DisplayCommand() string
}

// CollectorPorts holds connection info for active collectors,
// passed to ChildEnv so the agent type can configure the child.
type CollectorPorts struct {
    OtelPort int  // 0 if OTEL not active
}

type CollectorSet struct {
    Otel  bool
    Hooks bool
}
```

### Implementations

```go
// ClaudeCodeType — full integration: OTEL, hooks, session ID, env vars.
type ClaudeCodeType struct {
    parser *ClaudeCodeParser
}

func (t *ClaudeCodeType) Name() string           { return "claude" }
func (t *ClaudeCodeType) Command() string         { return "claude" }
func (t *ClaudeCodeType) DisplayCommand() string   { return "claude" }
func (t *ClaudeCodeType) Collectors() CollectorSet { return CollectorSet{Otel: true, Hooks: true} }
func (t *ClaudeCodeType) OtelParser() OtelParser   { return t.parser }

func (t *ClaudeCodeType) PrependArgs(sessionID string) []string {
    if sessionID != "" {
        return []string{"--session-id", sessionID}
    }
    return nil
}

func (t *ClaudeCodeType) ChildEnv(cp *CollectorPorts) map[string]string {
    if cp.OtelPort == 0 {
        return nil
    }
    endpoint := fmt.Sprintf("http://127.0.0.1:%d", cp.OtelPort)
    return map[string]string{
        "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
        "OTEL_METRICS_EXPORTER":        "otlp",
        "OTEL_LOGS_EXPORTER":           "otlp",
        "OTEL_TRACES_EXPORTER":         "none",
        "OTEL_EXPORTER_OTLP_PROTOCOL":  "http/json",
        "OTEL_EXPORTER_OTLP_ENDPOINT":  endpoint,
        "OTEL_METRIC_EXPORT_INTERVAL":  "5000",
        "OTEL_LOGS_EXPORT_INTERVAL":    "1000",
    }
}

// GenericType — no integration, just runs a command.
type GenericType struct {
    command string
}

func (t *GenericType) Name() string                           { return "generic" }
func (t *GenericType) Command() string                         { return t.command }
func (t *GenericType) DisplayCommand() string                   { return t.command }
func (t *GenericType) Collectors() CollectorSet                 { return CollectorSet{} }
func (t *GenericType) OtelParser() OtelParser                   { return nil }
func (t *GenericType) PrependArgs(sessionID string) []string    { return nil }
func (t *GenericType) ChildEnv(cp *CollectorPorts) map[string]string { return nil }
```

### Resolution: command string → AgentType

When the user runs `h2 run -- claude --verbose`, h2 resolves the command to
an agent type:

```go
// ResolveAgentType maps a command name to a known agent type,
// falling back to GenericType for unknown commands.
func ResolveAgentType(command string) AgentType {
    switch command {
    case "claude":
        return NewClaudeCodeType()
    default:
        return &GenericType{command: command}
    }
}
```

This replaces the current hardcoded checks:
- `Session.New()` hardcoding `ClaudeCodeHelper` → now uses `agentType.OtelParser()` etc.
- `Session.childArgs()` checking `s.Command == "claude"` → now uses `agentType.PrependArgs()`
- `ClaudeCodeHelper.OtelEnv()` → now `agentType.ChildEnv()`

### How it flows through the system

```
h2 run --name concierge -- claude --verbose
  → ResolveAgentType("claude") → ClaudeCodeType
  → generate sessionID UUID
  → ForkDaemon(name, sessionID, agentType, userArgs=["--verbose"])
    → h2 _daemon --name concierge --session-id <uuid> --agent-type claude -- claude --verbose
      → RunDaemon resolves agentType from --agent-type flag
      → Agent.Init(agentType)
        → starts collectors based on agentType.Collectors()
      → childArgs = agentType.PrependArgs(sessionID) + userArgs
        → ["--session-id", uuid, "--verbose"]
      → childEnv = agentType.ChildEnv(collectorPorts) + {"H2_ACTOR": name}
      → StartPTY(agentType.Command(), childArgs, childEnv)
```

### Session and Agent changes

The `Session` no longer needs `Command` or `childArgs()`. The Agent holds
the type and handles launch config:

```go
type Session struct {
    Name      string
    SessionID string
    AgentType AgentType  // replaces Command string
    UserArgs  []string   // user-provided args (without injected flags)
    // ...rest unchanged...
}

type Agent struct {
    agentType  AgentType
    otel       *OtelCollector
    hooks      *HookCollector
    // ...
}

// ChildArgs returns the full args for the child process.
func (a *Agent) ChildArgs(sessionID string, userArgs []string) []string {
    prepend := a.agentType.PrependArgs(sessionID)
    return append(prepend, userArgs...)
}

// ChildEnv returns env vars for the child process.
func (a *Agent) ChildEnv(agentName string) map[string]string {
    ports := &CollectorPorts{}
    if a.otel != nil {
        ports.OtelPort = a.otel.Port()
    }
    env := a.agentType.ChildEnv(ports)
    if env == nil {
        env = make(map[string]string)
    }
    env["H2_ACTOR"] = agentName
    return env
}
```

## Data Model

### Current state of the world

Status data lives across several layers today:

```
Session (internal/session/session.go)
├── state: State (Active|Idle|Exited)    — derived from child output + OTEL activity
├── stateChangedAt: time.Time
├── StartTime: time.Time
├── Queue.PendingCount(): int
│
├── Agent (internal/session/agent/otel.go)
│   ├── helper: AgentHelper              — agent-type-specific config
│   ├── metrics: *OtelMetrics            — token counts, cost, API request counts
│   ├── port: int                        — OTEL collector HTTP port
│   └── otelNotify: chan struct{}         — activity signal for state machine
│
└── Daemon.AgentInfo() → AgentInfo       — snapshot for status protocol & h2 list
```

`Session.state` (Active/Idle/Exited) is the only "derived" status. It's computed
by `watchState()` which listens for child output and OTEL events and runs an
idle timer. Everything else is raw data that gets formatted for display.

`AgentInfo` is the external representation — it's what the socket protocol returns
for `"status"` requests and what `h2 list` and the status bar consume.

### Proposed: Three layers of agent state

The Agent's internal state is organized into three distinct layers:

```
Agent
├── 1. Collector Data    — raw accumulated metrics from each collector
├── 2. Derived State     — active/idle/exited, computed by Agent from collector signals
└── 3. External View     — AgentInfo, a flat serialization for the socket protocol
```

Each layer has a clear responsibility and doesn't bleed into the others.

#### Layer 1: Collector data

Each collector is a pure data accumulator. It receives events, updates internal
counters, and signals a notify channel. Collectors know nothing about idle/active
state — they just collect.

```go
// OtelCollector — accumulates token/cost data from OTLP events.
// Pure data: receives HTTP posts, updates metrics, signals notify.
type OtelCollector struct {
    mu       sync.RWMutex
    metrics  OtelMetrics     // token counts, cost, request counts
    listener net.Listener
    server   *http.Server
    port     int
    notify   chan struct{}   // signaled on each event (for layer 2)
}

// Metrics returns a point-in-time copy of accumulated data.
// The copy avoids races — callers can read without holding locks.
func (c *OtelCollector) Metrics() OtelMetrics { ... }

// HookCollector — accumulates lifecycle data from Claude Code hooks.
// Pure data: receives hook events, updates state, signals event channel.
type HookCollector struct {
    mu            sync.RWMutex
    lastEvent     string       // "PreToolUse", "PostToolUse", etc.
    lastEventTime time.Time
    lastToolName  string       // from PreToolUse/PostToolUse tool_name
    toolUseCount  int64        // total tool invocations seen
    eventCh       chan string   // sends event name (not just signal) so Agent can interpret
}

// ProcessEvent records a hook event and sends the event name to the Agent.
func (c *HookCollector) ProcessEvent(eventName string, payload json.RawMessage) {
    c.mu.Lock()
    c.lastEvent = eventName
    c.lastEventTime = time.Now()
    // Extract tool_name from PreToolUse/PostToolUse payloads
    if eventName == "PreToolUse" || eventName == "PostToolUse" {
        c.lastToolName = extractToolName(payload)
    }
    if eventName == "PreToolUse" {
        c.toolUseCount++
    }
    c.mu.Unlock()

    // Send event name to Agent's state watcher (non-blocking)
    select {
    case c.eventCh <- eventName:
    default:
    }
}

// State returns a point-in-time copy of accumulated data.
func (c *HookCollector) State() HookState { ... }
```

Note: the "snapshot" methods are just returning a copy of the collector's
internal state to avoid holding locks across call boundaries. The copy struct
is the same shape as the collector's fields — it's not a separate concept,
just a concurrency safety measure.

#### Layer 2: Derived state (idle/active/exited)

The Agent owns a **state watcher** goroutine that observes collector signals
and child process output, and derives the current activity state. This is the
only place idle/active logic lives.

The state watcher uses a **committed authority** model:

**Priority order** (highest to lowest):
1. **Hook collector** — most granular (knows about individual tool use, subagents)
2. **OTEL collector** — knows about API requests and token activity
3. **Output timer** — fallback, watches child PTY output timing (current behavior)

**Commitment rules:**
- On first hook event → hook collector becomes the idle authority for this session
- On first OTEL event (if no hooks have fired) → OTEL becomes the idle authority
- If neither has fired → output timer is the authority (current behavior)
- Child exit always overrides everything → Exited

Once committed, that source stays authoritative. We trust that if one hook event
fired, the hook setup is working and more will come. We don't wait for each
individual hook type to fire before trusting the data source.

**Why not mix sources?** Mixing creates confusing edge cases. If the hook
collector says "last event 10s ago" but the output timer says "output 1s ago",
which one is right? The output timer would keep resetting idle even when the
agent is genuinely waiting for the next turn. By committing to one authority,
the status is consistent and predictable.

```go
type IdleAuthority int
const (
    AuthorityOutputTimer IdleAuthority = iota
    AuthorityOtel
    AuthorityHooks
)

type AgentState int
const (
    AgentActive AgentState = iota
    AgentIdle
    AgentExited
)

type Agent struct {
    agentType  AgentType

    // Layer 1: Collectors (nil if not active for this agent type)
    otel       *OtelCollector
    hooks      *HookCollector

    // Layer 2: Derived state
    mu            sync.RWMutex
    state         AgentState
    stateChangedAt time.Time
    idleAuthority IdleAuthority
    idleTimer     *time.Timer
    stateCh       chan struct{}   // closed on state change (same pattern as Session today)

    // Signals
    stateNotify   chan struct{}   // signaled when derived state changes (for Session)
    stopCh        chan struct{}
}
```

The state watcher goroutine is internal to Agent. It watches collector notify
channels, manages the idle timer, and promotes the authority. Nobody outside
the Agent calls into this logic — the Session just observes state changes.

```go
// watchState is the Agent's internal goroutine. Collectors signal their
// notify channels; this goroutine interprets those signals to derive state.
func (a *Agent) watchState() {
    for {
        select {
        case <-a.otelNotify():
            a.handleCollectorActivity(AuthorityOtel)
        case event := <-a.hooksEventCh():
            a.handleHookEvent(event)
        case <-a.outputNotify:
            a.handleCollectorActivity(AuthorityOutputTimer)
        case <-a.idleTimer.C:
            a.setState(AgentIdle)
        case <-a.stopCh:
            return
        }
    }
}

// handleCollectorActivity promotes authority and resets idle if this
// source is the current authority (or higher).
func (a *Agent) handleCollectorActivity(source IdleAuthority) {
    a.mu.Lock()
    defer a.mu.Unlock()
    if source > a.idleAuthority {
        a.idleAuthority = source
    }
    if source >= a.idleAuthority {
        a.setStateLocked(AgentActive)
        a.resetIdleTimer()
    }
}

// handleHookEvent is called when the hook collector receives an event.
// It updates derived state based on the hook event type.
func (a *Agent) handleHookEvent(eventName string) {
    a.mu.Lock()
    defer a.mu.Unlock()

    // Promote to hook authority on first hook event
    if a.idleAuthority < AuthorityHooks {
        a.idleAuthority = AuthorityHooks
    }

    switch eventName {
    case "SessionStart":
        // Session started — just commit authority, no state change yet
    case "UserPromptSubmit":
        a.setStateLocked(AgentActive)
    case "PreToolUse", "PostToolUse":
        a.setStateLocked(AgentActive)
    case "PermissionRequest":
        a.setStateLocked(AgentActive) // blocked on permission, but still "active"
    case "Stop":
        a.setStateLocked(AgentIdle)
    case "SessionEnd":
        a.setStateLocked(AgentExited)
    }
}

// otelNotify returns the OTEL collector's notify channel, or a nil channel
// (blocks forever) if OTEL is not active.
func (a *Agent) otelNotify() <-chan struct{} {
    if a.otel != nil { return a.otel.notify }
    return nil
}

// hooksEventCh returns the hook collector's event channel, or nil.
// Sends event names (e.g. "PreToolUse") so watchState can interpret them.
func (a *Agent) hooksEventCh() <-chan string {
    if a.hooks != nil { return a.hooks.eventCh }
    return nil
}
```

The Session simplifies dramatically — it just watches for Agent state changes
and child process exit:

```go
func (s *Session) watchState(stop <-chan struct{}) {
    for {
        select {
        case <-s.Agent.StateChanged():
            // Agent state changed — update Session state to match.
            // Re-render status bars, etc.
        case <-s.exitNotify:
            s.Agent.SetExited()
        case <-stop:
            return
        }
    }
}
```

The Session feeds child output into the Agent (since the Session owns the PTY):

```go
func (s *Session) NoteOutput() {
    s.Agent.NoteOutput()  // feeds into Agent's outputNotify
}
```

#### Layer 3: External view (AgentInfo)

`AgentInfo` is a flat JSON-friendly struct for external consumers (socket
protocol, `h2 list`, bridge service). It pulls from both Layer 1 (collector
data) and Layer 2 (derived state). It's the serialization boundary — internal
callers use Agent methods directly; external callers get AgentInfo over the wire.

```go
type AgentInfo struct {
    // Core (always present)
    Name          string `json:"name"`
    Command       string `json:"command"`
    SessionID     string `json:"session_id,omitempty"`
    Uptime        string `json:"uptime"`
    QueuedCount   int    `json:"queued_count"`

    // Derived state (layer 2)
    State         string `json:"state"`           // "active", "idle", "exited"
    StateDuration string `json:"state_duration"`

    // OTEL collector data (layer 1, omitted if collector not active)
    TotalTokens  int64   `json:"total_tokens,omitempty"`
    TotalCostUSD float64 `json:"total_cost_usd,omitempty"`

    // Hook collector data (layer 1, omitted if collector not active)
    LastToolUse  string `json:"last_tool_use,omitempty"`
    ToolUseCount int64  `json:"tool_use_count,omitempty"`
}

// AgentInfo builds the external view from all layers.
func (d *Daemon) AgentInfo() *AgentInfo {
    a := d.Session.Agent
    info := &AgentInfo{
        Name:          d.Session.Name,
        Command:       a.agentType.DisplayCommand(),
        SessionID:     d.Session.SessionID,
        Uptime:        formatDuration(time.Since(d.StartTime)),
        State:         a.State().String(),
        StateDuration: formatDuration(a.StateDuration()),
        QueuedCount:   d.Session.Queue.PendingCount(),
    }

    // Pull from OTEL collector if active
    if a.otel != nil {
        m := a.otel.Metrics()
        info.TotalTokens = m.TotalTokens
        info.TotalCostUSD = m.TotalCostUSD
    }

    // Pull from hook collector if active
    if a.hooks != nil {
        s := a.hooks.State()
        info.LastToolUse = s.LastToolName
        info.ToolUseCount = s.ToolUseCount
    }

    return info
}
```

### Graceful degradation

The three-layer model gives us graceful degradation for free. If a collector
isn't active for this agent type, its pointer is nil and the corresponding
AgentInfo fields are simply omitted. Consumers never need to check what kind
of agent they're dealing with — they just check whether fields have data:

```
# Full data (OTEL + hooks active):
  ● concierge claude — active 3s, up 12m, 45k $1.23 [550e8400] (Edit session.go)

# OTEL only (no hooks configured):
  ● concierge claude — active 3s, up 12m, 45k $1.23 [550e8400]

# Hooks only (OTEL not connected yet):
  ● concierge claude — active 3s, up 12m [550e8400] (Edit session.go)

# Neither (generic agent):
  ● my-shell bash — idle 5s, up 2m
```

### AgentType controls which collectors are active

The `AgentType` interface (see [Agent Types](#agent-types)) determines which
collectors to start. `ClaudeCodeType` returns `{Otel: true, Hooks: true}`,
`GenericType` returns `{}`.

```go
func (a *Agent) Init(agentType AgentType) {
    a.agentType = agentType
    cfg := agentType.Collectors()
    if cfg.Otel {
        a.otel = NewOtelCollector(agentType.OtelParser())
    }
    if cfg.Hooks {
        a.hooks = NewHookCollector()
    }
    go a.watchState()
}
```

### Protocol changes

New request type for receiving hook events:

```json
{
  "type": "hook_event",
  "event_name": "PreToolUse",
  "payload": { ... full hook JSON from stdin ... }
}
```

## The `h2 hook` Command

A single CLI command that handles all Claude Code hook events:

```
h2 hook [--agent <agent-name>]
```

- Reads the hook JSON payload from **stdin** (Claude Code pipes it)
- Extracts `hook_event_name` from the JSON
- Reports the event to the agent's h2 session via the existing Unix socket
- Exits with code 0 and empty JSON `{}` (no blocking, no decisions)

The `--agent` flag identifies which h2 session to report to. In practice this
comes from the `H2_ACTOR` env var that h2 already injects into child processes,
so the hook config just uses `$H2_ACTOR`. The command defaults to `$H2_ACTOR`
if `--agent` is not explicitly provided.

### Hook Event Flow

```
Claude Code fires hook
  → runs: h2 hook
  → stdin: {"session_id": "...", "hook_event_name": "PreToolUse", "tool_name": "Bash", ...}
  → h2 hook reads H2_ACTOR env var → "concierge"
  → connects to ~/.h2/sockets/agent.concierge.sock
  → sends: {"type": "hook_event", "event_name": "PreToolUse", "payload": {...}}
  → daemon receives event, calls hooks.ProcessEvent()
  → h2 hook exits 0, stdout: {}
```

### Hook Configuration

Users manually configure Claude Code to call `h2 hook` for the events we use.
No auto-install — if we add a setup command later it would be a separate `h2 setup-hooks`.

The `h2 hook` command is fast (reads stdin, sends to local Unix socket, exits) —
well under 100ms. No need for async hooks.

```json
{
  "hooks": {
    "SessionStart": [{"matcher": "", "hooks": [{"type": "command", "command": "h2 hook", "timeout": 5}]}],
    "SessionEnd": [{"matcher": "", "hooks": [{"type": "command", "command": "h2 hook", "timeout": 5}]}],
    "UserPromptSubmit": [{"matcher": "", "hooks": [{"type": "command", "command": "h2 hook", "timeout": 5}]}],
    "PreToolUse": [{"matcher": "", "hooks": [{"type": "command", "command": "h2 hook", "timeout": 5}]}],
    "PostToolUse": [{"matcher": "", "hooks": [{"type": "command", "command": "h2 hook", "timeout": 5}]}],
    "PermissionRequest": [{"matcher": "", "hooks": [{"type": "command", "command": "h2 hook", "timeout": 5}]}],
    "Stop": [{"matcher": "", "hooks": [{"type": "command", "command": "h2 hook", "timeout": 5}]}]
  }
}
```

### Hook-driven state machine

The hook events map to agent state transitions:

```
SessionStart       → commit hook authority (earliest signal hooks are working)
UserPromptSubmit   → active (agent starts working on a turn)
PreToolUse         → active (detail: which tool, update tool count)
PostToolUse        → active (tool done, might do more)
PermissionRequest  → active (blocked on permission, but still working)
Stop               → idle (turn finished, waiting for input)
SessionEnd         → exited
```

Note: `Stop` is preferred over `Notification(idle_prompt)` for idle detection.
Stop fires at the exact moment the turn ends. idle_prompt is a UI-level nudge
that may have delays. PermissionRequest is preferred over
`Notification(permission_prompt)` for the same reason — it has richer data
(tool_name, tool_input) and fires at the precise moment.

## Implementation Order

### Phase 1: AgentType refactor
1. Introduce `AgentType` interface in `internal/session/agent/agent_type.go`
2. Implement `ClaudeCodeType` and `GenericType`
3. Add `ResolveAgentType()` and wire into `h2 run` / `h2 _daemon`
4. Remove `AgentHelper` interface, `childArgs()`, hardcoded `s.Command == "claude"` checks
5. Refactor `Agent` to hold `AgentType` and delegate launch config

### Phase 2: Hook collector & plumbing
6. Add `HookCollector` struct to `internal/session/agent/hook_collector.go`
7. Refactor `Agent` to hold collectors with unified `ActivityNotify()`
8. Add `hook_event` request type to socket protocol and listener
9. Add `h2 hook` CLI command
10. Wire `ActivityNotify()` into session `watchState` (replacing `OtelNotify`)

### Phase 3: Status exposure
11. Add collector-derived fields to `AgentInfo`
12. Update `h2 list` to show current tool/activity (graceful degradation)
13. Update status bar rendering to use `Agent.Status()` snapshot

## Files to Modify/Create

**New files:**
- `internal/session/agent/agent_type.go` — AgentType interface, ClaudeCodeType, GenericType, ResolveAgentType
- `internal/session/agent/hook_collector.go` — HookCollector struct and event processing
- `internal/cmd/hook.go` — `h2 hook` CLI command

**Modified files:**
- `internal/session/agent/otel.go` — refactor Agent to hold AgentType + collectors, add ActivityNotify fan-in
- `internal/session/agent/agent_helper.go` — delete (replaced by agent_type.go)
- `internal/session/session.go` — replace Command/Args/childArgs with AgentType, use Agent.ChildArgs/ChildEnv
- `internal/session/daemon.go` — pass AgentType through ForkDaemon/RunDaemon, add --agent-type flag
- `internal/cmd/daemon.go` — add --agent-type flag, resolve AgentType
- `internal/cmd/run.go` — resolve AgentType from command, pass to ForkDaemon
- `internal/cmd/bridge.go` — same: resolve AgentType for concierge
- `internal/session/message/protocol.go` — add hook_event request type, AgentInfo fields
- `internal/session/listener.go` — handle hook_event requests
- `internal/cmd/ls.go` — display collector-derived info with graceful degradation
- `internal/cmd/root.go` — register hook command

## Decisions

1. **No auto-configure.** Users manually add hook config to their Claude Code settings.
   A future `h2 setup-hooks` command could automate this, but it's not in scope.

2. **Sync hooks.** `h2 hook` is fast enough (<100ms) that async is unnecessary.
   It reads stdin JSON, sends to a local Unix socket, and exits.

3. **7 hook events.** SessionStart, SessionEnd, UserPromptSubmit, PreToolUse,
   PostToolUse, PermissionRequest, Stop. This gives complete turn lifecycle
   coverage with no blind spots. Stop is preferred over Notification(idle_prompt)
   for idle detection; PermissionRequest over Notification(permission_prompt) for
   richer data.
