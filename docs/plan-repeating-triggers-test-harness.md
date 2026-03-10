# Test Harness: Repeating Triggers

**Companion to**: `plan-repeating-triggers.md`
**Date**: 2026-03-09

## Property-Based Tests

### Invariant 1: Fire Count Never Exceeds MaxFirings

For any trigger with `MaxFirings > 0`, `FireCount` must never exceed `MaxFirings`. Generate random event sequences (100-1000 events) with random matching patterns and verify:
- `t.FireCount <= t.effectiveMaxFirings()` always holds
- Trigger is removed from the engine exactly when `FireCount == effectiveMaxFirings()`

### Invariant 2: Cooldown Gap Between Firings

For any trigger with `Cooldown > 0`, the time gap between any two consecutive firings must be `>= Cooldown`. Record all firing timestamps and verify pairwise.

### Invariant 3: No Firings After Expiry

For any trigger with `ExpiresAt` set, no firings occur after `ExpiresAt`. Inject a time source (or use a controllable clock) and verify that events arriving after the deadline produce zero firings.

### Invariant 4: Default Behavior Preserved

For any trigger with `MaxFirings == 0` (unset), the trigger fires exactly once and is removed. This is the backwards-compatibility invariant.

### Invariant 5: Monotonic FireCount

`FireCount` is monotonically non-decreasing and increments by exactly 1 per firing.

## Fault Injection / Chaos Tests

### Action Failure on Repeating Trigger

A repeating trigger (`MaxFirings=3`) where the action fails (runner returns error) on the 2nd firing. Verify:
- FireCount still increments (consumed on attempt)
- Trigger continues to fire on subsequent events
- Trigger is removed after 3rd firing regardless of action success/failure

### Concurrent Events During Cooldown

Spawn 10 goroutines each sending a matching event simultaneously to a trigger with `Cooldown=1s`. Verify:
- Exactly 1 firing occurs (the rest are cooldown-blocked)
- No data races (run with `-race`)
- FireCount == 1

### Expiry During Condition Evaluation

A trigger with `ExpiresAt` set to expire during a slow condition evaluation (condition sleeps for 2s, expiry is 1s from now). Verify:
- The trigger fires if condition started before expiry (condition was already in-flight)
- OR the trigger is reaped on the next `processEvent` call

### Clock Jump Forward

Simulate time jumping forward past a trigger's `ExpiresAt` (mock clock). Verify the trigger is reaped on the next event, not left dangling.

## Comparison Oracle Tests

### Before/After Behavior Equivalence

For all existing one-shot tests in `trigger_test.go`, verify that the same tests produce identical results when the trigger has `MaxFirings=0` (default/unset) — confirming the refactor preserves backwards compatibility.

Run the full existing trigger test suite twice: once on the old code, once on the new code, and diff the outputs. They must be identical.

## Deterministic Simulation Tests

### Simulated Event Sequence

Create a deterministic event generator with a seed that produces a known sequence of:
- `state_change` events with various states
- Random inter-event delays (1ms to 10s range)

Register 5 triggers with varying `MaxFirings`, `Cooldown`, and `ExpiresAt` values. Run the simulation and verify the exact set of firings matches a precomputed expected result. Re-run with same seed to confirm determinism.

### Cooldown Boundary Simulation

Generate events at exactly `Cooldown - 1ms`, `Cooldown`, and `Cooldown + 1ms` intervals. Verify:
- `Cooldown - 1ms`: blocked
- `Cooldown`: fires (boundary is inclusive of the duration)
- `Cooldown + 1ms`: fires

Use a controllable clock rather than real time to make this deterministic.

## Benchmarks and Performance Tests

### Benchmark: processEvent with N Triggers

Benchmark `processEvent` with 10, 100, and 1000 registered triggers (mix of repeating and one-shot). Target:
- 10 triggers: < 10µs per event
- 100 triggers: < 100µs per event
- 1000 triggers: < 1ms per event

These are the same targets as the existing trigger system; the repeating fields add only a timestamp comparison per trigger.

### Benchmark: Cooldown Check Overhead

Benchmark the cooldown check specifically (time comparison) to verify it adds negligible overhead vs the existing one-shot path.

### Benchmark: Expiry Reaping

Benchmark `processEvent` with 100 expired triggers + 10 active ones. Verify reaping 100 triggers doesn't cause latency spikes.

## Stress / Soak Tests

### Rapid-Fire Event Stream

Send 10,000 matching events per second to a trigger with `MaxFirings=-1` (unlimited) and `Cooldown=0`. Run for 60 seconds under `-race`. Verify:
- No data races
- FireCount matches expected (10k * 60 = 600k, minus any dropped by ActionRunner saturation)
- Memory usage stable (no leaks from trigger tracking)

### Long-Running Cooldown Trigger

Register a trigger with `Cooldown=100ms` and `MaxFirings=-1`. Send matching events every 50ms for 10 minutes. Verify:
- Fires approximately once per 100ms (within 20% tolerance)
- No accumulation of skipped events
- Memory stable

## Security Tests

Not applicable for this change. The trigger system executes shell commands that were already registered by authorized callers (role YAML or authenticated socket). The new fields don't introduce new execution vectors.

## Manual QA Plan

### QA 1: Visual Verification of trigger list

1. Start an agent with a role that has a repeating trigger (`max_firings: 5, cooldown: "30s"`)
2. Run `h2 trigger list <agent>`
3. Verify the output shows MAX_FIRINGS=5, FIRE_COUNT=0, COOLDOWN=30s
4. Wait for the trigger to fire twice
5. Run `h2 trigger list <agent>` again
6. Verify FIRE_COUNT=2

### QA 2: Cooldown Behavior Observation

1. Register a trigger with `--max-firings 0 --cooldown 1m --event state_change --state idle --message "idle reminder"`
2. Make the agent go idle
3. Observe the reminder fires
4. Make the agent active, then idle again within 30s
5. Verify no second reminder fires
6. Wait for 1 minute, make agent idle again
7. Verify reminder fires

### QA 3: Expiry Behavior

1. Register a trigger with `--max-firings 0 --expires-at "+2m" --event state_change --state idle --message "watch"`
2. Make agent go idle — observe trigger fires
3. Wait 2+ minutes
4. Run `h2 trigger list` — verify trigger no longer present
5. Make agent go idle — verify no message

### QA 4: Expects-Response Unchanged

1. Run `h2 send --expects-response agent "please respond"`
2. Verify trigger registered (one-shot)
3. Agent goes idle — reminder fires
4. Verify trigger removed after single firing
5. Agent goes idle again — no second reminder

## CI Tier Mapping

| Tier | Tests | When |
|------|-------|------|
| **Tier 1 (pre-commit)** | All unit tests in `trigger_test.go`, `trigger_test.go` in cmd/ | Every commit |
| **Tier 1 (pre-commit)** | Property-based invariant tests (Invariants 1-5) | Every commit |
| **Tier 2 (CI)** | Fault injection tests, concurrent event tests with `-race` | Every PR |
| **Tier 2 (CI)** | Comparison oracle tests (before/after equivalence) | First PR only |
| **Tier 3 (nightly)** | Benchmarks, stress/soak tests | Nightly CI |
| **Manual** | QA 1-4 | Before release |

## Exit Criteria

Implementation is considered done when:

1. All existing `trigger_test.go` tests pass unchanged (backwards compat)
2. All new unit tests listed in the plan pass
3. Property-based invariant tests pass for 1000 random sequences
4. `-race` flag passes on all tests
5. Benchmarks show < 5% regression vs baseline for one-shot trigger path
6. `make check` passes (gofmt, go vet, staticcheck)
7. Manual QA 1-4 completed successfully
