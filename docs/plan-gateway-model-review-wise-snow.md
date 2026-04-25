# Gateway Model Plan Review — wise-snow

Reviewer: wise-snow
Plan: docs/plan-gateway-model.md (5ecfa32)
Test harness: docs/plan-gateway-model-test-harness.md (f742da8)

## Findings

### P0 — Must fix before implementation

#### P0-1: Attach stream ownership transfer is underspecified

The plan says `SessionRuntime.Attach` "reuses the existing framed attach protocol over the gateway connection after the gateway has selected the target session." The current `handleAttach` in `internal/session/attach.go` takes ownership of the `net.Conn` for the entire duration of the attach session — it never returns until the client disconnects. It runs `readClientInput` in a blocking loop, manages client lifecycle (add/remove from session, mouse enable/disable, passthrough ownership, resize coordination across all clients).

The gateway listener dispatches requests via `ServeConn`. For attach, the gateway must hand off the raw connection to the `SessionRuntime` and **never touch it again** — but the plan doesn't describe this handoff. Questions the plan must answer:

1. Does `ServeConn` return immediately after dispatching attach, leaving the goroutine alive with the conn? Or does it block until detach?
2. If the gateway connection goroutine blocks on attach, what happens to gateway shutdown? The conn goroutine won't see shutdown signals unless the attach loop cooperates.
3. Multi-client attach resize coordination currently calls `s.VT.Resize()` and re-renders all clients. This works today because all clients share the same `Session` in the same process. In the gateway model this is still true (session is in-process), but the plan should explicitly confirm that attach connections are **not** proxied — they are direct `net.Conn` references passed through.

**Recommendation:** Add an explicit "Attach Handoff" subsection to the Runtime Model describing: (a) gateway dispatches attach by calling `SessionRuntime.Attach(ctx, conn, opts)` which blocks until client disconnects, (b) the gateway goroutine serving that conn is consumed for the attach lifetime, (c) gateway shutdown must cancel the attach context to unblock, (d) no proxying occurs — the conn is passed through directly.

#### P0-2: Hook delivery address resolution is incomplete

The plan's CLI changes table says `h2 handle-hook` uses `H2_ACTOR` or `H2_SESSION_DIR` to address `hook_event` through gateway. But the current `handle_hook.go` implementation does more than just forward the hook event — it also:

1. Loads permission review config and role from the session's `RuntimeConfig`
2. Runs DCG guard evaluation or AI reviewer for `PreToolUse` events
3. Sends `permission_decision` hook events back to the agent

These steps currently read `RuntimeConfig` from disk using `H2_SESSION_DIR`. In the gateway model, hook commands still run as child processes of Claude Code (which is a child of the gateway), so `H2_SESSION_DIR` still works for reading config from disk. But the socket dial changes — `handle_hook.go` currently dials the agent socket directly at `sendHookEvent()`. The plan needs to specify:

1. Does `handle-hook` dial `gateway.sock` instead of `agent.*.sock`?
2. If yes, how does it address the correct session? By `H2_ACTOR` name in the gateway request?
3. The `sendHookEvent` call is best-effort (ignores errors). Does this remain true through the gateway?

**Recommendation:** Add a "Hook Delivery" paragraph to the CLI Changes section specifying that `handle-hook` dials `gateway.sock`, sends `hook_event` with `agent_name` from `H2_ACTOR`, and that the permission review path (DCG/AI reviewer) remains local to the hook process and does not route through the gateway.

#### P0-3: Expects-response trigger + send is not truly atomic in the plan

The plan's URP section claims "single-transaction expects-response delivery" where "gateway `send_session` registers the reminder trigger and message enqueue under one session operation." But the current bridge `sendToAgent` in `internal/bridgeservice/service.go:337-383` registers the trigger first via a separate socket call (`registerExpectsResponseTrigger`), then sends the message in a second call. If the send fails, it calls `removeTriggerBestEffort`.

The plan says the gateway fixes this, but the `SessionRuntime.Send(SendSpec)` signature doesn't show how trigger registration is folded in. The `SendSpec` needs to carry the trigger definition, and `SessionRuntime.Send` needs to atomically add the trigger and enqueue the message under one lock acquisition. If the message enqueue fails after trigger registration, the trigger must be removed before returning.

**Recommendation:** Add `ERTrigger *TriggerSpec` to `SendSpec` in the protocol section. Document that `SessionRuntime.Send` registers the trigger and enqueues the message in a single critical section, rolling back the trigger on enqueue failure. This is the concrete mechanism the URP claim requires.

### P1 — Should fix before implementation

#### P1-1: Bridge `sendToAgent` currently dials agent sockets — migration path unclear

The bridge `sendToAgent` (`service.go:337`) dials `agent.*.sock` directly. The plan says `BridgeRouter.SendToAgent` replaces this. But `bridgeservice.Service` also dials agent sockets in:

- `handleSetConcierge` (line 251): probes agent socket for liveness
- `runTypingLoop`: queries agent state via socket (through `queryAgentStateFn`)
- `resolveDefaultTarget` → `firstAvailableAgent`: lists agent sockets to find available targets

All of these need to go through `BridgeRouter`. The `BridgeRouter` interface in the plan has `SendToAgent`, `FirstAvailableAgent`, and `AgentState` — this covers it, but the plan should explicitly enumerate these four call sites as the migration points in the bridge refactor (Phase 4), not just describe the interface abstractly.

**Recommendation:** In Phase 4, list the concrete call sites in `bridgeservice.Service` that currently dial agent sockets and map each to the `BridgeRouter` method that replaces it.

#### P1-2: Gateway shutdown vs long-lived attach connections

The plan says "default graceful stop sends stop to bridge receivers, then stops agent children." But active attach connections block gateway goroutines (see P0-1). Shutdown must:

1. Stop accepting new connections on `gateway.sock`
2. Stop bridge receivers (prevents new inbound work)
3. Cancel active attach contexts (disconnects all attached terminals)
4. Stop sessions (kills child PTYs)
5. Remove `gateway.sock`

Steps 3 and 4 interact: if you stop a session while someone is attached, the attach should get a clean disconnect, not a broken pipe. The current daemon handles this because `lifecycleLoop` exit causes the listener to close, which breaks all attach connections. In the gateway model, session stop needs to explicitly close attach connections before or as part of stopping the child.

**Recommendation:** Add a shutdown sequence diagram or ordered list to the Gateway Lifecycle section covering attach connection draining.

#### P1-3: `h2 list` without a running gateway

The plan's open questions say `h2 list` should auto-start the gateway only when needed for live runtime inspection, and can show stopped sessions by reading metadata directly. But the CLI Changes table says `h2 list` calls `list_runtime`. These are contradictory for the case where no gateway is running and the user runs `h2 list`.

Current behavior: `h2 list` enumerates socket files and probes them. No daemon needed for listing — it's purely discovery. If the gateway model requires a running gateway for `h2 list`, that's a UX regression for users who just want to see what's around.

**Recommendation:** Decide and document one of: (a) `h2 list` always auto-starts the gateway, (b) `h2 list` has a fast path that reads metadata files without a gateway and only dials the gateway if it's already running, (c) `h2 list` always auto-starts. Option (b) is the least surprising.

#### P1-4: No rollback or phased cutover strategy

The migration plan has 5 phases but no description of how to run old and new paths simultaneously during development, or how to roll back if a phase introduces regressions. For a change this large, each phase should have:

1. A feature flag or build tag that enables the new path
2. A way to fall back to the old path if the new path fails
3. Clear criteria for when the old path can be deleted

The plan says "keep hidden `_daemon` and `_bridge-service` only until their call sites are fully deleted" but doesn't describe how both paths coexist during the transition.

**Recommendation:** Add a "Cutover Strategy" subsection to the Migration Plan describing how old and new paths coexist during development. At minimum: Phase 1-2 should support `H2_GATEWAY=0` env var to bypass the gateway and use legacy daemons, removed after Phase 3 passes all tests.

#### P1-5: `session.Session` concurrency assumptions change

Currently `session.Session` is owned by exactly one `Daemon` goroutine tree. In the gateway model, `SessionRuntime` wraps `Session`, but the gateway dispatches RPCs to it from arbitrary goroutine contexts (any CLI connection can trigger `Send`, `Attach`, `Stop`, etc. concurrently). The plan says "a `SessionRuntime` method never calls back into manager while holding `Session` internal locks" — but the more important question is whether `Session` methods are safe to call from multiple goroutines.

Looking at the code: `Session.Queue` has its own mutex, `Session.VT` has `VT.Mu`, the monitor has internal locking. But `Session.Quit`, `Session.relaunchCh`, `Session.quitCh`, `Session.relaunchWithSetup`, `Session.relaunchIsRotate`, `Session.relaunchOldProfile` are all accessed without locks in the current listener handlers (they're safe today because the daemon listener serializes nothing — each handler runs in its own goroutine, but the fields are simple flags/channels).

In the gateway model these same fields are accessed from gateway dispatch goroutines. The channel sends to `quitCh`/`relaunchCh` use non-blocking select, which is fine. But `Quit` is a bare `bool` read/written from multiple goroutines without synchronization. This is a data race.

**Recommendation:** Document in the Session Runtime section that `Session.Quit` must become an `atomic.Bool` (or be guarded by a mutex), and audit other `Session` fields accessed from listener handlers for the same pattern. This should be part of Phase 2 acceptance criteria.

### P2 — Worth addressing

#### P2-1: Gateway protocol is JSON request/response — consider versioning

The plan defines a `Request` struct with a `Type` string discriminator. No version field is included. If the gateway binary and CLI binary can ever be different versions (e.g., gateway is running from an older build), unknown request types will get "unknown type" errors. The current per-agent protocol has the same limitation, but the blast radius was one agent. A gateway protocol mismatch affects all agents.

**Recommendation:** Add a `Version` field to `Request` (or a protocol version in the health handshake). Gateway should reject requests from incompatible versions with a clear error message suggesting restart.

#### P2-2: Structured lifecycle journal schema not specified

The URP section commits to `gateway-events.jsonl` with events for start, stop, child exit, etc. But no schema is defined. Without a schema, the test (`state_test.go`) can only verify "some JSON was written," not that the events are useful.

**Recommendation:** Define the event schema (at minimum: `timestamp`, `event_type`, `agent_name`/`bridge_name`, `detail`) in the plan so tests can validate structure.

#### P2-3: `EnsureRunning` race between probe and fork

`EnsureRunning` probes `gateway.sock`, and if missing, re-execs `_gateway --background`. If two CLI commands race, both may find no socket and both fork a gateway. The plan mentions "waits for `gateway.sock` readiness with an explicit health check" but doesn't describe the mutual exclusion mechanism.

**Recommendation:** Use a filesystem lock (`flock` on `<H2_DIR>/gateway.lock`) in `EnsureRunning` to serialize gateway startup attempts. The gateway itself should also acquire this lock before binding the socket. Describe this in the Auto-start section.

#### P2-4: Test harness comparison oracle may be impractical

The test harness proposes building `h2-legacy` before migration milestones and comparing CLI output. In practice, maintaining a second binary build through 5 migration phases is significant overhead, and normalization of PIDs/timestamps/UUIDs is fragile.

**Recommendation:** Instead of (or in addition to) a comparison oracle, define golden-file fixtures for key CLI outputs and validate the gateway version against those fixtures. This is more maintainable and doesn't require building two binaries.

### P3 — Minor / style

#### P3-1: Connected Components table lists `AttachConn` but the SessionRuntime struct shows `Attach`

Minor naming inconsistency between the Connected Components section (`AttachConn(ctx, conn, AttachOpts)`) and the Runtime Model section (`Attach(ctx context.Context, conn net.Conn, opts AttachOpts)`). Pick one name.

#### P3-2: No explicit signal handling for foreground gateway

The plan says `h2 gateway run` is intended for launchd/systemd/tmux. These supervisors send SIGTERM for graceful stop. The plan should mention that the foreground gateway installs a SIGTERM handler that triggers `Shutdown`.

#### P3-3: Benchmark targets are relative, not absolute

"Median in-process dispatch below current socket-based bridge-to-agent dispatch by at least 30%" requires running the old code path in the same benchmark. After the old path is deleted, this benchmark target becomes unmeasurable. Define an absolute target (e.g., "median dispatch under 100μs") that survives the migration.
