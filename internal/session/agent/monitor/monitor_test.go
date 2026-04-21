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
		Data:      SessionStartedData{SessionID: "t-123", Model: "claude-4"},
	}

	// Let the event process.
	time.Sleep(10 * time.Millisecond)

	if m.SessionID() != "t-123" {
		t.Errorf("SessionID = %q, want %q", m.SessionID(), "t-123")
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

	snap := m.MetricsSnapshot()
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

	if m.MetricsSnapshot().UserPromptCount != 3 {
		t.Errorf("UserPromptCount = %d, want 3", m.MetricsSnapshot().UserPromptCount)
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

	snap := m.MetricsSnapshot()
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

func TestResetForRelaunch(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	// Drive to Active then Exited.
	m.Events() <- AgentEvent{Type: EventStateChange, Data: StateChangeData{State: StateActive, SubState: SubStateThinking}}
	time.Sleep(20 * time.Millisecond)
	m.SetExited()
	state, _ := m.State()
	if state != StateExited {
		t.Fatalf("expected StateExited, got %v", state)
	}

	// State changes should be ignored while exited.
	m.Events() <- AgentEvent{Type: EventStateChange, Data: StateChangeData{State: StateActive, SubState: SubStateToolUse}}
	time.Sleep(20 * time.Millisecond)
	state, _ = m.State()
	if state != StateExited {
		t.Fatalf("expected StateExited to be sticky, got %v", state)
	}

	// Reset for relaunch.
	m.ResetForRelaunch()
	state, sub := m.State()
	if state != StateInitialized {
		t.Fatalf("expected StateInitialized after reset, got %v", state)
	}
	if sub != SubStateNone {
		t.Fatalf("expected SubStateNone after reset, got %v", sub)
	}

	// State changes should work again after reset.
	m.Events() <- AgentEvent{Type: EventStateChange, Data: StateChangeData{State: StateActive, SubState: SubStateThinking}}
	time.Sleep(20 * time.Millisecond)
	state, sub = m.State()
	if state != StateActive {
		t.Fatalf("expected StateActive after reset+event, got %v", state)
	}
	if sub != SubStateThinking {
		t.Fatalf("expected SubStateThinking after reset+event, got %v", sub)
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
	writtenCh := make(chan AgentEvent, 10)
	writer := func(ev AgentEvent) error {
		writtenCh <- ev
		return nil
	}

	m := New(WithEventWriter(writer))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	m.Events() <- AgentEvent{
		Type:      EventSessionStarted,
		Timestamp: time.Now(),
		Data:      SessionStartedData{SessionID: "t1", Model: "m1"},
	}
	m.Events() <- AgentEvent{
		Type:      EventTurnCompleted,
		Timestamp: time.Now(),
		Data:      TurnCompletedData{InputTokens: 50},
	}

	var written []AgentEvent
	for range 2 {
		select {
		case ev := <-writtenCh:
			written = append(written, ev)
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for written events, got %d of 2", len(written))
		}
	}

	if written[0].Type != EventSessionStarted {
		t.Errorf("written[0].Type = %v, want EventSessionStarted", written[0].Type)
	}
	if written[1].Type != EventTurnCompleted {
		t.Errorf("written[1].Type = %v, want EventTurnCompleted", written[1].Type)
	}
}

func TestOnSessionStartedCallback(t *testing.T) {
	doneCh := make(chan SessionStartedData, 1)

	m := New()
	m.SetOnSessionStarted(func(data SessionStartedData) {
		doneCh <- data
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	m.Events() <- AgentEvent{
		Type:      EventSessionStarted,
		Timestamp: time.Now(),
		Data:      SessionStartedData{SessionID: "harness-123", Model: "claude-4"},
	}

	select {
	case callbackData := <-doneCh:
		if callbackData.SessionID != "harness-123" {
			t.Errorf("SessionID = %q, want %q", callbackData.SessionID, "harness-123")
		}
		if callbackData.Model != "claude-4" {
			t.Errorf("Model = %q, want %q", callbackData.Model, "claude-4")
		}
	case <-time.After(time.Second):
		t.Fatal("OnSessionStarted callback was not called within timeout")
	}
	// Verify the monitor also stored the session ID.
	if m.SessionID() != "harness-123" {
		t.Errorf("monitor.SessionID() = %q, want %q", m.SessionID(), "harness-123")
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

func TestProcessEvent_PermissionReview_NotBlocked(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	// PermissionReview (hook evaluating) should NOT set blockedOnPermission.
	m.Events() <- AgentEvent{
		Type:      EventStateChange,
		Timestamp: time.Now(),
		Data:      StateChangeData{State: StateActive, SubState: SubStatePermissionReview},
	}
	time.Sleep(20 * time.Millisecond)

	activity := m.Activity()
	if activity.BlockedOnPermission {
		t.Error("PermissionReview should not set BlockedOnPermission")
	}
}

func TestProcessEvent_BlockedOnPermission_SetsBlocked(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	// ApprovalRequested captures the tool name but doesn't set blocked.
	m.Events() <- AgentEvent{
		Type:      EventApprovalRequested,
		Timestamp: time.Now(),
		Data:      ApprovalRequestedData{ToolName: "Bash"},
	}
	time.Sleep(20 * time.Millisecond)

	activity := m.Activity()
	if activity.BlockedOnPermission {
		t.Error("ApprovalRequested alone should not set BlockedOnPermission")
	}
	if activity.BlockedToolName != "Bash" {
		t.Errorf("BlockedToolName = %q, want Bash", activity.BlockedToolName)
	}

	// BlockedOnPermission (ask_user) SHOULD set blockedOnPermission.
	m.Events() <- AgentEvent{
		Type:      EventStateChange,
		Timestamp: time.Now(),
		Data:      StateChangeData{State: StateActive, SubState: SubStateBlockedOnPermission},
	}
	time.Sleep(20 * time.Millisecond)

	activity = m.Activity()
	if !activity.BlockedOnPermission {
		t.Error("BlockedOnPermission sub-state should set BlockedOnPermission flag")
	}

	// Transitioning away should clear it.
	m.Events() <- AgentEvent{
		Type:      EventStateChange,
		Timestamp: time.Now(),
		Data:      StateChangeData{State: StateActive, SubState: SubStateToolUse},
	}
	time.Sleep(20 * time.Millisecond)

	activity = m.Activity()
	if activity.BlockedOnPermission {
		t.Error("BlockedOnPermission should be cleared after leaving blocked state")
	}
	if activity.BlockedToolName != "" {
		t.Errorf("BlockedToolName should be cleared, got %q", activity.BlockedToolName)
	}
}

func TestProcessEvent_TracksLastActivityAt(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	ts := time.Date(2026, 3, 21, 12, 0, 0, 0, time.UTC)
	m.Events() <- AgentEvent{
		Type:      EventAgentMessage,
		Timestamp: ts,
		Data:      AgentMessageData{Content: "hello"},
	}
	time.Sleep(20 * time.Millisecond)

	activity := m.Activity()
	if !activity.LastActivityAt.Equal(ts) {
		t.Fatalf("LastActivityAt = %v, want %v", activity.LastActivityAt, ts)
	}
}

func TestProcessEvent_UsageLimitInfo(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	resetsAt := time.Date(2026, 3, 12, 19, 0, 0, 0, time.UTC)
	m.Events() <- AgentEvent{
		Type:      EventUsageLimitInfo,
		Timestamp: time.Now(),
		Data:      UsageLimitData{ResetsAt: resetsAt, Message: "resets 12pm (America/Los_Angeles)"},
	}
	time.Sleep(20 * time.Millisecond)

	got := m.UsageLimitResetsAt()
	if got == nil {
		t.Fatal("UsageLimitResetsAt should not be nil")
	}
	if !got.Equal(resetsAt) {
		t.Errorf("UsageLimitResetsAt = %v, want %v", *got, resetsAt)
	}
	if m.UsageLimitMessage() != "resets 12pm (America/Los_Angeles)" {
		t.Errorf("UsageLimitMessage = %q", m.UsageLimitMessage())
	}
}

func TestProcessEvent_UsageLimitInfo_ClearedOnStateChange(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	// Set usage limit info.
	resetsAt := time.Date(2026, 3, 12, 19, 0, 0, 0, time.UTC)
	m.Events() <- AgentEvent{
		Type:      EventUsageLimitInfo,
		Timestamp: time.Now(),
		Data:      UsageLimitData{ResetsAt: resetsAt, Message: "test"},
	}
	time.Sleep(20 * time.Millisecond)

	if m.UsageLimitResetsAt() == nil {
		t.Fatal("expected usage limit to be set")
	}

	// Transition to active/thinking — should clear usage limit.
	m.Events() <- AgentEvent{
		Type:      EventStateChange,
		Timestamp: time.Now(),
		Data:      StateChangeData{State: StateActive, SubState: SubStateThinking},
	}
	time.Sleep(20 * time.Millisecond)

	if m.UsageLimitResetsAt() != nil {
		t.Error("UsageLimitResetsAt should be nil after leaving usage_limit state")
	}
	if m.UsageLimitMessage() != "" {
		t.Errorf("UsageLimitMessage should be empty, got %q", m.UsageLimitMessage())
	}
}

func TestProcessEvent_UsageLimitInfo_CallbackFires(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	var callbackData UsageLimitData
	callbackFired := make(chan struct{}, 1)
	m.SetOnUsageLimit(func(data UsageLimitData) {
		callbackData = data
		callbackFired <- struct{}{}
	})

	resetsAt := time.Date(2026, 3, 12, 19, 0, 0, 0, time.UTC)
	m.Events() <- AgentEvent{
		Type:      EventUsageLimitInfo,
		Timestamp: time.Now(),
		Data:      UsageLimitData{ResetsAt: resetsAt, Message: "callback test"},
	}

	select {
	case <-callbackFired:
	case <-time.After(1 * time.Second):
		t.Fatal("usage limit callback was not fired")
	}

	if !callbackData.ResetsAt.Equal(resetsAt) {
		t.Errorf("callback ResetsAt = %v, want %v", callbackData.ResetsAt, resetsAt)
	}
	if callbackData.Message != "callback test" {
		t.Errorf("callback Message = %q, want %q", callbackData.Message, "callback test")
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

	snap := m.MetricsSnapshot()

	// Mutating the snapshot should not affect the monitor.
	snap.ToolCounts["Bash"] = 999

	snap2 := m.MetricsSnapshot()
	if snap2.ToolCounts["Bash"] != 1 {
		t.Errorf("ToolCounts[Bash] = %d, want 1 (snapshot mutation leaked)", snap2.ToolCounts["Bash"])
	}
}

func TestInject_ReachesSubscribers(t *testing.T) {
	m := New()
	sub := m.Subscribe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	// Inject a session_rotated event from outside the harness.
	m.Inject(AgentEvent{
		Type:      EventSessionRotated,
		Timestamp: time.Now(),
		Data:      SessionRotatedData{OldProfile: "default", NewProfile: "alt1"},
	})

	select {
	case ev := <-sub:
		if ev.Type != EventSessionRotated {
			t.Errorf("Type = %v, want EventSessionRotated", ev.Type)
		}
		data, ok := ev.Data.(SessionRotatedData)
		if !ok {
			t.Fatal("Data is not SessionRotatedData")
		}
		if data.OldProfile != "default" || data.NewProfile != "alt1" {
			t.Errorf("profiles = (%q, %q), want (default, alt1)", data.OldProfile, data.NewProfile)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive injected event")
	}

	// Also inject session_restarted.
	m.Inject(AgentEvent{
		Type:      EventSessionRestarted,
		Timestamp: time.Now(),
		Data:      SessionRestartedData{},
	})

	select {
	case ev := <-sub:
		if ev.Type != EventSessionRestarted {
			t.Errorf("Type = %v, want EventSessionRestarted", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive injected restart event")
	}
}

func TestProcessEvent_ServerError_SetAndAutoClear(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	// Enter server error state.
	m.Events() <- AgentEvent{
		Type:      EventStateChange,
		Timestamp: time.Now(),
		Data:      StateChangeData{State: StateIdle, SubState: SubStateServerError},
	}
	m.Events() <- AgentEvent{
		Type:      EventServerErrorInfo,
		Timestamp: time.Now(),
		Data:      ServerErrorData{StatusCode: "500", Message: "Internal server error"},
	}

	time.Sleep(20 * time.Millisecond)

	state, subState := m.State()
	if state != StateIdle || subState != SubStateServerError {
		t.Fatalf("state = (%v,%v), want (Idle,ServerError)", state, subState)
	}
	if m.ServerErrorMessage() != "Internal server error" {
		t.Fatalf("ServerErrorMessage = %q, want %q", m.ServerErrorMessage(), "Internal server error")
	}

	// Successful turn should auto-clear the server error.
	m.Events() <- AgentEvent{
		Type:      EventTurnCompleted,
		Timestamp: time.Now(),
		Data:      TurnCompletedData{InputTokens: 100, OutputTokens: 50, CostUSD: 0.01},
	}

	time.Sleep(20 * time.Millisecond)

	state, subState = m.State()
	if subState == SubStateServerError {
		t.Fatalf("subState should not be ServerError after successful turn, got (%v,%v)", state, subState)
	}
	if m.ServerErrorMessage() != "" {
		t.Fatalf("ServerErrorMessage should be cleared, got %q", m.ServerErrorMessage())
	}
}

func TestProcessEvent_ServerError_ClearedCallback(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cleared := make(chan struct{}, 1)
	m.SetOnServerErrorCleared(func() {
		cleared <- struct{}{}
	})

	errored := make(chan ServerErrorData, 1)
	m.SetOnServerError(func(data ServerErrorData) {
		errored <- data
	})

	go m.Run(ctx)

	// Enter server error state.
	m.Events() <- AgentEvent{
		Type:      EventStateChange,
		Timestamp: time.Now(),
		Data:      StateChangeData{State: StateIdle, SubState: SubStateServerError},
	}
	m.Events() <- AgentEvent{
		Type:      EventServerErrorInfo,
		Timestamp: time.Now(),
		Data:      ServerErrorData{StatusCode: "500", Message: "Internal server error"},
	}

	select {
	case data := <-errored:
		if data.StatusCode != "500" {
			t.Errorf("StatusCode = %q, want %q", data.StatusCode, "500")
		}
	case <-time.After(time.Second):
		t.Fatal("OnServerError callback not called")
	}

	// Successful turn triggers clear callback.
	m.Events() <- AgentEvent{
		Type:      EventTurnCompleted,
		Timestamp: time.Now(),
		Data:      TurnCompletedData{InputTokens: 100, OutputTokens: 50},
	}

	select {
	case <-cleared:
		// OK
	case <-time.After(time.Second):
		t.Fatal("OnServerErrorCleared callback not called after successful turn")
	}
}

func TestProcessEvent_ServerError_NotClearedByZeroTokenTurn(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	// Enter server error state.
	m.Events() <- AgentEvent{
		Type:      EventStateChange,
		Timestamp: time.Now(),
		Data:      StateChangeData{State: StateIdle, SubState: SubStateServerError},
	}
	m.Events() <- AgentEvent{
		Type:      EventServerErrorInfo,
		Timestamp: time.Now(),
		Data:      ServerErrorData{StatusCode: "500", Message: "Internal server error"},
	}

	time.Sleep(20 * time.Millisecond)

	// A turn with zero tokens (not a real successful response) should NOT clear.
	m.Events() <- AgentEvent{
		Type:      EventTurnCompleted,
		Timestamp: time.Now(),
		Data:      TurnCompletedData{},
	}

	time.Sleep(20 * time.Millisecond)

	if m.ServerErrorMessage() != "Internal server error" {
		t.Fatalf("ServerErrorMessage should not be cleared by zero-token turn, got %q", m.ServerErrorMessage())
	}
}
