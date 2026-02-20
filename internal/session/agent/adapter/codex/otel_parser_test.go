package codex

import (
	"encoding/json"
	"testing"
	"time"

	"h2/internal/session/agent/monitor"
)

// makeTracePayload builds an OTEL traces payload with a single span.
func makeTracePayload(name string, attrs []otelAttribute) []byte {
	payload := otelTracesPayload{
		ResourceSpans: []otelResourceSpans{{
			ScopeSpans: []otelScopeSpans{{
				Spans: []otelSpan{{
					Name:       name,
					Attributes: attrs,
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

func TestOtelParser_ConversationStarts(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewOtelParser(events)

	body := makeTracePayload("codex.conversation_starts", []otelAttribute{
		{Key: "conversation.id", Value: otelAttrValue{StringValue: "conv-123"}},
		{Key: "model", Value: otelAttrValue{StringValue: "o3"}},
	})
	p.OnTraces(body)

	got := drainEvents(events, 1)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Type != monitor.EventSessionStarted {
		t.Fatalf("Type = %v, want EventSessionStarted", got[0].Type)
	}
	data := got[0].Data.(monitor.SessionStartedData)
	if data.ThreadID != "conv-123" {
		t.Errorf("ThreadID = %q, want %q", data.ThreadID, "conv-123")
	}
	if data.Model != "o3" {
		t.Errorf("Model = %q, want %q", data.Model, "o3")
	}
}

func TestOtelParser_UserPrompt(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewOtelParser(events)

	body := makeTracePayload("codex.user_prompt", []otelAttribute{
		{Key: "prompt_length", Value: otelAttrValue{IntValue: json.RawMessage("42")}},
	})
	p.OnTraces(body)

	got := drainEvents(events, 1)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Type != monitor.EventTurnStarted {
		t.Fatalf("Type = %v, want EventTurnStarted", got[0].Type)
	}
}

func TestOtelParser_SSEEvent_Completed(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewOtelParser(events)

	body := makeTracePayload("codex.sse_event", []otelAttribute{
		{Key: "event.kind", Value: otelAttrValue{StringValue: "response.completed"}},
		{Key: "input_token_count", Value: otelAttrValue{IntValue: json.RawMessage("500")}},
		{Key: "output_token_count", Value: otelAttrValue{IntValue: json.RawMessage("300")}},
		{Key: "cached_token_count", Value: otelAttrValue{IntValue: json.RawMessage("100")}},
	})
	p.OnTraces(body)

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

func TestOtelParser_SSEEvent_NonCompleted_NoEmit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewOtelParser(events)

	body := makeTracePayload("codex.sse_event", []otelAttribute{
		{Key: "event.kind", Value: otelAttrValue{StringValue: "response.created"}},
	})
	p.OnTraces(body)

	select {
	case ev := <-events:
		t.Errorf("unexpected event for non-completed SSE: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// OK
	}
}

func TestOtelParser_ToolResult(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewOtelParser(events)

	body := makeTracePayload("codex.tool_result", []otelAttribute{
		{Key: "tool_name", Value: otelAttrValue{StringValue: "shell"}},
		{Key: "call_id", Value: otelAttrValue{StringValue: "call-abc"}},
		{Key: "duration_ms", Value: otelAttrValue{IntValue: json.RawMessage("1500")}},
		{Key: "success", Value: otelAttrValue{StringValue: "true"}},
	})
	p.OnTraces(body)

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

func TestOtelParser_ToolResult_Failure(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewOtelParser(events)

	body := makeTracePayload("codex.tool_result", []otelAttribute{
		{Key: "tool_name", Value: otelAttrValue{StringValue: "shell"}},
		{Key: "success", Value: otelAttrValue{StringValue: "false"}},
	})
	p.OnTraces(body)

	got := drainEvents(events, 1)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	data := got[0].Data.(monitor.ToolCompletedData)
	if data.Success {
		t.Error("Success = true, want false")
	}
}

func TestOtelParser_ToolDecision_AskUser(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewOtelParser(events)

	body := makeTracePayload("codex.tool_decision", []otelAttribute{
		{Key: "tool_name", Value: otelAttrValue{StringValue: "shell"}},
		{Key: "call_id", Value: otelAttrValue{StringValue: "call-xyz"}},
		{Key: "decision", Value: otelAttrValue{StringValue: "ask_user"}},
	})
	p.OnTraces(body)

	got := drainEvents(events, 1)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Type != monitor.EventApprovalRequested {
		t.Fatalf("Type = %v, want EventApprovalRequested", got[0].Type)
	}
	data := got[0].Data.(monitor.ApprovalRequestedData)
	if data.ToolName != "shell" {
		t.Errorf("ToolName = %q, want %q", data.ToolName, "shell")
	}
	if data.CallID != "call-xyz" {
		t.Errorf("CallID = %q, want %q", data.CallID, "call-xyz")
	}
}

func TestOtelParser_ToolDecision_Allow_NoEmit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewOtelParser(events)

	body := makeTracePayload("codex.tool_decision", []otelAttribute{
		{Key: "tool_name", Value: otelAttrValue{StringValue: "shell"}},
		{Key: "decision", Value: otelAttrValue{StringValue: "allow"}},
	})
	p.OnTraces(body)

	select {
	case ev := <-events:
		t.Errorf("unexpected event for allow decision: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// OK — only ask_user emits an event.
	}
}

func TestOtelParser_UnknownSpan_NoEmit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewOtelParser(events)

	body := makeTracePayload("codex.api_request", []otelAttribute{
		{Key: "duration_ms", Value: otelAttrValue{IntValue: json.RawMessage("200")}},
	})
	p.OnTraces(body)

	select {
	case ev := <-events:
		t.Errorf("unexpected event for api_request: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// OK
	}
}

func TestOtelParser_InvalidJSON_NoEmit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewOtelParser(events)

	p.OnTraces([]byte("not json"))

	select {
	case ev := <-events:
		t.Errorf("unexpected event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// OK
	}
}

func TestOtelParser_MultipleSpans(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewOtelParser(events)

	payload := otelTracesPayload{
		ResourceSpans: []otelResourceSpans{{
			ScopeSpans: []otelScopeSpans{{
				Spans: []otelSpan{
					{
						Name: "codex.conversation_starts",
						Attributes: []otelAttribute{
							{Key: "conversation.id", Value: otelAttrValue{StringValue: "c1"}},
							{Key: "model", Value: otelAttrValue{StringValue: "o3"}},
						},
					},
					{
						Name: "codex.user_prompt",
						Attributes: []otelAttribute{
							{Key: "prompt_length", Value: otelAttrValue{IntValue: json.RawMessage("10")}},
						},
					},
				},
			}},
		}},
	}
	body, _ := json.Marshal(payload)
	p.OnTraces(body)

	got := drainEvents(events, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	if got[0].Type != monitor.EventSessionStarted {
		t.Errorf("event[0].Type = %v, want EventSessionStarted", got[0].Type)
	}
	if got[1].Type != monitor.EventTurnStarted {
		t.Errorf("event[1].Type = %v, want EventTurnStarted", got[1].Type)
	}
}
