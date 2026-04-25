# Gateway Model Test Harness Plan

## Summary

This test harness verifies that the gateway migration changes the internal process model without regressing user-visible h2 behavior. The harness must prove there is one gateway process per `H2_DIR`, that agent and bridge runtimes are owned by the gateway, that all CLI commands route through `gateway.sock`, and that crash/restart behavior preserves session metadata and resume paths.

## Test Matrix

| Test area | Location | Runner | CI tier |
| --- | --- | --- | --- |
| Unit and protocol tests | `internal/gateway/*_test.go` | `make test` | PR |
| Session runtime component tests | `internal/gateway/session_runtime_test.go`, `internal/session/*_test.go` | `make test` | PR |
| Bridge routing component tests | `internal/gateway/bridge_runtime_test.go`, `internal/bridgeservice/*_test.go` | `make test` | PR |
| CLI integration tests | `tests/external/gateway_test.go` | `make test-external` | PR |
| Fault injection tests | `tests/external/gateway_fault_test.go` | `go test ./tests/external -run GatewayFault` | Nightly/on-demand |
| Soak tests | `tests/external/gateway_soak_test.go` | `go test ./tests/external -run GatewaySoak -count=1` | Nightly/on-demand |
| Benchmarks | `internal/gateway/bench_test.go` | `go test -bench Gateway ./internal/gateway` | On-demand |

All tests must use fake h2 homes. Test setup must set `H2_DIR` to a temp directory and reset config and socketdir caches.

## Property-Based Tests

### Runtime registry invariants

Location: `internal/gateway/manager_property_test.go`

Runner: `make test`

CI: PR

Use Go's `testing/quick` or a deterministic generated operation sequence. Generate operations:

| Operation | Inputs |
| --- | --- |
| `start_session` | agent name, pod, role metadata |
| `stop_session` | existing or missing agent name |
| `start_bridge` | bridge name, optional concierge |
| `stop_bridge` | existing or missing bridge name |
| `send_session` | target agent name, priority |
| `set_concierge` | bridge name, agent name |
| `remove_concierge` | bridge name |

Properties:

1. No two live sessions share the same agent name.
2. No two live bridges share the same bridge name.
3. `list_runtime` equals the manager's internal registry after every operation.
4. A stopped session cannot receive sends or attach requests.
5. A bridge concierge can reference a stopped agent, but inbound delivery must return a structured unavailable error and must not panic.
6. Failed starts leave no partially registered runtime.

### Protocol round-trip

Location: `internal/gateway/protocol_test.go`

Runner: `make test`

CI: PR

Generate gateway requests and responses with optional fields and verify JSON round-trip preserves semantic equality. Include attach specs, trigger specs, schedule specs, bridge specs, and error responses.

## Fault Injection and Chaos Tests

### Gateway socket startup race

Location: `tests/external/gateway_fault_test.go`

Runner: `go test ./tests/external -run GatewayFaultSocketRace`

CI: Nightly/on-demand initially; promote to PR if stable under 5 seconds.

Test:

1. Start 20 concurrent `h2 run --detach --command <fake-agent>` commands against the same fake `H2_DIR`.
2. Inject slow gateway startup by setting `H2_TEST_GATEWAY_START_DELAY`.
3. Assert exactly one gateway process is live.
4. Assert every successfully started agent is visible in `h2 list`.
5. Assert stale or duplicate `gateway.sock` files do not remain after stop.

### Child crash containment

Location: `tests/external/gateway_fault_test.go`

Runner: `go test ./tests/external -run GatewayFaultChildCrash`

CI: PR if runtime stays below 10 seconds.

Test:

1. Start two generic fake agents.
2. Kill one child process.
3. Verify gateway stays live.
4. Verify the crashed session transitions to exited.
5. Verify the other session still accepts `h2 send` and `h2 attach`.

### Gateway hard crash recovery

Location: `tests/external/gateway_fault_test.go`

Runner: `go test ./tests/external -run GatewayFaultHardCrash`

CI: Nightly/on-demand.

Test:

1. Start a resumable fake harness session.
2. Kill the gateway with SIGKILL.
3. Verify child agent processes are gone.
4. Run `h2 run <name> --resume --detach`.
5. Verify stale `gateway.sock` cleanup, metadata parse, and resumed runtime.

### Bridge provider failures

Location: `internal/gateway/bridge_runtime_test.go`

Runner: `make test`

CI: PR

Use fake bridge providers that fail send, fail start, block on receive, and return slow typing indicators. Verify:

1. Provider start failure aborts bridge registration.
2. Provider send failure returns a structured bridge error but does not poison the bridge runtime.
3. Slow typing indicators stop on bridge shutdown.
4. Inbound messages during gateway shutdown are rejected deterministically after bridge receivers stop.

## Comparison Oracle Tests

### Legacy behavior oracle

Location: `tests/external/gateway_oracle_test.go`

Runner: `go test ./tests/external -run GatewayOracle`

CI: On-demand until the old daemon path is deleted; then archive expected fixtures.

Mechanism:

1. Build the current pre-gateway binary as `h2-legacy` before migration milestones where practical.
2. Run scripted scenarios against `h2-legacy` and `h2` using the same fake harness commands.
3. Compare normalized CLI output for:
   - `run --detach`
   - `list`
   - `status`
   - `send`
   - `trigger add/list/remove`
   - `schedule add/list/remove`
   - `bridge create/status/stop` with fake providers

Normalization removes PIDs, durations, generated UUIDs, timestamps, and generated message IDs.

## Deterministic Simulation Tests

### Gateway manager model test

Location: `internal/gateway/simulation_test.go`

Runner: `make test`

CI: PR

Implement a fake clock, fake process launcher, fake session runtime, and fake bridge runtime. Run deterministic scripts:

| Script | Assertions |
| --- | --- |
| `pod_launch_then_stop` | Agents start in pod order, snapshots preserve `PodIndex`, stop tears down all children. |
| `bridge_concierge_switch` | Inbound routing follows concierge, then last sender, then first available agent as currently documented. |
| `expects_response_failure` | Trigger registration and send behave atomically under injected failures. |
| `shutdown_order` | Bridge receivers stop before sessions; sessions stop before gateway socket removal. |

## Benchmarks and Performance Tests

Location: `internal/gateway/bench_test.go`

Runner: `go test -bench Gateway ./internal/gateway`

CI: On-demand.

Benchmarks:

| Benchmark | Target |
| --- | --- |
| `BenchmarkGatewaySendDispatch` | In-process bridge-to-session dispatch is at least 30% faster than old socket bridge-to-agent dispatch under the same fake session workload. |
| `BenchmarkGatewayList100Sessions` | Runtime snapshot for 100 sessions does not dial sockets and completes under 10 ms on a developer laptop. |
| `BenchmarkAttachFrameProxy` | Gateway attach dispatch adds no extra per-frame allocation after stream handoff. |

Performance artifacts should be written to `coverage.out`-style ignored files only when explicitly requested, not committed.

## Stress and Soak Tests

### Multi-agent soak

Location: `tests/external/gateway_soak_test.go`

Runner: `go test ./tests/external -run GatewaySoakMultiAgent -count=1`

CI: Nightly/on-demand.

Test:

1. Start one foreground gateway.
2. Start 25 fake generic agents.
3. Send 1,000 messages across agents with mixed priorities.
4. Attach and detach repeatedly from five agents.
5. Start and stop a fake bridge provider repeatedly.
6. Stop all sessions and gateway.

Assertions:

1. No goroutine leak above an allowed threshold in gateway test builds.
2. No leaked child processes.
3. No stale socket files.
4. Every successful send has a message ID and observable queue lifecycle.

### Long-poll bridge soak

Location: `tests/external/gateway_soak_test.go`

Runner: `go test ./tests/external -run GatewaySoakBridge -count=1`

CI: Nightly/on-demand.

Use an HTTP test server emulating Telegram long polling with transient failures, delayed responses, and message bursts. Verify gateway bridge runtime remains responsive to CLI stop/status while polling is active.

## Security Tests

### Socket permissions

Location: `internal/gateway/listener_test.go`

Runner: `make test`

CI: PR

Verify `gateway.sock` parent directory is created with `0700`, stale socket cleanup does not follow unsafe symlink replacements outside the h2 socket directory, and foreground/background startup refuses to use a socket path owned by a different effective user where the OS exposes ownership.

### Hook routing authorization

Location: `internal/gateway/listener_test.go`

Runner: `make test`

CI: PR

Verify `hook_event` requests must identify a live session by `agent_name` or `session_dir`, and that mismatched `session_id` payloads continue to be filtered by harness event handlers.

## Manual QA Plan

Location: `qa/plans/gateway-manual.md`

Runner: Manual before release.

CI: On-demand only.

Manual checks:

1. Run `h2 gateway run` in foreground and launch Claude and Codex agents from another terminal. Confirm process tree shows gateway as parent of both agent app processes.
2. Use `h2 attach` from two terminals against one agent. Exercise passthrough lock, resize, scroll, relaunch, and quit.
3. Configure a real Telegram bridge and verify unaddressed messages, `agent: message` prefixes, reply-to routing, typing indicator, concierge switch, and shutdown messages.
4. Kill the foreground gateway with Ctrl+C and verify shutdown leaves the terminal clean, removes `gateway.sock`, and stops child agents.
5. Start through auto-background mode from inside an agent session and verify `CLAUDECODE` or similar parent-agent environment markers do not leak into new child agents.

## CI Tier Mapping

| Target | Includes | Required before merge |
| --- | --- | --- |
| `make check` | Formatting, vet, staticcheck | Yes |
| `make test` | Gateway unit, manager simulation, session runtime, bridge runtime, CLI command tests | Yes |
| `make test-external` | CLI-level gateway smoke tests with fake harnesses | Yes |
| Nightly/on-demand external | hard crash, long soak, race-heavy startup, Telegram emulator soak | Before release, and after changes to gateway manager, bridge runtime, or process supervision |
| Benchmarks | Dispatch/list/attach benchmarks | Before release and after performance-sensitive gateway changes |

## Exit Criteria

Implementation is complete only when:

1. No normal command path forks `_daemon` or `_bridge-service`.
2. `h2 list` and process inspection show one gateway process plus direct child agent app processes.
3. No per-agent or per-bridge sockets are created during normal operation.
4. `make check`, `make test`, and `make test-external` pass.
5. Gateway fault tests pass at least once before release.
6. Manual QA in `qa/plans/gateway-manual.md` is completed and recorded.
7. Documentation updates describe `h2 gateway run` for supervisors and preserve existing user-facing run/bridge instructions.
