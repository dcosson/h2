# Review: plan-repeating-triggers (reviewer-leaf)

- Source doc: `docs/plan-repeating-triggers.md`
- Reviewed commit: 0d0a9d0
- Reviewer: reviewer-leaf

## Findings

### P1 - Race condition: cooldown and expiry checks read trigger fields without lock

**Problem**

In the proposed `evalAndFire` (lines 171-227 of the plan), the expiry check (`now.After(t.ExpiresAt)`) and cooldown check (`now.Sub(t.LastFiredAt) < t.Cooldown`) read `t.LastFiredAt` **outside the mutex**. Since `LastFiredAt` is written under the lock later in the same function (line 210: `t.LastFiredAt = now`), and `processEvent` can dispatch multiple goroutines calling `evalAndFire` concurrently for triggers matching different events, there is a data race on `t.LastFiredAt`.

This is not just theoretical -- a repeating trigger with `Event: "state_change"` and no state filter would match any state change event. If two state change events arrive in rapid succession, two goroutines could both read `LastFiredAt` before either writes it, causing the cooldown to be bypassed.

The existing code avoids this because it deletes the trigger under the lock before running the action (consume-on-attempt). The new code keeps the trigger alive, so concurrent `evalAndFire` calls on the same trigger become possible.

**Required fix**

Either:
1. Move the cooldown and expiry checks inside the lock acquisition that already exists for the fire-count update, or
2. Read `LastFiredAt` under the lock in a short critical section before evaluating the condition, then re-check under the lock after condition evaluation (double-check pattern).

Option 1 is simpler and sufficient since the cooldown check is a trivial time comparison.

---

### P1 - `handleTriggerAdd` does not call the error-returning `triggerFromSpec`

**Problem**

The plan specifies that `triggerFromSpec` will be changed to return `(*automation.Trigger, error)` to handle parse errors for `ExpiresAt` and `Cooldown` (lines 262-289). However, the existing `handleTriggerAdd` in `listener.go` (line 204) calls `triggerFromSpec` as a single-return function: `t := triggerFromSpec(req.Trigger)`. The plan does not show the updated `handleTriggerAdd` code that handles the error return.

If the caller ignores the error (or the plan forgets to update the call site), malformed `ExpiresAt` or `Cooldown` values will silently produce zero-value fields, making the trigger behave as one-shot with no expiry -- a silent correctness bug.

**Required fix**

Add the updated `handleTriggerAdd` code to the plan showing the error being checked and returned to the client as a socket response error. This is a mechanical change but should be explicitly documented since the current code uses a single-return signature.

---

### P2 - `List()` returns pointers to live triggers, enabling unsynchronized reads

**Problem**

The existing `List()` method (trigger.go line 78) returns `[]*Trigger` -- pointers to the same `Trigger` structs stored in the map. The plan adds mutable runtime fields (`FireCount`, `LastFiredAt`) that are updated under the engine's mutex. But callers of `List()` (e.g., `handleTriggerList` in listener.go, and `specFromTrigger`) read these fields without holding the lock after `List()` returns.

Today this is harmless because triggers are immutable after creation (they only get deleted). With repeating triggers, `FireCount` and `LastFiredAt` are actively mutated by `evalAndFire` while `List()` callers may be reading them.

**Required fix**

`List()` should return copies of the trigger structs (value copies, not pointer copies). Change to:
```go
func (te *TriggerEngine) List() []Trigger {
    te.mu.Lock()
    defer te.mu.Unlock()
    result := make([]Trigger, 0, len(te.triggers))
    for _, t := range te.triggers {
        result = append(result, *t)
    }
    return result
}
```

Alternatively, snapshot `FireCount` and `LastFiredAt` under the lock within `List()` if changing the return type is too disruptive.

---

### P2 - Manual QA 2 and QA 3 use `--max-firings 0` for unlimited, contradicting the plan's sentinel values

**Problem**

The test harness doc (Manual QA Plan, QA 2 line 136 and QA 3 line 145) uses `--max-firings 0` to mean "unlimited". But the plan explicitly states that `MaxFirings=0` means "default (one-shot)" and `-1` means unlimited (lines 159-162 of the plan). This contradicts the sentinel value convention and would cause the QA tests to create one-shot triggers instead of unlimited ones.

**Required fix**

Change `--max-firings 0` to `--max-firings -1` in Manual QA 2 and QA 3 of the test harness doc.

---

### P2 - No validation that `ExpiresAt` is in the future when creating a trigger

**Problem**

The `triggerFromSpec` conversion (plan lines 271-276) parses `ExpiresAt` as an RFC 3339 timestamp but does not validate that it is in the future. A user could pass `--expires-at "2020-01-01T00:00:00Z"` and create a trigger that is already expired. The trigger would then never fire and would only be reaped on the next unrelated event via `processEvent`, which could be confusing.

For the relative format (`+1h`), this is not an issue since it always resolves to a future time. But the absolute format has no guard.

**Required fix**

Add a validation check in `triggerFromSpec`: if `ExpiresAt` is set and is in the past, return an error. This gives immediate feedback to CLI users rather than silently creating a dead trigger.

---

### P2 - Clock interface mentioned in URP section but not specified in the plan

**Problem**

The URP section (line 433) mentions an injectable `Clock` interface for deterministic testing, and the Package Structure section (line 365) mentions it in trigger.go. However, the plan never defines the interface, shows how `TriggerEngine` would accept it (constructor change), or shows how `evalAndFire` and `processEvent` would use `te.clock.Now()` instead of `time.Now()`.

The R1 disposition (item 5) says "Added Clock interface to URP section and package structure" but the actual interface definition and integration into the proposed code is missing. The `evalAndFire` code in the plan still uses `time.Now()` directly (line 173).

**Required fix**

Add the Clock interface definition and show the constructor accepting it. Update the `evalAndFire` and `processEvent` code snippets to use `te.clock.Now()` instead of `time.Now()`. This is important because it is load-bearing for the entire test harness (which depends on controllable time).

---

### P3 - `resolveExpiresAt` uses `time.Now()` directly, not injectable clock

**Problem**

`resolveExpiresAt` (plan lines 315-329) calls `time.Now()` directly for relative timestamps. If the daemon uses an injectable clock (per URP), this function should also accept a `now` parameter or use the same clock, otherwise relative ExpiresAt values resolved at startup will use wall clock while trigger lifecycle uses the injectable clock, creating inconsistency in tests.

**Required fix**

Change signature to `resolveExpiresAt(raw string, now time.Time) (time.Time, error)` and have callers pass in their clock's current time.

---

### P3 - Cooldown boundary condition: plan says `>=` but code uses strict `<`

**Problem**

The test harness "Cooldown Boundary Simulation" section (lines 76-79) says that at exactly `Cooldown` duration, the trigger should fire ("boundary is inclusive of the duration"). The plan's `evalAndFire` code (line 185) uses `now.Sub(t.LastFiredAt) < t.Cooldown`, which means at exactly `Cooldown` the condition is false (elapsed == cooldown is NOT less than cooldown), so the trigger fires. This is consistent.

However, this boundary behavior should be documented in the plan's data model section as an explicit design decision: "Cooldown is the minimum gap; a trigger is eligible to fire again at exactly Cooldown elapsed." This prevents an implementer from accidentally using `<=` thinking they need to wait strictly longer than the cooldown.

**Required fix**

Add a one-line note in the Cooldown field documentation: "The trigger becomes eligible to fire again once `time.Since(LastFiredAt) >= Cooldown`."

---

## Summary

7 findings: 0 P0, 2 P1, 4 P2, 1 P3 (note: 1 additional P3 is informational only, bringing the display count to 2 P3)

The two P1 findings are real correctness issues:
1. The data race on `LastFiredAt` during concurrent `evalAndFire` calls is a genuine race condition that will be caught by `-race` but could cause cooldown bypass in production.
2. The `handleTriggerAdd` call site not handling the new error return is a gap that could silently swallow parse errors.

**Verdict**: Approved with revisions
