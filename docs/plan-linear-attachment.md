# Plan: Linear Issue Attachment (outbound agent status)

## Goal

When an h2 agent is linked to a Linear issue, surface the agent's live status on
that issue as a Linear **attachment** — the same first-class object Linear uses
for GitHub PRs, Sentry errors, etc. From the Linear issue you can see which h2
agent is working it, its current state, and a link back to the work (branch/PR).

This is **outbound only** (h2 -> Linear). It is not a `bridge.Bridge`: there is
no inbound message routing and Linear is not a chat channel.

## Hard constraint: do not harm the agent or h2

The agent must never be aware of or affected by this feature. Concretely:

1. **Out of the agent's context.** No instructions, no prompts, no extra tokens
   injected into the agent to do Linear bookkeeping. h2 makes all Linear calls
   itself.
2. **Off the hot path.** All Linear work runs in a dedicated goroutine. The
   agent's launch, I/O, and state machine never block on or wait for Linear.
3. **Fail-safe.** Every Linear call is wrapped; errors are logged and dropped,
   never propagated. Linear being down, slow, rate-limited, or misconfigured has
   **zero** effect on the agent or h2 — worst case the attachment is stale.
4. **Opt-in / inert by default.** No linked issue => zero behavior change, zero
   network calls.
5. **Removable.** Lives in a self-contained `internal/linear` package that only
   *subscribes* to existing monitor events. Deleting one wiring line in the
   daemon fully disables it.

## Why direct token (not MCP)

The Linear MCP is a tool surface for an LLM client (the agent). To keep Linear
updates out of the agent's context, **h2 itself** must make the calls — so it
can't piggyback on the agent's MCP tools. h2 calls Linear's GraphQL API directly
with a stored token, exactly like the Telegram bridge holds `bot_token`. This is
simpler, fully headless, and isolated. (An "h2 as its own MCP client" path was
considered and rejected: extra process/protocol dependency for no gain.)

## Existing seams (verified)

- `internal/session/agent/monitor/monitor.go`
  - `StateChanged() <-chan struct{}` — closed on every state transition.
  - `State() (State, SubState)` — current state, read-only.
  - `SetOnSessionStarted`, `SetOnAuthError`, `SetOnServerError`,
    `SetOnUsageLimit` — milestone callbacks, invoked outside the lock.
  - States: `Initialized / Active / Idle / Exited` with `SubState`
    (`internal/session/agent/monitor/state.go`).
- `internal/config/runtime_config.go` — `RuntimeConfig` is the persisted
  per-session metadata (`RoleName`, `Pod`, `CWD`, ...). The linked issue ID lives
  here.
- `internal/config/config.go` — root `Config { Bridges, Users }`; add a sibling
  `Linear`.
- `internal/cmd/run.go:263` — where `h2 run` flags are registered.

## Design

### 1. Credential (config)

```go
// internal/config/config.go
type Config struct {
    Bridges map[string]*BridgesConfig `yaml:"bridges"`
    Users   map[string]*UserConfig    `yaml:"users"`
    Linear  *LinearConfig             `yaml:"linear,omitempty"`
}

type LinearConfig struct {
    APIToken string `yaml:"api_token"` // Linear personal API key
}
```

`~/.h2/config.yaml`:

```yaml
linear:
  api_token: "lin_api_..."
```

Token absent => feature inert regardless of `--linear`.

### 2. Link an agent to an issue (per session)

- Add `LinearIssue string `json:"linear_issue,omitempty"`` to `RuntimeConfig`.
- New flag in `run.go`: `--linear LIN-123` sets it.
- Fallback detection (later, optional): parse the branch name / task text for a
  `LIN-\d+` identifier. Keep v1 explicit-only.

### 3. The watcher (`internal/linear`)

A small package with:

- `Client` — thin GraphQL client over Linear's `attachmentCreate` /
  `attachmentUpdate`. No SDK dependency; one `http.Client` + hand-written query
  strings. Every method returns an error that the caller swallows.
- `Watcher` — started by the daemon only when `RuntimeConfig.LinearIssue != ""`
  and a token is configured. It:
  1. Resolves the issue (`LIN-123` -> issue UUID) once at startup.
  2. Creates the attachment (idempotent: keyed by a stable `metadata` field like
     `h2AgentName`, so resume reuses it instead of duplicating).
  3. Runs a goroutine that `select`s on `monitor.StateChanged()`, reads
     `monitor.State()`, **debounces** (coalesce bursts; ignore sub-300ms
     flickers), and pushes a status update.
  4. Registers milestone callbacks (`SetOnAuthError`, `SetOnServerError`,
     `SetOnSessionStarted`) to set richer subtitles ("blocked: auth error").
  5. On agent exit, sets a terminal status and stops.

Status mapping (coarse on purpose — milestones, not real-time flicker):

| h2 state                  | Attachment shows            |
|---------------------------|-----------------------------|
| Initialized / SessionStart| "starting"                  |
| Active (+ subState/tool)  | "working" (+ what)          |
| Idle                      | "idle / awaiting input"     |
| waiting-on-permission     | "blocked: needs permission" |
| auth/server error         | "blocked: <error>"          |
| Exited                    | "finished" / "stopped"      |

Attachment fields: `title` = agent name, `subtitle` = status line, `url` = the
work link (branch compare URL / PR once known; fallback to a stable
`h2://agent/<name>` deep link), `metadata` = `{h2AgentName, h2SessionID}`.

### 4. Wiring (daemon)

In the daemon setup where the `AgentMonitor` is constructed and `RuntimeConfig`
is available, add a single guarded block:

```go
if cfg.Linear != nil && cfg.Linear.APIToken != "" && rc.LinearIssue != "" {
    w := linear.NewWatcher(cfg.Linear.APIToken, rc.LinearIssue, rc.AgentName, monitor)
    go w.Run(ctx) // self-contained; logs+drops all errors
}
```

That `if` block is the entire integration surface. Remove it -> feature gone.

## Token-cost & noise controls

- Debounce + coalesce so a flapping state can't become a request storm.
- Only push on *changed* coarse status, not every `StateChanged` tick.
- Cap update frequency (e.g. min interval between Linear writes per agent).

## Testing

- `internal/linear` unit tests with a stubbed HTTP transport: state-sequence ->
  expected attachment payloads; verify debounce and idempotent create-on-resume.
- Verify that with no token / no `--linear`, `NewWatcher` is never constructed
  and no goroutine starts.
- Follow CLAUDE.md testing rules: `setupFakeHome(t)` / `H2_DIR`, never touch the
  real config dir.

## Phasing

1. **v1 (this plan):** config + `--linear` flag + `internal/linear` watcher +
   daemon wiring + coarse status. Explicit issue link only.
2. **v2 (optional):** auto-detect `LIN-\d+` from branch/task; PR-aware `url`;
   per-pod default issue.

## Out of scope

- Inbound (Linear -> h2). No assignment-triggered spawning, no comment routing.
  That would be the separate "full bridge channel" / "native agent app"
  direction.
