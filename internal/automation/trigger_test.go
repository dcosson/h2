package automation

import (
	"context"
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

func (m *mockEnqueuer) EnqueueMessage(from, body string, priority message.Priority) (string, error) {
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

// sendEvent sends an event to the TriggerEngine by running it briefly.
func sendEvent(te *TriggerEngine, evt monitor.AgentEvent) {
	ch := make(chan monitor.AgentEvent, 1)
	ch <- evt
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		te.Run(ctx, ch)
		close(done)
	}()
	// Give the engine time to process.
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
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
