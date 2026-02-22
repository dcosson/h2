# Remove `agent.Agent` Wrapper Plan

This document outlines how to remove the `internal/session/agent/agent.go` wrapper and let `Session` own harness + monitor directly.

## Goal

- Remove thin pass-through wrapper methods in `agent.Agent`.
- Keep behavior identical.
- Preserve clean boundary:
  - `harness`: source-specific event normalization
  - `monitor`: state + metrics aggregation
  - `session`: orchestration/lifecycle

## Session Field Changes

In `internal/session/session.go`:

- Remove:
  - `Agent *agent.Agent`
- Add:
  - `harness harness.Harness`
  - `monitor *monitor.AgentMonitor`
  - `agentCancel context.CancelFunc`
  - `activityLog *activitylog.Logger`

## Lifecycle Changes

### Construction

- In `session.New(...)`:
  - Resolve minimal harness (same logic as today).
  - Initialize `monitor.New()`.

### Setup

- In `setupAgent()`:
  - Build `activityLog` and store on `Session`.
  - Re-resolve full harness and store on `Session`.
  - Call `PrepareForLaunch(...)` on `Session.harness`.
  - Wire event persistence with `Session.monitor.SetEventWriter(...)`.

### Start/Stop Pipeline

Replace wrapper-based startup with direct wiring:

- Start:
  - `ctx, s.agentCancel = context.WithCancel(...)`
  - `go s.harness.Start(ctx, s.monitor.Events())`
  - `go s.monitor.Run(ctx)`
- Stop:
  - `s.agentCancel()`
  - `s.harness.Stop()`

### Exit

- Replace `s.Agent.SetExited()` with `s.monitor.SetExited()` in child lifecycle loop.

## Method Migration (Wrapper -> Session)

Current wrapper methods from `agent.Agent` should move to `Session` or become direct field usage:

1. `PrepareForLaunch(...)`
- Move into `setupAgent`/private session helper.

2. `Start(ctx)`
- Move into session startup logic.

3. `HandleHookEvent(eventName, payload)`
- Add `Session.HandleHookEvent(...)` and forward to `s.harness.HandleHookEvent(...)`.

4. `SetEventWriter(fn)`
- Use `s.monitor.SetEventWriter(...)` directly in setup.

5. `State()`
- `Session.State()` -> `s.monitor.State()`.

6. `StateChanged()`
- `Session.StateChanged()` -> `s.monitor.StateChanged()`.

7. `WaitForState(ctx, target)`
- `Session.WaitForState(...)` -> `s.monitor.WaitForState(...)`.

8. `StateDuration()`
- `Session.StateDuration()` -> `s.monitor.StateDuration()`.

9. `SetExited()`
- Replace by direct `s.monitor.SetExited()` usage.

10. `HandleOutput()`
- `Session.HandleOutput()` -> `s.harness.HandleOutput()`.

11. `SignalInterrupt()`
- `Session.SignalInterrupt()` -> `s.harness.HandleInterrupt()`.

12. `Harness()`
- Remove; use `s.harness` directly.

13. `Metrics()`
- `Session.Metrics()` -> `s.monitor.MetricsSnapshot()`.

14. `OtelPort()`
- `Session.OtelPort()` via harness type assertion:
  - `type otelPorter interface { OtelPort() int }`

15. `ActivitySnapshot()`
- `Session.ActivitySnapshot()` -> `s.monitor.Activity()`.

16. `ActivityLog()`/`SetActivityLog()`
- Session owns logger directly (`s.activityLog`).

## Cross-Package Call Site Updates

Expected updates:

- `internal/session/session.go`
  - Replace all `s.Agent.*` calls.
- `internal/session/listener.go`
  - Hook events should call `Session.HandleHookEvent(...)`.
- `internal/session/daemon.go`
  - Read state/metrics/activity from session monitor/harness accessors.
- `internal/session/heartbeat.go`
  - Stop depending on `*agent.Agent`; use session or monitor-based callbacks.
- `internal/session/attach.go` and client wiring closures
  - Use session methods directly for interrupt/state/metrics.

## Deletions

After migration:

- Delete `internal/session/agent/agent.go`.
- Remove/replace tests currently tied to wrapper behavior:
  - `internal/session/agent/agent_test.go` (or migrate assertions to session/monitor tests).

## Safe Refactor Sequence

1. Add session-owned harness/monitor fields and equivalent session methods.
2. Switch all call sites from `s.Agent.*` to direct session methods/fields.
3. Remove `agent.Agent` use from daemon/listener/heartbeat paths.
4. Delete wrapper file + stale wrapper tests.
5. Run:
   - `go test ./internal/session/... ./internal/cmd`

## Non-Goals

- Changing harness event semantics.
- Changing monitor state machine semantics.
- Any behavior changes beyond removing the wrapper layer.
