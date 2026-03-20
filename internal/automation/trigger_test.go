package automation

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"h2/internal/session/agent/monitor"
	"h2/internal/session/message"
)

// mockEnqueuer records messages enqueued by the ActionRunner.
// Shared across trigger_test.go and runner_test.go (runner_test.go references this).
type mockEnqueuer struct {
	mu       sync.Mutex
	messages []enqueuedMsg
}

type enqueuedMsg struct {
	From     string
	Body     string
	Priority message.Priority
}

func (m *mockEnqueuer) EnqueueMessage(from, body, header string, priority message.Priority) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, enqueuedMsg{From: from, Body: body, Priority: priority})
	return "test-id", nil
}

func (m *mockEnqueuer) getMessages() []enqueuedMsg {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]enqueuedMsg, len(m.messages))
	copy(cp, m.messages)
	return cp
}

// helper to create a TriggerEngine with a mock enqueuer.
func newTestTriggerEngine() (*TriggerEngine, *mockEnqueuer) {
	enq := &mockEnqueuer{}
	runner := NewActionRunner(enq, nil, nil)
	te := NewTriggerEngine(runner, nil)
	return te, enq
}

// sendEvent sends a single event to the TriggerEngine synchronously.
// It creates a closed channel with the event so Run drains and returns.
func sendEvent(te *TriggerEngine, evt monitor.AgentEvent) {
	ch := make(chan monitor.AgentEvent, 1)
	ch <- evt
	close(ch)
	te.Run(context.Background(), ch)
}

func stateChangeEvent(state monitor.State, sub monitor.SubState) monitor.AgentEvent {
	return monitor.AgentEvent{
		Type:      monitor.EventStateChange,
		Timestamp: time.Now(),
		Data:      monitor.StateChangeData{State: state, SubState: sub},
	}
}

func TestTriggerEngine_FiresOnMatch(t *testing.T) {
	te, enq := newTestTriggerEngine()
	te.Add(&Trigger{
		ID:    "t1",
		Name:  "test-trigger",
		Event: "state_change",
		State: "idle",
		Action: Action{
			Message:  "you are idle",
			Priority: "normal",
		},
	})

	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))

	msgs := enq.getMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Body != "you are idle" {
		t.Errorf("expected body 'you are idle', got %q", msgs[0].Body)
	}
}

func TestTriggerEngine_NoMatchWrongEvent(t *testing.T) {
	te, enq := newTestTriggerEngine()
	te.Add(&Trigger{
		ID:    "t1",
		Event: "approval_requested",
		Action: Action{
			Message: "should not fire",
		},
	})

	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))

	if len(enq.getMessages()) != 0 {
		t.Fatal("trigger should not have fired on wrong event type")
	}
	if len(te.List()) != 1 {
		t.Fatal("trigger should still be registered")
	}
}

func TestTriggerEngine_NoMatchWrongState(t *testing.T) {
	te, enq := newTestTriggerEngine()
	te.Add(&Trigger{
		ID:    "t1",
		Event: "state_change",
		State: "idle",
		Action: Action{
			Message: "should not fire",
		},
	})

	sendEvent(te, stateChangeEvent(monitor.StateActive, monitor.SubStateThinking))

	if len(enq.getMessages()) != 0 {
		t.Fatal("trigger should not have fired on wrong state")
	}
	if len(te.List()) != 1 {
		t.Fatal("trigger should still be registered")
	}
}

func TestTriggerEngine_SubStateMatch(t *testing.T) {
	te, enq := newTestTriggerEngine()
	te.Add(&Trigger{
		ID:       "t1",
		Event:    "state_change",
		SubState: "usage_limit",
		Action: Action{
			Message: "usage limit hit",
		},
	})

	sendEvent(te, stateChangeEvent(monitor.StateActive, monitor.SubStateUsageLimit))

	msgs := enq.getMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Body != "usage limit hit" {
		t.Errorf("expected 'usage limit hit', got %q", msgs[0].Body)
	}
}

func TestTriggerEngine_WildcardState(t *testing.T) {
	te, enq := newTestTriggerEngine()
	te.Add(&Trigger{
		ID:    "t1",
		Event: "state_change",
		Action: Action{
			Message: "any state change",
		},
	})

	sendEvent(te, stateChangeEvent(monitor.StateActive, monitor.SubStateThinking))

	if len(enq.getMessages()) != 1 {
		t.Fatal("wildcard trigger should have fired on any state_change")
	}
}

func TestTriggerEngine_ConditionPass(t *testing.T) {
	te, enq := newTestTriggerEngine()
	te.Add(&Trigger{
		ID:        "t1",
		Event:     "state_change",
		State:     "idle",
		Condition: "true",
		Action: Action{
			Message: "condition passed",
		},
	})

	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))

	if len(enq.getMessages()) != 1 {
		t.Fatal("trigger with passing condition should fire")
	}
}

func TestTriggerEngine_ConditionFail(t *testing.T) {
	te, enq := newTestTriggerEngine()
	te.Add(&Trigger{
		ID:        "t1",
		Event:     "state_change",
		State:     "idle",
		Condition: "false",
		Action: Action{
			Message: "should not fire",
		},
	})

	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))

	if len(enq.getMessages()) != 0 {
		t.Fatal("trigger with failing condition should not fire")
	}
	if len(te.List()) != 1 {
		t.Fatal("trigger should still be registered after condition failure")
	}
}

func TestTriggerEngine_ConditionFailThenPass(t *testing.T) {
	te, enq := newTestTriggerEngine()

	te.Add(&Trigger{
		ID:        "t1",
		Event:     "state_change",
		Condition: `test "$H2_EVENT_STATE" = "idle"`,
		Action: Action{
			Message: "became idle",
		},
	})

	// First event: active → condition fails.
	sendEvent(te, stateChangeEvent(monitor.StateActive, monitor.SubStateNone))
	if len(enq.getMessages()) != 0 {
		t.Fatal("should not fire when condition fails (active)")
	}
	if len(te.List()) != 1 {
		t.Fatal("trigger should still be registered")
	}

	// Second event: idle → condition passes.
	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 1 {
		t.Fatalf("expected 1 message after condition pass, got %d", len(enq.getMessages()))
	}
}

func TestTriggerEngine_OneShot(t *testing.T) {
	te, enq := newTestTriggerEngine()
	te.Add(&Trigger{
		ID:    "t1",
		Event: "state_change",
		State: "idle",
		Action: Action{
			Message: "idle once",
		},
	})

	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 1 {
		t.Fatal("trigger should fire on first match")
	}
	if len(te.List()) != 0 {
		t.Fatal("trigger should be removed after firing")
	}

	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 1 {
		t.Fatal("one-shot trigger should not fire twice")
	}
}

func TestTriggerEngine_Remove(t *testing.T) {
	te, enq := newTestTriggerEngine()
	te.Add(&Trigger{
		ID:    "t1",
		Event: "state_change",
		State: "idle",
		Action: Action{
			Message: "should not fire",
		},
	})

	if !te.Remove("t1") {
		t.Fatal("Remove should return true for existing trigger")
	}
	if te.Remove("t1") {
		t.Fatal("Remove should return false for non-existent trigger")
	}

	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))

	if len(enq.getMessages()) != 0 {
		t.Fatal("removed trigger should not fire")
	}
}

func TestTriggerEngine_AddDuplicateID(t *testing.T) {
	te, _ := newTestTriggerEngine()
	ok := te.Add(&Trigger{
		ID:    "t1",
		Event: "state_change",
		Action: Action{
			Message: "first",
		},
	})
	if !ok {
		t.Fatal("first Add should succeed")
	}

	ok = te.Add(&Trigger{
		ID:    "t1",
		Event: "state_change",
		Action: Action{
			Message: "duplicate",
		},
	})
	if ok {
		t.Fatal("Add with duplicate ID should return false")
	}
	triggers := te.List()
	if len(triggers) != 1 {
		t.Fatal("should have exactly 1 trigger")
	}
	if triggers[0].Action.Message != "first" {
		t.Error("original trigger should be preserved")
	}
}

func TestTriggerEngine_MultipleTriggersSameEvent(t *testing.T) {
	te, enq := newTestTriggerEngine()
	te.Add(&Trigger{
		ID:    "t1",
		Event: "state_change",
		State: "idle",
		Action: Action{
			Message: "trigger 1",
		},
	})
	te.Add(&Trigger{
		ID:    "t2",
		Event: "state_change",
		State: "idle",
		Action: Action{
			Message: "trigger 2",
		},
	})

	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))

	msgs := enq.getMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if len(te.List()) != 0 {
		t.Fatal("both triggers should be consumed")
	}
}

func TestTriggerEngine_EnvVarsSet(t *testing.T) {
	te, enq := newTestTriggerEngine()
	te.Add(&Trigger{
		ID:        "t1",
		Event:     "state_change",
		State:     "idle",
		Condition: `test "$H2_EVENT_TYPE" = "state_change" && test "$H2_TRIGGER_ID" = "t1"`,
		Action: Action{
			Message: "env ok",
		},
	})

	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))

	if len(enq.getMessages()) != 1 {
		t.Fatal("trigger with env-checking condition should fire")
	}
}

// mockClock is a controllable clock for deterministic testing.
type mockClock struct {
	mu  sync.Mutex
	now time.Time
}

func newMockClock(t time.Time) *mockClock {
	return &mockClock{now: t}
}

func (c *mockClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mockClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *mockClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// NewTimer satisfies the Clock interface. Trigger tests don't use timers,
// so this returns a stopped timer that never fires.
func (c *mockClock) NewTimer(d time.Duration) Timer {
	return &mockTimer{ch: make(chan time.Time)}
}

type mockTimer struct{ ch chan time.Time }

func (t *mockTimer) C() <-chan time.Time        { return t.ch }
func (t *mockTimer) Stop() bool                 { return true }
func (t *mockTimer) Reset(d time.Duration) bool { return true }

// newTestTriggerEngineWithClock creates a TriggerEngine with a mock clock.
func newTestTriggerEngineWithClock(clock Clock) (*TriggerEngine, *mockEnqueuer) {
	enq := &mockEnqueuer{}
	runner := NewActionRunner(enq, nil, nil)
	te := NewTriggerEngine(runner, nil)
	te.SetClock(clock)
	return te, enq
}

func TestTriggerEngine_NonStateChangeEvent(t *testing.T) {
	te, enq := newTestTriggerEngine()
	te.Add(&Trigger{
		ID:    "t1",
		Event: "turn_completed",
		Action: Action{
			Message: "turn done",
		},
	})

	evt := monitor.AgentEvent{
		Type:      monitor.EventTurnCompleted,
		Timestamp: time.Now(),
		Data:      monitor.TurnCompletedData{TurnID: "abc"},
	}
	sendEvent(te, evt)

	if len(enq.getMessages()) != 1 {
		t.Fatal("trigger should fire on non-state-change event type match")
	}
}

// --- Repeating Trigger Tests ---

func TestTriggerEngine_RepeatingFiresMultipleTimes(t *testing.T) {
	clock := newMockClock(time.Now())
	te, enq := newTestTriggerEngineWithClock(clock)
	te.Add(&Trigger{
		ID:         "t1",
		Event:      "state_change",
		State:      "idle",
		MaxFirings: 3,
		Action:     Action{Message: "nudge"},
	})

	for i := 0; i < 5; i++ {
		sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	}

	msgs := enq.getMessages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 firings, got %d", len(msgs))
	}
	if len(te.List()) != 0 {
		t.Fatal("trigger should be removed after exhausting MaxFirings")
	}
}

func TestTriggerEngine_UnlimitedFirings(t *testing.T) {
	clock := newMockClock(time.Now())
	te, enq := newTestTriggerEngineWithClock(clock)
	te.Add(&Trigger{
		ID:         "t1",
		Event:      "state_change",
		State:      "idle",
		MaxFirings: -1, // unlimited
		Action:     Action{Message: "nudge"},
	})

	for i := 0; i < 10; i++ {
		sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	}

	msgs := enq.getMessages()
	if len(msgs) != 10 {
		t.Fatalf("expected 10 firings, got %d", len(msgs))
	}
	triggers := te.List()
	if len(triggers) != 1 {
		t.Fatal("unlimited trigger should still be registered")
	}
	if triggers[0].FireCount != 10 {
		t.Fatalf("expected FireCount=10, got %d", triggers[0].FireCount)
	}
}

func TestTriggerEngine_DefaultOneShotPreserved(t *testing.T) {
	clock := newMockClock(time.Now())
	te, enq := newTestTriggerEngineWithClock(clock)
	te.Add(&Trigger{
		ID:    "t1",
		Event: "state_change",
		State: "idle",
		// MaxFirings=0 (unset) = default one-shot
		Action: Action{Message: "once"},
	})

	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 1 {
		t.Fatal("should fire once")
	}
	if len(te.List()) != 0 {
		t.Fatal("should be removed after one firing (default one-shot)")
	}

	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 1 {
		t.Fatal("should not fire again")
	}
}

func TestTriggerEngine_CooldownSkipsRapidEvents(t *testing.T) {
	clock := newMockClock(time.Now())
	te, enq := newTestTriggerEngineWithClock(clock)
	te.Add(&Trigger{
		ID:         "t1",
		Event:      "state_change",
		State:      "idle",
		MaxFirings: -1,
		Cooldown:   5 * time.Minute,
		Action:     Action{Message: "nudge"},
	})

	// First event fires.
	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 1 {
		t.Fatal("first event should fire")
	}

	// Second event 1 second later — should be blocked by cooldown.
	clock.Advance(1 * time.Second)
	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 1 {
		t.Fatal("second event within cooldown should not fire")
	}
}

func TestTriggerEngine_CooldownAllowsAfterDuration(t *testing.T) {
	clock := newMockClock(time.Now())
	te, enq := newTestTriggerEngineWithClock(clock)
	te.Add(&Trigger{
		ID:         "t1",
		Event:      "state_change",
		State:      "idle",
		MaxFirings: -1,
		Cooldown:   5 * time.Minute,
		Action:     Action{Message: "nudge"},
	})

	// First event fires.
	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 1 {
		t.Fatal("first event should fire")
	}

	// Advance past cooldown and fire again.
	clock.Advance(5*time.Minute + 1*time.Millisecond)
	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 2 {
		t.Fatalf("expected 2 firings after cooldown, got %d", len(enq.getMessages()))
	}
}

func TestTriggerEngine_CooldownExactBoundary(t *testing.T) {
	clock := newMockClock(time.Now())
	te, enq := newTestTriggerEngineWithClock(clock)
	te.Add(&Trigger{
		ID:         "t1",
		Event:      "state_change",
		State:      "idle",
		MaxFirings: -1,
		Cooldown:   5 * time.Minute,
		Action:     Action{Message: "nudge"},
	})

	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 1 {
		t.Fatal("first event should fire")
	}

	// At exactly cooldown duration — should fire (>= boundary).
	clock.Advance(5 * time.Minute)
	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 2 {
		t.Fatalf("expected 2 firings at exact cooldown boundary, got %d", len(enq.getMessages()))
	}
}

func TestTriggerEngine_ExpiresAtRemovesTrigger(t *testing.T) {
	clock := newMockClock(time.Now())
	te, enq := newTestTriggerEngineWithClock(clock)

	// Trigger that expired in the past.
	te.Add(&Trigger{
		ID:         "t1",
		Event:      "state_change",
		State:      "idle",
		MaxFirings: -1,
		ExpiresAt:  clock.Now().Add(-1 * time.Second),
		Action:     Action{Message: "should not fire"},
	})

	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 0 {
		t.Fatal("expired trigger should not fire")
	}
	if len(te.List()) != 0 {
		t.Fatal("expired trigger should be removed")
	}
}

func TestTriggerEngine_ExpiresAtAllowsBeforeDeadline(t *testing.T) {
	clock := newMockClock(time.Now())
	te, enq := newTestTriggerEngineWithClock(clock)

	te.Add(&Trigger{
		ID:         "t1",
		Event:      "state_change",
		State:      "idle",
		MaxFirings: -1,
		ExpiresAt:  clock.Now().Add(10 * time.Minute),
		Action:     Action{Message: "watching"},
	})

	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 1 {
		t.Fatal("trigger before deadline should fire")
	}
	if len(te.List()) != 1 {
		t.Fatal("trigger should still be registered before expiry")
	}
}

func TestTriggerEngine_ExpiryReapingOnUnrelatedEvent(t *testing.T) {
	clock := newMockClock(time.Now())
	te, _ := newTestTriggerEngineWithClock(clock)

	te.Add(&Trigger{
		ID:         "t1",
		Event:      "state_change",
		State:      "idle",
		MaxFirings: -1,
		ExpiresAt:  clock.Now().Add(1 * time.Minute),
		Action:     Action{Message: "should be reaped"},
	})

	// Advance past expiry.
	clock.Advance(2 * time.Minute)

	// Send an unrelated event (active, not idle) — should still reap the expired trigger.
	sendEvent(te, stateChangeEvent(monitor.StateActive, monitor.SubStateNone))
	if len(te.List()) != 0 {
		t.Fatal("expired trigger should be reaped even on unrelated event")
	}
}

func TestTriggerEngine_CooldownAndMaxFirings(t *testing.T) {
	clock := newMockClock(time.Now())
	te, enq := newTestTriggerEngineWithClock(clock)
	te.Add(&Trigger{
		ID:         "t1",
		Event:      "state_change",
		State:      "idle",
		MaxFirings: 3,
		Cooldown:   1 * time.Minute,
		Action:     Action{Message: "nudge"},
	})

	// Send 6 events with enough spacing to pass cooldown.
	for i := 0; i < 6; i++ {
		sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
		clock.Advance(2 * time.Minute)
	}

	msgs := enq.getMessages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 firings (MaxFirings limit), got %d", len(msgs))
	}
	if len(te.List()) != 0 {
		t.Fatal("trigger should be removed after MaxFirings exhausted")
	}
}

func TestTriggerEngine_CooldownAndCondition(t *testing.T) {
	clock := newMockClock(time.Now())
	te, enq := newTestTriggerEngineWithClock(clock)
	te.Add(&Trigger{
		ID:         "t1",
		Event:      "state_change",
		MaxFirings: -1,
		Cooldown:   1 * time.Minute,
		Condition:  `test "$H2_EVENT_STATE" = "idle"`,
		Action:     Action{Message: "nudge"},
	})

	// First: active state — condition fails, but cooldown should not start.
	sendEvent(te, stateChangeEvent(monitor.StateActive, monitor.SubStateNone))
	if len(enq.getMessages()) != 0 {
		t.Fatal("condition should fail for active state")
	}

	// Second: idle state — fires.
	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 1 {
		t.Fatal("should fire on idle")
	}

	// Third: idle again immediately — blocked by cooldown.
	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 1 {
		t.Fatal("should be blocked by cooldown")
	}

	// Fourth: advance past cooldown, idle again — fires.
	clock.Advance(2 * time.Minute)
	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	if len(enq.getMessages()) != 2 {
		t.Fatalf("expected 2 firings after cooldown, got %d", len(enq.getMessages()))
	}
}

func TestTriggerEngine_FireCountTracking(t *testing.T) {
	clock := newMockClock(time.Now())
	te, _ := newTestTriggerEngineWithClock(clock)
	te.Add(&Trigger{
		ID:         "t1",
		Event:      "state_change",
		State:      "idle",
		MaxFirings: 5,
		Action:     Action{Message: "nudge"},
	})

	for i := 1; i <= 3; i++ {
		sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
		triggers := te.List()
		if len(triggers) != 1 {
			t.Fatalf("trigger should still exist after %d firings", i)
		}
		if triggers[0].FireCount != i {
			t.Fatalf("expected FireCount=%d, got %d", i, triggers[0].FireCount)
		}
	}
}

func TestTriggerEngine_LastFiredAtTracking(t *testing.T) {
	baseTime := time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC)
	clock := newMockClock(baseTime)
	te, _ := newTestTriggerEngineWithClock(clock)
	te.Add(&Trigger{
		ID:         "t1",
		Event:      "state_change",
		State:      "idle",
		MaxFirings: -1,
		Action:     Action{Message: "nudge"},
	})

	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	triggers := te.List()
	if triggers[0].LastFiredAt != baseTime {
		t.Fatalf("expected LastFiredAt=%v, got %v", baseTime, triggers[0].LastFiredAt)
	}

	clock.Advance(5 * time.Minute)
	sendEvent(te, stateChangeEvent(monitor.StateIdle, monitor.SubStateNone))
	triggers = te.List()
	expected := baseTime.Add(5 * time.Minute)
	if triggers[0].LastFiredAt != expected {
		t.Fatalf("expected LastFiredAt=%v, got %v", expected, triggers[0].LastFiredAt)
	}
}

func TestTriggerEngine_ConcurrentAddDuringProcessEvent(t *testing.T) {
	clock := newMockClock(time.Now())
	te, _ := newTestTriggerEngineWithClock(clock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan monitor.AgentEvent, 100)
	go te.Run(ctx, ch)

	// Concurrently add triggers while sending events.
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			te.Add(&Trigger{
				ID:         fmt.Sprintf("t%d", i),
				Event:      "state_change",
				State:      "idle",
				MaxFirings: -1,
				Action:     Action{Message: fmt.Sprintf("msg-%d", i)},
			})
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			ch <- stateChangeEvent(monitor.StateIdle, monitor.SubStateNone)
		}
	}()

	wg.Wait()
	close(ch) // Run drains remaining events then returns
	cancel()

	// The test passes if no race condition panic occurs.
	// Run with -race to verify.
}

func TestTriggerEngine_EffectiveMaxFirings(t *testing.T) {
	tests := []struct {
		maxFirings int
		expected   int
	}{
		{0, 1},   // default one-shot
		{1, 1},   // explicit one-shot
		{3, 3},   // fixed count
		{-1, -1}, // unlimited
	}
	for _, tt := range tests {
		trig := &Trigger{MaxFirings: tt.maxFirings}
		got := trig.effectiveMaxFirings()
		if got != tt.expected {
			t.Errorf("effectiveMaxFirings(%d) = %d, want %d", tt.maxFirings, got, tt.expected)
		}
	}
}

func TestResolveExpiresAt(t *testing.T) {
	now := time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC)

	// Empty string.
	result, err := ResolveExpiresAt("", now)
	if err != nil || !result.IsZero() {
		t.Fatalf("empty string should return zero time, got %v, err %v", result, err)
	}

	// Relative "+1h".
	result, err = ResolveExpiresAt("+1h", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := now.Add(1 * time.Hour)
	if !result.Equal(expected) {
		t.Fatalf("expected %v, got %v", expected, result)
	}

	// Relative "+30m".
	result, err = ResolveExpiresAt("+30m", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected = now.Add(30 * time.Minute)
	if !result.Equal(expected) {
		t.Fatalf("expected %v, got %v", expected, result)
	}

	// Absolute RFC 3339.
	result, err = ResolveExpiresAt("2026-03-11T15:00:00Z", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected = time.Date(2026, 3, 11, 15, 0, 0, 0, time.UTC)
	if !result.Equal(expected) {
		t.Fatalf("expected %v, got %v", expected, result)
	}

	// Invalid relative.
	_, err = ResolveExpiresAt("+badvalue", now)
	if err == nil {
		t.Fatal("expected error for invalid relative duration")
	}

	// Invalid absolute.
	_, err = ResolveExpiresAt("not-a-timestamp", now)
	if err == nil {
		t.Fatal("expected error for invalid absolute timestamp")
	}
}
