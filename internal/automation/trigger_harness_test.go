package automation

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"sync"
	"testing"
	"time"

	"h2/internal/session/agent/monitor"
	"h2/internal/session/message"
)

// discardLogger returns a logger that discards all output, for benchmarks.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// directProcessEvent calls processEvent directly for fast testing (no goroutine/sleep).
func directProcessEvent(te *TriggerEngine, evt monitor.AgentEvent) {
	te.processEvent(context.Background(), evt)
}

// =============================================================================
// Property-Based Tests
// =============================================================================

// TestPropertyInvariant1_FireCountNeverExceedsMaxFirings generates random event
// sequences and verifies FireCount never exceeds effectiveMaxFirings.
func TestPropertyInvariant1_FireCountNeverExceedsMaxFirings(t *testing.T) {
	for seed := int64(0); seed < 1000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		maxF := rng.Intn(10) + 1 // 1..10
		numEvents := 100 + rng.Intn(900)

		clock := newMockClock(time.Now())
		te, _ := newTestTriggerEngineWithClock(clock)
		te.Add(&Trigger{
			ID:         "prop1",
			Event:      "state_change",
			MaxFirings: maxF,
			Action:     Action{Message: "test"},
		})

		for i := 0; i < numEvents; i++ {
			directProcessEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
			triggers := te.List()
			for _, tr := range triggers {
				if tr.ID == "prop1" && tr.FireCount > maxF {
					t.Fatalf("seed=%d: FireCount %d > MaxFirings %d", seed, tr.FireCount, maxF)
				}
			}
		}

		// Trigger should be gone after maxF firings.
		if len(te.List()) != 0 {
			t.Fatalf("seed=%d: trigger should be removed after %d firings", seed, maxF)
		}
	}
}

// TestPropertyInvariant2_CooldownGapBetweenFirings records firing timestamps
// and verifies the gap is always >= Cooldown.
func TestPropertyInvariant2_CooldownGapBetweenFirings(t *testing.T) {
	for seed := int64(0); seed < 1000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		cooldown := time.Duration(rng.Intn(100)+10) * time.Millisecond
		numEvents := 50 + rng.Intn(100)

		clock := newMockClock(time.Now())
		te, enq := newTestTriggerEngineWithClock(clock)
		te.Add(&Trigger{
			ID:         "prop2",
			Event:      "state_change",
			MaxFirings: -1,
			Cooldown:   cooldown,
			Action:     Action{Message: "test"},
		})

		var fireTimes []time.Time
		for i := 0; i < numEvents; i++ {
			prevCount := len(enq.getMessages())
			directProcessEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
			if len(enq.getMessages()) > prevCount {
				fireTimes = append(fireTimes, clock.Now())
			}
			// Advance by random amount: 0-200ms
			clock.Advance(time.Duration(rng.Intn(200)) * time.Millisecond)
		}

		for i := 1; i < len(fireTimes); i++ {
			gap := fireTimes[i].Sub(fireTimes[i-1])
			if gap < cooldown {
				t.Fatalf("seed=%d: gap between firing %d and %d is %v, less than cooldown %v",
					seed, i-1, i, gap, cooldown)
			}
		}
	}
}

// TestPropertyInvariant3_NoFiringsAfterExpiry verifies no firings occur after ExpiresAt.
func TestPropertyInvariant3_NoFiringsAfterExpiry(t *testing.T) {
	for seed := int64(0); seed < 1000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		expiryOffset := time.Duration(rng.Intn(500)+100) * time.Millisecond

		baseTime := time.Now()
		clock := newMockClock(baseTime)
		te, enq := newTestTriggerEngineWithClock(clock)
		te.Add(&Trigger{
			ID:         "prop3",
			Event:      "state_change",
			MaxFirings: -1,
			ExpiresAt:  baseTime.Add(expiryOffset),
			Action:     Action{Message: "test"},
		})

		for i := 0; i < 100; i++ {
			directProcessEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
			clock.Advance(time.Duration(rng.Intn(50)) * time.Millisecond)
		}

		// Record count before expiry events.
		preExpiryCount := len(enq.getMessages())

		// Jump past expiry and send more events.
		clock.Set(baseTime.Add(expiryOffset + 1*time.Second))
		for i := 0; i < 50; i++ {
			directProcessEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
		}

		if len(enq.getMessages()) > preExpiryCount {
			t.Fatalf("seed=%d: %d firings occurred after expiry",
				seed, len(enq.getMessages())-preExpiryCount)
		}
	}
}

// TestPropertyInvariant4_DefaultBehaviorPreserved verifies MaxFirings=0 triggers
// fire exactly once and are removed.
func TestPropertyInvariant4_DefaultBehaviorPreserved(t *testing.T) {
	for seed := int64(0); seed < 1000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		numEvents := 10 + rng.Intn(50)

		clock := newMockClock(time.Now())
		te, enq := newTestTriggerEngineWithClock(clock)
		te.Add(&Trigger{
			ID:         "prop4",
			Event:      "state_change",
			MaxFirings: 0, // default one-shot
			Action:     Action{Message: "test"},
		})

		for i := 0; i < numEvents; i++ {
			directProcessEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
		}

		if len(enq.getMessages()) != 1 {
			t.Fatalf("seed=%d: expected exactly 1 firing, got %d", seed, len(enq.getMessages()))
		}
		if len(te.List()) != 0 {
			t.Fatalf("seed=%d: trigger should be removed after one firing", seed)
		}
	}
}

// TestPropertyInvariant5_MonotonicFireCount verifies FireCount is monotonically
// non-decreasing and increments by exactly 1.
func TestPropertyInvariant5_MonotonicFireCount(t *testing.T) {
	for seed := int64(0); seed < 1000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		maxF := rng.Intn(10) + 3

		clock := newMockClock(time.Now())
		te, _ := newTestTriggerEngineWithClock(clock)
		te.Add(&Trigger{
			ID:         "prop5",
			Event:      "state_change",
			MaxFirings: maxF,
			Action:     Action{Message: "test"},
		})

		prevCount := 0
		for i := 0; i < maxF+5; i++ {
			directProcessEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
			triggers := te.List()
			for _, tr := range triggers {
				if tr.ID == "prop5" {
					if tr.FireCount < prevCount {
						t.Fatalf("seed=%d: FireCount decreased from %d to %d", seed, prevCount, tr.FireCount)
					}
					if tr.FireCount > prevCount+1 {
						t.Fatalf("seed=%d: FireCount jumped from %d to %d", seed, prevCount, tr.FireCount)
					}
					prevCount = tr.FireCount
				}
			}
		}
	}
}

// =============================================================================
// Fault Injection Tests
// =============================================================================

// failingEnqueuer fails on the Nth call.
type failingEnqueuer struct {
	mu        sync.Mutex
	callCount int
	failOnN   int
	messages  []enqueuedMsg
}

func (f *failingEnqueuer) EnqueueMessage(from, body, header string, priority message.Priority) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++
	if f.callCount == f.failOnN {
		return "", fmt.Errorf("simulated failure on call %d", f.callCount)
	}
	f.messages = append(f.messages, enqueuedMsg{From: from, Body: body, Priority: priority})
	return "test-id", nil
}

func (f *failingEnqueuer) getMessages() []enqueuedMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]enqueuedMsg, len(f.messages))
	copy(cp, f.messages)
	return cp
}

// TestFault_ActionFailureOnRepeatingTrigger verifies that action failure doesn't
// prevent subsequent firings and that FireCount still increments.
func TestFault_ActionFailureOnRepeatingTrigger(t *testing.T) {
	failEnq := &failingEnqueuer{failOnN: 2} // fail on 2nd firing
	runner := NewActionRunner(failEnq, nil, nil)
	clock := newMockClock(time.Now())
	te := NewTriggerEngine(runner, nil)
	te.SetClock(clock)

	te.Add(&Trigger{
		ID:         "fault1",
		Event:      "state_change",
		State:      "idle",
		MaxFirings: 3,
		Action:     Action{Message: "test"},
	})

	// Fire 3 times (send 5 events, only 3 should process before exhaustion).
	for i := 0; i < 5; i++ {
		directProcessEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	}

	// Should have 2 successful messages (1st and 3rd), 2nd failed.
	msgs := failEnq.getMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 successful messages, got %d", len(msgs))
	}

	// Trigger should be removed after 3 attempts regardless of success.
	if len(te.List()) != 0 {
		t.Fatal("trigger should be removed after MaxFirings exhausted (even with failures)")
	}
}

// TestFault_ConcurrentEventsDuringCooldown spawns goroutines sending events
// simultaneously and verifies exactly 1 firing occurs.
func TestFault_ConcurrentEventsDuringCooldown(t *testing.T) {
	clock := newMockClock(time.Now())
	te, enq := newTestTriggerEngineWithClock(clock)
	te.Add(&Trigger{
		ID:         "fault2",
		Event:      "state_change",
		State:      "idle",
		MaxFirings: -1,
		Cooldown:   1 * time.Second,
		Action:     Action{Message: "test"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan monitor.AgentEvent, 100)
	go te.Run(ctx, ch)

	// Spawn 10 goroutines each sending a matching event.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch <- stateChangeEvent(monitor.StateIdle, monitor.SubStateNone)
		}()
	}
	wg.Wait()
	time.Sleep(100 * time.Millisecond) // let events process
	cancel()

	msgs := enq.getMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected exactly 1 firing (cooldown should block rest), got %d", len(msgs))
	}

	triggers := te.List()
	if len(triggers) != 1 || triggers[0].FireCount != 1 {
		t.Fatal("trigger should have FireCount=1")
	}
}

// TestFault_ClockJumpForward simulates time jumping past ExpiresAt and verifies
// the trigger is reaped on the next event.
func TestFault_ClockJumpForward(t *testing.T) {
	baseTime := time.Now()
	clock := newMockClock(baseTime)
	te, enq := newTestTriggerEngineWithClock(clock)

	te.Add(&Trigger{
		ID:         "fault4",
		Event:      "state_change",
		State:      "idle",
		MaxFirings: -1,
		ExpiresAt:  baseTime.Add(1 * time.Hour),
		Action:     Action{Message: "test"},
	})

	// Fire once before expiry.
	directProcessEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 1 {
		t.Fatal("should fire before expiry")
	}

	// Jump time forward past expiry.
	clock.Set(baseTime.Add(2 * time.Hour))
	directProcessEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))

	if len(enq.getMessages()) != 1 {
		t.Fatal("should not fire after clock jump past expiry")
	}
	if len(te.List()) != 0 {
		t.Fatal("trigger should be reaped after clock jump past expiry")
	}
}

// =============================================================================
// Deterministic Simulation Tests
// =============================================================================

// TestDeterministicSimulation runs a seeded event sequence against multiple
// triggers and verifies the exact firing counts are reproducible.
func TestDeterministicSimulation(t *testing.T) {
	runSimulation := func(seed int64) []int {
		rng := rand.New(rand.NewSource(seed))
		baseTime := time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC)
		clock := newMockClock(baseTime)
		te, _ := newTestTriggerEngineWithClock(clock)

		states := []monitor.State{monitor.StateIdle, monitor.StateActive}

		// Register 5 triggers with varying configs.
		te.Add(&Trigger{ID: "sim1", Event: "state_change", State: "idle", MaxFirings: 3, Action: Action{Message: "a"}})
		te.Add(&Trigger{ID: "sim2", Event: "state_change", State: "idle", MaxFirings: -1, Cooldown: 500 * time.Millisecond, Action: Action{Message: "b"}})
		te.Add(&Trigger{ID: "sim3", Event: "state_change", MaxFirings: 5, Action: Action{Message: "c"}})
		te.Add(&Trigger{ID: "sim4", Event: "state_change", State: "idle", MaxFirings: -1, ExpiresAt: baseTime.Add(5 * time.Second), Action: Action{Message: "d"}})
		te.Add(&Trigger{ID: "sim5", Event: "state_change", MaxFirings: 0, Action: Action{Message: "e"}}) // default one-shot

		// Generate 200 events with random states and delays.
		for i := 0; i < 200; i++ {
			state := states[rng.Intn(len(states))]
			delay := time.Duration(rng.Intn(200)) * time.Millisecond
			clock.Advance(delay)
			directProcessEvent(te, stateChangeEvent(state, monitor.SubStateNone))
		}

		// Return fire counts for each trigger (from list or 0 if removed).
		counts := make([]int, 5)
		triggers := te.List()
		triggerMap := make(map[string]int)
		for _, tr := range triggers {
			triggerMap[tr.ID] = tr.FireCount
		}
		// For removed triggers, we know they hit their max.
		ids := []string{"sim1", "sim2", "sim3", "sim4", "sim5"}
		maxFirings := []int{3, -1, 5, -1, 1}
		for i, id := range ids {
			if fc, ok := triggerMap[id]; ok {
				counts[i] = fc
			} else if maxFirings[i] > 0 {
				counts[i] = maxFirings[i] // removed = exhausted
			}
		}
		return counts
	}

	// Run twice with the same seed — results must be identical.
	seed := int64(42)
	run1 := runSimulation(seed)
	run2 := runSimulation(seed)

	for i := range run1 {
		if run1[i] != run2[i] {
			t.Fatalf("non-deterministic: trigger %d had %d firings in run1 but %d in run2",
				i, run1[i], run2[i])
		}
	}

	// Sanity: different seed should (very likely) produce different results.
	run3 := runSimulation(seed + 1)
	allSame := true
	for i := range run1 {
		if run1[i] != run3[i] {
			allSame = false
			break
		}
	}
	if allSame {
		t.Log("warning: different seeds produced same results (unlikely but possible)")
	}
}

// TestCooldownBoundarySimulation tests exact cooldown boundary behavior with
// controlled clock.
func TestCooldownBoundarySimulation(t *testing.T) {
	cooldown := 100 * time.Millisecond
	baseTime := time.Now()
	clock := newMockClock(baseTime)
	te, enq := newTestTriggerEngineWithClock(clock)

	te.Add(&Trigger{
		ID:         "boundary",
		Event:      "state_change",
		State:      "idle",
		MaxFirings: -1,
		Cooldown:   cooldown,
		Action:     Action{Message: "test"},
	})

	// First firing.
	directProcessEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 1 {
		t.Fatal("first event should fire")
	}

	// At Cooldown - 1ms: should be blocked.
	clock.Advance(cooldown - 1*time.Millisecond)
	directProcessEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 1 {
		t.Fatal("event at cooldown-1ms should be blocked")
	}

	// Reset: advance to exactly cooldown from first firing.
	clock.Set(baseTime.Add(cooldown))
	directProcessEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 2 {
		t.Fatalf("event at exactly cooldown should fire, got %d messages", len(enq.getMessages()))
	}

	// At Cooldown + 1ms from second firing.
	clock.Advance(cooldown + 1*time.Millisecond)
	directProcessEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 3 {
		t.Fatalf("event at cooldown+1ms should fire, got %d messages", len(enq.getMessages()))
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

// BenchmarkProcessEvent_10Triggers benchmarks processEvent with 10 triggers.
func BenchmarkProcessEvent_10Triggers(b *testing.B) {
	benchmarkProcessEventN(b, 10)
}

// BenchmarkProcessEvent_100Triggers benchmarks processEvent with 100 triggers.
func BenchmarkProcessEvent_100Triggers(b *testing.B) {
	benchmarkProcessEventN(b, 100)
}

// BenchmarkProcessEvent_1000Triggers benchmarks processEvent with 1000 triggers.
func BenchmarkProcessEvent_1000Triggers(b *testing.B) {
	benchmarkProcessEventN(b, 1000)
}

func benchmarkProcessEventN(b *testing.B, n int) {
	clock := newMockClock(time.Now())
	enq := &mockEnqueuer{}
	runner := NewActionRunner(enq, nil, nil)
	te := NewTriggerEngine(runner, discardLogger())
	te.SetClock(clock)

	// Mix of repeating and one-shot triggers. Only a few match the event.
	for i := 0; i < n; i++ {
		maxF := 1
		if i%3 == 0 {
			maxF = -1 // unlimited
		}
		state := "active"
		if i < 3 {
			state = "idle" // only first 3 match
		}
		te.Add(&Trigger{
			ID:         fmt.Sprintf("bench-%d", i),
			Event:      "state_change",
			State:      state,
			MaxFirings: maxF,
			Cooldown:   5 * time.Minute,
			Action:     Action{Message: "bench"},
		})
	}

	evt := stateChangeEvent(monitor.StateIdle, monitor.SubStateNone)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		te.processEvent(ctx, evt)
		// Advance clock past cooldown to allow re-firing.
		clock.Advance(6 * time.Minute)
	}
}

// BenchmarkCooldownCheckOverhead compares one-shot vs cooldown trigger performance.
func BenchmarkCooldownCheckOverhead(b *testing.B) {
	b.Run("one-shot", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			clock := newMockClock(time.Now())
			enq := &mockEnqueuer{}
			runner := NewActionRunner(enq, nil, nil)
			te := NewTriggerEngine(runner, discardLogger())
			te.SetClock(clock)
			te.Add(&Trigger{
				ID:         "os",
				Event:      "state_change",
				State:      "idle",
				MaxFirings: 0,
				Action:     Action{Message: "test"},
			})
			te.processEvent(context.Background(), stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
		}
	})
	b.Run("with-cooldown", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			clock := newMockClock(time.Now())
			enq := &mockEnqueuer{}
			runner := NewActionRunner(enq, nil, nil)
			te := NewTriggerEngine(runner, discardLogger())
			te.SetClock(clock)
			te.Add(&Trigger{
				ID:         "cd",
				Event:      "state_change",
				State:      "idle",
				MaxFirings: -1,
				Cooldown:   5 * time.Minute,
				Action:     Action{Message: "test"},
			})
			te.processEvent(context.Background(), stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
		}
	})
}

// BenchmarkExpiryReaping benchmarks processEvent with 100 expired + 10 active triggers.
func BenchmarkExpiryReaping(b *testing.B) {
	baseTime := time.Now()
	clock := newMockClock(baseTime.Add(2 * time.Hour)) // well past expiry
	enq := &mockEnqueuer{}
	runner := NewActionRunner(enq, nil, nil)
	te := NewTriggerEngine(runner, discardLogger())
	te.SetClock(clock)

	// 100 expired triggers.
	for i := 0; i < 100; i++ {
		te.Add(&Trigger{
			ID:         fmt.Sprintf("expired-%d", i),
			Event:      "state_change",
			State:      "active",
			MaxFirings: -1,
			ExpiresAt:  baseTime.Add(1 * time.Hour), // expired
			Action:     Action{Message: "expired"},
		})
	}
	// 10 active triggers.
	for i := 0; i < 10; i++ {
		te.Add(&Trigger{
			ID:         fmt.Sprintf("active-%d", i),
			Event:      "state_change",
			State:      "idle",
			MaxFirings: -1,
			Cooldown:   5 * time.Minute,
			Action:     Action{Message: "active"},
		})
	}

	evt := stateChangeEvent(monitor.StateIdle, monitor.SubStateNone)
	ctx := context.Background()

	b.ResetTimer()
	// First iteration reaps all 100 expired; subsequent iterations just process 10 active.
	for i := 0; i < b.N; i++ {
		te.processEvent(ctx, evt)
		clock.Advance(6 * time.Minute)
	}
}

// =============================================================================
// Stress Tests (skipped in short mode)
// =============================================================================

// TestStress_RapidFireEventStream sends events rapidly to an unlimited trigger
// under the race detector.
func TestStress_RapidFireEventStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	clock := newMockClock(time.Now())
	te, enq := newTestTriggerEngineWithClock(clock)
	te.Add(&Trigger{
		ID:         "stress1",
		Event:      "state_change",
		State:      "idle",
		MaxFirings: -1,
		Action:     Action{Message: "stress"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan monitor.AgentEvent, 10000)
	go te.Run(ctx, ch)

	// Send 10000 events as fast as possible.
	for i := 0; i < 10000; i++ {
		ch <- stateChangeEvent(monitor.StateIdle, monitor.SubStateNone)
	}

	// Wait for processing.
	time.Sleep(2 * time.Second)
	cancel()

	msgs := enq.getMessages()
	if len(msgs) == 0 {
		t.Fatal("expected some firings")
	}
	t.Logf("stress test: %d firings from 10000 events", len(msgs))

	// Trigger should still be registered (unlimited).
	triggers := te.List()
	if len(triggers) != 1 {
		t.Fatal("unlimited trigger should still be registered")
	}
}

// TestStress_CooldownTriggerUnderLoad sends events faster than cooldown allows
// and verifies consistent behavior.
func TestStress_CooldownTriggerUnderLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	cooldown := 10 * time.Millisecond
	clock := newMockClock(time.Now())
	te, enq := newTestTriggerEngineWithClock(clock)
	te.Add(&Trigger{
		ID:         "stress2",
		Event:      "state_change",
		State:      "idle",
		MaxFirings: -1,
		Cooldown:   cooldown,
		Action:     Action{Message: "stress"},
	})

	// Send 1000 events with 5ms intervals (faster than 10ms cooldown).
	for i := 0; i < 1000; i++ {
		directProcessEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
		clock.Advance(5 * time.Millisecond)
	}

	msgs := enq.getMessages()
	// Should fire roughly every other event (10ms cooldown / 5ms interval).
	// Allow ±20% tolerance.
	expected := 500
	low := expected * 80 / 100
	high := expected * 120 / 100
	if len(msgs) < low || len(msgs) > high {
		t.Fatalf("expected ~%d firings (tolerance %d-%d), got %d", expected, low, high, len(msgs))
	}
	t.Logf("cooldown stress: %d firings from 1000 events (cooldown=%v, interval=5ms)", len(msgs), cooldown)
}
