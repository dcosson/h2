package codex

import (
	"encoding/json"
	"testing"
	"time"

	"h2/internal/session/agent/monitor"
)

// makeLogsPayload builds an OTEL logs payload with a single log record.
func makeLogsPayload(name string, attrs []otelAttribute) []byte {
	recAttrs := make([]otelAttribute, 0, len(attrs)+1)
	recAttrs = append(recAttrs, otelAttribute{
		Key:   "event.name",
		Value: otelAttrValue{StringValue: name},
	})
	recAttrs = append(recAttrs, attrs...)
	payload := otelLogsPayload{
		ResourceLogs: []otelResourceLogs{{
			ScopeLogs: []otelScopeLogs{{
				LogRecords: []otelLogRecord{{
					Attributes: recAttrs,
				}},
			}},
		}},
	}
	body, _ := json.Marshal(payload)
	return body
}

func drainEvents(ch chan monitor.AgentEvent, n int) []monitor.AgentEvent {
	var events []monitor.AgentEvent
	timeout := time.After(time.Second)
	for len(events) < n {
		select {
		case ev := <-ch:
			events = append(events, ev)
		case <-timeout:
			return events
		}
	}
	return events
}

func TestEventHandler_ConversationStarts(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewEventHandler(events)

	body := makeLogsPayload("codex.conversation_starts", []otelAttribute{
		{Key: "conversation.id", Value: otelAttrValue{StringValue: "conv-123"}},
		{Key: "model", Value: otelAttrValue{StringValue: "o3"}},
	})
	p.OnLogs(body)

	got := drainEvents(events, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	if got[0].Type != monitor.EventSessionStarted {
		t.Fatalf("Type = %v, want EventSessionStarted", got[0].Type)
	}
	if got[1].Type != monitor.EventStateChange {
		t.Fatalf("Type = %v, want EventStateChange", got[1].Type)
	}
	data := got[0].Data.(monitor.SessionStartedData)
	if data.ThreadID != "conv-123" {
		t.Errorf("ThreadID = %q, want %q", data.ThreadID, "conv-123")
	}
	if data.Model != "o3" {
		t.Errorf("Model = %q, want %q", data.Model, "o3")
	}
	state := got[1].Data.(monitor.StateChangeData)
	if state.State != monitor.StateIdle || state.SubState != monitor.SubStateNone {
		t.Errorf("state = (%v,%v), want (Idle,None)", state.State, state.SubState)
	}
}

func TestEventHandler_UserPrompt(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewEventHandler(events)

	body := makeLogsPayload("codex.user_prompt", []otelAttribute{
		{Key: "prompt_length", Value: otelAttrValue{IntValue: json.RawMessage("42")}},
	})
	p.OnLogs(body)

	got := drainEvents(events, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	if got[0].Type != monitor.EventUserPrompt {
		t.Fatalf("Type = %v, want EventUserPrompt", got[0].Type)
	}
	if got[1].Type != monitor.EventStateChange {
		t.Fatalf("Type = %v, want EventStateChange", got[1].Type)
	}
	state := got[1].Data.(monitor.StateChangeData)
	if state.State != monitor.StateActive || state.SubState != monitor.SubStateThinking {
		t.Errorf("state = (%v,%v), want (Active,Thinking)", state.State, state.SubState)
	}
}

func TestEventHandler_SSEEvent_Completed(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewEventHandler(events)

	body := makeLogsPayload("codex.sse_event", []otelAttribute{
		{Key: "event.kind", Value: otelAttrValue{StringValue: "response.completed"}},
		{Key: "input_token_count", Value: otelAttrValue{IntValue: json.RawMessage("500")}},
		{Key: "output_token_count", Value: otelAttrValue{IntValue: json.RawMessage("300")}},
		{Key: "cached_token_count", Value: otelAttrValue{IntValue: json.RawMessage("100")}},
	})
	p.OnLogs(body)

	got := drainEvents(events, 1)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Type != monitor.EventTurnCompleted {
		t.Fatalf("Type = %v, want EventTurnCompleted", got[0].Type)
	}
	data := got[0].Data.(monitor.TurnCompletedData)
	if data.InputTokens != 500 {
		t.Errorf("InputTokens = %d, want 500", data.InputTokens)
	}
	if data.OutputTokens != 300 {
		t.Errorf("OutputTokens = %d, want 300", data.OutputTokens)
	}
	if data.CachedTokens != 100 {
		t.Errorf("CachedTokens = %d, want 100", data.CachedTokens)
	}
}

func TestEventHandler_SSEEvent_Completed_UsesInputCachedDelta(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewEventHandler(events)

	first := makeLogsPayload("codex.sse_event", []otelAttribute{
		{Key: "event.kind", Value: otelAttrValue{StringValue: "response.completed"}},
		{Key: "input_token_count", Value: otelAttrValue{IntValue: json.RawMessage("1000")}},
		{Key: "output_token_count", Value: otelAttrValue{IntValue: json.RawMessage("40")}},
		{Key: "cached_token_count", Value: otelAttrValue{IntValue: json.RawMessage("900")}},
	})
	p.OnLogs(first)

	second := makeLogsPayload("codex.sse_event", []otelAttribute{
		{Key: "event.kind", Value: otelAttrValue{StringValue: "response.completed"}},
		{Key: "input_token_count", Value: otelAttrValue{IntValue: json.RawMessage("1120")}},
		{Key: "output_token_count", Value: otelAttrValue{IntValue: json.RawMessage("50")}},
		{Key: "cached_token_count", Value: otelAttrValue{IntValue: json.RawMessage("980")}},
	})
	p.OnLogs(second)

	got := drainEvents(events, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	firstTurn := got[0].Data.(monitor.TurnCompletedData)
	if firstTurn.InputTokens != 1000 || firstTurn.CachedTokens != 900 || firstTurn.OutputTokens != 40 {
		t.Fatalf("first turn = %+v, want input=1000 cached=900 output=40", firstTurn)
	}
	secondTurn := got[1].Data.(monitor.TurnCompletedData)
	if secondTurn.InputTokens != 120 || secondTurn.CachedTokens != 80 || secondTurn.OutputTokens != 50 {
		t.Fatalf("second turn = %+v, want input=120 cached=80 output=50", secondTurn)
	}
}

func TestEventHandler_SSEEvent_Completed_DeltaResetsOnDecrease(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewEventHandler(events)

	first := makeLogsPayload("codex.sse_event", []otelAttribute{
		{Key: "event.kind", Value: otelAttrValue{StringValue: "response.completed"}},
		{Key: "input_token_count", Value: otelAttrValue{IntValue: json.RawMessage("1200")}},
		{Key: "output_token_count", Value: otelAttrValue{IntValue: json.RawMessage("20")}},
		{Key: "cached_token_count", Value: otelAttrValue{IntValue: json.RawMessage("1000")}},
	})
	p.OnLogs(first)

	// Simulate compaction/reset where reported counts drop.
	second := makeLogsPayload("codex.sse_event", []otelAttribute{
		{Key: "event.kind", Value: otelAttrValue{StringValue: "response.completed"}},
		{Key: "input_token_count", Value: otelAttrValue{IntValue: json.RawMessage("700")}},
		{Key: "output_token_count", Value: otelAttrValue{IntValue: json.RawMessage("30")}},
		{Key: "cached_token_count", Value: otelAttrValue{IntValue: json.RawMessage("500")}},
	})
	p.OnLogs(second)

	got := drainEvents(events, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	secondTurn := got[1].Data.(monitor.TurnCompletedData)
	if secondTurn.InputTokens != 700 || secondTurn.CachedTokens != 500 || secondTurn.OutputTokens != 30 {
		t.Fatalf("second turn = %+v, want reset input=700 cached=500 output=30", secondTurn)
	}
}

func TestEventHandler_SSEEvent_ResponseCreated_EmitsActive(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewEventHandler(events)

	body := makeLogsPayload("codex.sse_event", []otelAttribute{
		{Key: "event.kind", Value: otelAttrValue{StringValue: "response.created"}},
	})
	p.OnLogs(body)

	got := drainEvents(events, 1)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Type != monitor.EventStateChange {
		t.Fatalf("Type = %v, want EventStateChange", got[0].Type)
	}
	state := got[0].Data.(monitor.StateChangeData)
	if state.State != monitor.StateActive || state.SubState != monitor.SubStateThinking {
		t.Errorf("state = (%v,%v), want (Active,Thinking)", state.State, state.SubState)
	}
}

func TestEventHandler_SSEEvent_NonCompleted_NoEmit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewEventHandler(events)

	body := makeLogsPayload("codex.sse_event", []otelAttribute{
		{Key: "event.kind", Value: otelAttrValue{StringValue: "response.in_progress"}},
	})
	p.OnLogs(body)

	select {
	case ev := <-events:
		t.Errorf("unexpected event for non-completed SSE: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// OK
	}
}

func TestEventHandler_SSEEvent_CompletedZeroTokens_NoEmit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewEventHandler(events)

	body := makeLogsPayload("codex.sse_event", []otelAttribute{
		{Key: "event.kind", Value: otelAttrValue{StringValue: "response.completed"}},
		{Key: "input_token_count", Value: otelAttrValue{IntValue: json.RawMessage("0")}},
		{Key: "output_token_count", Value: otelAttrValue{IntValue: json.RawMessage("0")}},
		{Key: "cached_token_count", Value: otelAttrValue{IntValue: json.RawMessage("0")}},
	})
	p.OnLogs(body)

	select {
	case ev := <-events:
		t.Errorf("unexpected event for zero-token completed SSE: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// OK
	}
}

func TestEventHandler_ToolResult(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewEventHandler(events)

	body := makeLogsPayload("codex.tool_result", []otelAttribute{
		{Key: "tool_name", Value: otelAttrValue{StringValue: "shell"}},
		{Key: "call_id", Value: otelAttrValue{StringValue: "call-abc"}},
		{Key: "duration_ms", Value: otelAttrValue{IntValue: json.RawMessage("1500")}},
		{Key: "success", Value: otelAttrValue{StringValue: "true"}},
	})
	p.OnLogs(body)

	got := drainEvents(events, 1)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Type != monitor.EventToolCompleted {
		t.Fatalf("Type = %v, want EventToolCompleted", got[0].Type)
	}
	data := got[0].Data.(monitor.ToolCompletedData)
	if data.ToolName != "shell" {
		t.Errorf("ToolName = %q, want %q", data.ToolName, "shell")
	}
	if data.CallID != "call-abc" {
		t.Errorf("CallID = %q, want %q", data.CallID, "call-abc")
	}
	if data.DurationMs != 1500 {
		t.Errorf("DurationMs = %d, want 1500", data.DurationMs)
	}
	if !data.Success {
		t.Error("Success = false, want true")
	}
}

func TestEventHandler_ToolResult_Failure(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewEventHandler(events)

	body := makeLogsPayload("codex.tool_result", []otelAttribute{
		{Key: "tool_name", Value: otelAttrValue{StringValue: "shell"}},
		{Key: "success", Value: otelAttrValue{StringValue: "false"}},
	})
	p.OnLogs(body)

	got := drainEvents(events, 1)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	data := got[0].Data.(monitor.ToolCompletedData)
	if data.Success {
		t.Error("Success = true, want false")
	}
}

func TestEventHandler_ToolDecision_AskUser(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewEventHandler(events)

	body := makeLogsPayload("codex.tool_decision", []otelAttribute{
		{Key: "tool_name", Value: otelAttrValue{StringValue: "shell"}},
		{Key: "call_id", Value: otelAttrValue{StringValue: "call-xyz"}},
		{Key: "decision", Value: otelAttrValue{StringValue: "ask_user"}},
	})
	p.OnLogs(body)

	got := drainEvents(events, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	if got[0].Type != monitor.EventApprovalRequested {
		t.Fatalf("Type = %v, want EventApprovalRequested", got[0].Type)
	}
	if got[1].Type != monitor.EventStateChange {
		t.Fatalf("Type = %v, want EventStateChange", got[1].Type)
	}
	data := got[0].Data.(monitor.ApprovalRequestedData)
	if data.ToolName != "shell" {
		t.Errorf("ToolName = %q, want %q", data.ToolName, "shell")
	}
	if data.CallID != "call-xyz" {
		t.Errorf("CallID = %q, want %q", data.CallID, "call-xyz")
	}
	state := got[1].Data.(monitor.StateChangeData)
	if state.State != monitor.StateActive || state.SubState != monitor.SubStateWaitingForPermission {
		t.Errorf("state = (%v,%v), want (Active,WaitingForPermission)", state.State, state.SubState)
	}
}

func TestEventHandler_ToolDecision_Approved_EmitsToolStarted(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewEventHandler(events)

	body := makeLogsPayload("codex.tool_decision", []otelAttribute{
		{Key: "tool_name", Value: otelAttrValue{StringValue: "shell"}},
		{Key: "call_id", Value: otelAttrValue{StringValue: "call-approve-1"}},
		{Key: "decision", Value: otelAttrValue{StringValue: "approved"}},
	})
	p.OnLogs(body)

	got := drainEvents(events, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	if got[0].Type != monitor.EventToolStarted {
		t.Fatalf("Type = %v, want EventToolStarted", got[0].Type)
	}
	if got[1].Type != monitor.EventStateChange {
		t.Fatalf("Type = %v, want EventStateChange", got[1].Type)
	}
	data := got[0].Data.(monitor.ToolStartedData)
	if data.ToolName != "shell" {
		t.Errorf("ToolName = %q, want %q", data.ToolName, "shell")
	}
	if data.CallID != "call-approve-1" {
		t.Errorf("CallID = %q, want %q", data.CallID, "call-approve-1")
	}
	state := got[1].Data.(monitor.StateChangeData)
	if state.State != monitor.StateActive || state.SubState != monitor.SubStateToolUse {
		t.Errorf("state = (%v,%v), want (Active,ToolUse)", state.State, state.SubState)
	}
}

func TestEventHandler_SSECompleted_DoesNotIdleDuringToolUse(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewEventHandler(events)

	p.OnLogs(makeLogsPayload("codex.tool_decision", []otelAttribute{
		{Key: "tool_name", Value: otelAttrValue{StringValue: "shell"}},
		{Key: "call_id", Value: otelAttrValue{StringValue: "call-tool-1"}},
		{Key: "decision", Value: otelAttrValue{StringValue: "approved"}},
	}))

	p.OnLogs(makeLogsPayload("codex.sse_event", []otelAttribute{
		{Key: "event.kind", Value: otelAttrValue{StringValue: "response.completed"}},
		{Key: "input_token_count", Value: otelAttrValue{IntValue: json.RawMessage("100")}},
		{Key: "output_token_count", Value: otelAttrValue{IntValue: json.RawMessage("20")}},
		{Key: "cached_token_count", Value: otelAttrValue{IntValue: json.RawMessage("50")}},
	}))

	got := drainEvents(events, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d", len(got))
	}
	if got[0].Type != monitor.EventToolStarted {
		t.Fatalf("event[0].Type = %v, want EventToolStarted", got[0].Type)
	}
	if got[1].Type != monitor.EventStateChange {
		t.Fatalf("event[1].Type = %v, want EventStateChange", got[1].Type)
	}
	state := got[1].Data.(monitor.StateChangeData)
	if state.State != monitor.StateActive || state.SubState != monitor.SubStateToolUse {
		t.Fatalf("tool_use state = (%v,%v), want (Active,ToolUse)", state.State, state.SubState)
	}
	if got[2].Type != monitor.EventTurnCompleted {
		t.Fatalf("event[2].Type = %v, want EventTurnCompleted", got[2].Type)
	}

	select {
	case ev := <-events:
		t.Fatalf("unexpected extra event while in tool_use: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// OK: no idle state emitted.
	}
}

func TestEventHandler_SSECompleted_DebouncesIdleAndCancelsOnToolUse(t *testing.T) {
	old := codexIdleDebounceDelay
	codexIdleDebounceDelay = 30 * time.Millisecond
	defer func() { codexIdleDebounceDelay = old }()

	events := make(chan monitor.AgentEvent, 64)
	p := NewEventHandler(events)

	// Move parser state to active/thinking.
	p.OnLogs(makeLogsPayload("codex.user_prompt", nil))
	_ = drainEvents(events, 2)

	// Completed response schedules idle debounce.
	p.OnLogs(makeLogsPayload("codex.sse_event", []otelAttribute{
		{Key: "event.kind", Value: otelAttrValue{StringValue: "response.completed"}},
		{Key: "input_token_count", Value: otelAttrValue{IntValue: json.RawMessage("100")}},
		{Key: "output_token_count", Value: otelAttrValue{IntValue: json.RawMessage("20")}},
		{Key: "cached_token_count", Value: otelAttrValue{IntValue: json.RawMessage("50")}},
	}))
	got := drainEvents(events, 1)
	if len(got) != 1 || got[0].Type != monitor.EventTurnCompleted {
		t.Fatalf("expected immediate turn_completed, got %+v", got)
	}

	// Tool approval arrives inside debounce window and must cancel pending idle.
	p.OnLogs(makeLogsPayload("codex.tool_decision", []otelAttribute{
		{Key: "tool_name", Value: otelAttrValue{StringValue: "shell"}},
		{Key: "call_id", Value: otelAttrValue{StringValue: "call-race-1"}},
		{Key: "decision", Value: otelAttrValue{StringValue: "approved"}},
	}))
	got = drainEvents(events, 2)
	if len(got) != 2 {
		t.Fatalf("expected tool_started + state_change, got %d", len(got))
	}

	time.Sleep(60 * time.Millisecond)
	select {
	case ev := <-events:
		t.Fatalf("unexpected event after debounce cancellation: %+v", ev)
	default:
	}
}

func TestEventHandler_ToolDecision_Allow_NoEmit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewEventHandler(events)

	body := makeLogsPayload("codex.tool_decision", []otelAttribute{
		{Key: "tool_name", Value: otelAttrValue{StringValue: "shell"}},
		{Key: "decision", Value: otelAttrValue{StringValue: "allow"}},
	})
	p.OnLogs(body)

	select {
	case ev := <-events:
		t.Errorf("unexpected event for allow decision: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// OK — only ask_user emits an event.
	}
}

func TestEventHandler_UnknownSpan_NoEmit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewEventHandler(events)

	body := makeLogsPayload("codex.api_request", []otelAttribute{
		{Key: "duration_ms", Value: otelAttrValue{IntValue: json.RawMessage("200")}},
	})
	p.OnLogs(body)

	select {
	case ev := <-events:
		t.Errorf("unexpected event for api_request: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// OK
	}
}

func TestEventHandler_InvalidJSON_NoEmit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewEventHandler(events)

	p.OnLogs([]byte("not json"))

	select {
	case ev := <-events:
		t.Errorf("unexpected event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// OK
	}
}

func TestEventHandler_MultipleSpans(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewEventHandler(events)

	payload := otelLogsPayload{
		ResourceLogs: []otelResourceLogs{{
			ScopeLogs: []otelScopeLogs{{
				LogRecords: []otelLogRecord{
					{
						Attributes: []otelAttribute{
							{Key: "event.name", Value: otelAttrValue{StringValue: "codex.conversation_starts"}},
							{Key: "conversation.id", Value: otelAttrValue{StringValue: "c1"}},
							{Key: "model", Value: otelAttrValue{StringValue: "o3"}},
						},
					},
					{
						Attributes: []otelAttribute{
							{Key: "event.name", Value: otelAttrValue{StringValue: "codex.user_prompt"}},
							{Key: "prompt_length", Value: otelAttrValue{IntValue: json.RawMessage("10")}},
						},
					},
				},
			}},
		}},
	}
	body, _ := json.Marshal(payload)
	p.OnLogs(body)

	got := drainEvents(events, 4)
	if len(got) != 4 {
		t.Fatalf("expected 4 events, got %d", len(got))
	}
	if got[0].Type != monitor.EventSessionStarted {
		t.Errorf("event[0].Type = %v, want EventSessionStarted", got[0].Type)
	}
	if got[1].Type != monitor.EventStateChange {
		t.Errorf("event[1].Type = %v, want EventStateChange", got[1].Type)
	}
	if got[2].Type != monitor.EventUserPrompt {
		t.Errorf("event[2].Type = %v, want EventUserPrompt", got[2].Type)
	}
	if got[3].Type != monitor.EventStateChange {
		t.Errorf("event[3].Type = %v, want EventStateChange", got[3].Type)
	}
}
