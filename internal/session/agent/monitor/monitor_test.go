package monitor

import (
	"context"
	"testing"
	"time"
)

func TestNew_InitialState(t *testing.T) {
	m := New()
	state, subState := m.State()
	if state != StateInitialized {
		t.Errorf("state = %v, want Initialized", state)
	}
	if subState != SubStateNone {
		t.Errorf("subState = %v, want None", subState)
	}
}

func TestProcessEvent_SessionStarted(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	m.Events() <- AgentEvent{
		Type:      EventSessionStarted,
		Timestamp: time.Now(),
		Data:      SessionStartedData{ThreadID: "t-123", Model: "claude-4"},
	}

	// Let the event process.
	time.Sleep(10 * time.Millisecond)

	if m.ThreadID() != "t-123" {
		t.Errorf("ThreadID = %q, want %q", m.ThreadID(), "t-123")
	}
	if m.Model() != "claude-4" {
		t.Errorf("Model = %q, want %q", m.Model(), "claude-4")
	}
}

func TestProcessEvent_TurnCompleted_AccumulatesTokens(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	// Send two TurnCompleted events.
	m.Events() <- AgentEvent{
		Type:      EventTurnCompleted,
		Timestamp: time.Now(),
		Data: TurnCompletedData{
			InputTokens:  100,
			OutputTokens: 200,
			CachedTokens: 50,
			CostUSD:      0.01,
		},
	}
	m.Events() <- AgentEvent{
		Type:      EventTurnCompleted,
		Timestamp: time.Now(),
		Data: TurnCompletedData{
			InputTokens:  300,
			OutputTokens: 400,
			CachedTokens: 100,
			CostUSD:      0.02,
		},
	}

	time.Sleep(10 * time.Millisecond)

	snap := m.Metrics()
	if snap.InputTokens != 400 {
		t.Errorf("InputTokens = %d, want 400", snap.InputTokens)
	}
	if snap.OutputTokens != 600 {
		t.Errorf("OutputTokens = %d, want 600", snap.OutputTokens)
	}
	if snap.CachedTokens != 150 {
		t.Errorf("CachedTokens = %d, want 150", snap.CachedTokens)
	}
	if snap.TotalCostUSD != 0.03 {
		t.Errorf("TotalCostUSD = %f, want 0.03", snap.TotalCostUSD)
	}
}

func TestProcessEvent_TurnCompleted_DoesNotChangeState(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	m.Events() <- AgentEvent{
		Type:      EventStateChange,
		Timestamp: time.Now(),
		Data:      StateChangeData{State: StateActive, SubState: SubStateThinking},
	}
	m.Events() <- AgentEvent{
		Type:      EventTurnCompleted,
		Timestamp: time.Now(),
		Data:      TurnCompletedData{InputTokens: 1},
	}

	time.Sleep(10 * time.Millisecond)

	state, subState := m.State()
	if state != StateActive || subState != SubStateThinking {
		t.Fatalf("state = (%v,%v), want (Active,Thinking)", state, subState)
	}
}

func TestProcessEvent_TurnStarted_CountsUserPrompts(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	m.Events() <- AgentEvent{Type: EventUserPrompt, Timestamp: time.Now()}
	m.Events() <- AgentEvent{Type: EventUserPrompt, Timestamp: time.Now()}
	m.Events() <- AgentEvent{Type: EventUserPrompt, Timestamp: time.Now()}

	time.Sleep(10 * time.Millisecond)

	if m.Metrics().UserPromptCount != 3 {
		t.Errorf("UserPromptCount = %d, want 3", m.Metrics().UserPromptCount)
	}
}

func TestProcessEvent_ToolCompleted_CountsTools(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	m.Events() <- AgentEvent{
		Type: EventToolCompleted, Timestamp: time.Now(),
		Data: ToolCompletedData{ToolName: "Bash"},
	}
	m.Events() <- AgentEvent{
		Type: EventToolCompleted, Timestamp: time.Now(),
		Data: ToolCompletedData{ToolName: "Read"},
	}
	m.Events() <- AgentEvent{
		Type: EventToolCompleted, Timestamp: time.Now(),
		Data: ToolCompletedData{ToolName: "Bash"},
	}

	time.Sleep(10 * time.Millisecond)

	snap := m.Metrics()
	if snap.ToolCounts["Bash"] != 2 {
		t.Errorf("ToolCounts[Bash] = %d, want 2", snap.ToolCounts["Bash"])
	}
	if snap.ToolCounts["Read"] != 1 {
		t.Errorf("ToolCounts[Read] = %d, want 1", snap.ToolCounts["Read"])
	}
}

func TestProcessEvent_StateChange(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	m.Events() <- AgentEvent{
		Type:      EventStateChange,
		Timestamp: time.Now(),
		Data:      StateChangeData{State: StateActive, SubState: SubStateThinking},
	}

	time.Sleep(10 * time.Millisecond)

	state, subState := m.State()
	if state != StateActive {
		t.Errorf("state = %v, want Active", state)
	}
	if subState != SubStateThinking {
		t.Errorf("subState = %v, want Thinking", subState)
	}
}

func TestProcessEvent_SessionEnded_SetsExited(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	m.Events() <- AgentEvent{Type: EventSessionEnded, Timestamp: time.Now()}

	time.Sleep(10 * time.Millisecond)

	state, _ := m.State()
	if state != StateExited {
		t.Errorf("state = %v, want Exited", state)
	}
}

func TestSetExited(t *testing.T) {
	m := New()
	m.SetExited()

	state, subState := m.State()
	if state != StateExited {
		t.Errorf("state = %v, want Exited", state)
	}
	if subState != SubStateNone {
		t.Errorf("subState = %v, want None", subState)
	}
}

func TestWaitForState(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	// Start waiting in a goroutine.
	done := make(chan bool, 1)
	go func() {
		done <- m.WaitForState(ctx, StateActive)
	}()

	// Transition to Active.
	m.Events() <- AgentEvent{
		Type:      EventStateChange,
		Timestamp: time.Now(),
		Data:      StateChangeData{State: StateActive, SubState: SubStateNone},
	}

	select {
	case ok := <-done:
		if !ok {
			t.Error("WaitForState returned false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for state")
	}
}

func TestWaitForState_CancelReturnsfalse(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)

	done := make(chan bool, 1)
	go func() {
		done <- m.WaitForState(ctx, StateExited)
	}()

	cancel()

	select {
	case ok := <-done:
		if ok {
			t.Error("WaitForState returned true after cancel, want false")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestStateChanged_NotifiesOnChange(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	ch := m.StateChanged()

	m.Events() <- AgentEvent{
		Type:      EventStateChange,
		Timestamp: time.Now(),
		Data:      StateChangeData{State: StateActive, SubState: SubStateNone},
	}

	select {
	case <-ch:
		// OK, got notification.
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for state change notification")
	}
}

func TestWithEventWriter(t *testing.T) {
	var written []AgentEvent
	writer := func(ev AgentEvent) error {
		written = append(written, ev)
		return nil
	}

	m := New(WithEventWriter(writer))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	m.Events() <- AgentEvent{
		Type:      EventSessionStarted,
		Timestamp: time.Now(),
		Data:      SessionStartedData{ThreadID: "t1", Model: "m1"},
	}
	m.Events() <- AgentEvent{
		Type:      EventTurnCompleted,
		Timestamp: time.Now(),
		Data:      TurnCompletedData{InputTokens: 50},
	}

	time.Sleep(10 * time.Millisecond)

	if len(written) != 2 {
		t.Fatalf("expected 2 written events, got %d", len(written))
	}
	if written[0].Type != EventSessionStarted {
		t.Errorf("written[0].Type = %v, want EventSessionStarted", written[0].Type)
	}
	if written[1].Type != EventTurnCompleted {
		t.Errorf("written[1].Type = %v, want EventTurnCompleted", written[1].Type)
	}
}

func TestRunBlocksUntilCancelled(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		m.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// OK
	case <-time.After(time.Second):
		t.Fatal("Run didn't return after cancel")
	}
}

func TestMetrics_SnapshotIsolation(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	m.Events() <- AgentEvent{
		Type: EventToolCompleted, Timestamp: time.Now(),
		Data: ToolCompletedData{ToolName: "Bash"},
	}
	time.Sleep(10 * time.Millisecond)

	snap := m.Metrics()

	// Mutating the snapshot should not affect the monitor.
	snap.ToolCounts["Bash"] = 999

	snap2 := m.Metrics()
	if snap2.ToolCounts["Bash"] != 1 {
		t.Errorf("ToolCounts[Bash] = %d, want 1 (snapshot mutation leaked)", snap2.ToolCounts["Bash"])
	}
}
