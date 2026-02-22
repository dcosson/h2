package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"h2/internal/session/agent/monitor"
	"h2/internal/session/agent/shared/sessionlogcollector"
)

func TestEventHandler_APIRequest(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	payload := otelLogsPayload{
		ResourceLogs: []otelResourceLogs{{
			ScopeLogs: []otelScopeLogs{{
				LogRecords: []otelLogRecord{{
					Attributes: []otelAttribute{
						{Key: "event.name", Value: otelAttrValue{StringValue: "api_request"}},
						{Key: "input_tokens", Value: otelAttrValue{IntValue: json.RawMessage("100")}},
						{Key: "output_tokens", Value: otelAttrValue{IntValue: json.RawMessage("200")}},
						{Key: "cost_usd", Value: otelAttrValue{StringValue: "0.05"}},
					},
				}},
			}},
		}},
	}

	body, _ := json.Marshal(payload)
	h.OnLogs(body)

	select {
	case ev := <-events:
		if ev.Type != monitor.EventTurnCompleted {
			t.Fatalf("Type = %v, want EventTurnCompleted", ev.Type)
		}
		data := ev.Data.(monitor.TurnCompletedData)
		if data.InputTokens != 100 || data.OutputTokens != 200 || data.CostUSD != 0.05 {
			t.Fatalf("unexpected turn data: %+v", data)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestEventHandler_ToolResult(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	payload := otelLogsPayload{
		ResourceLogs: []otelResourceLogs{{
			ScopeLogs: []otelScopeLogs{{
				LogRecords: []otelLogRecord{{
					Attributes: []otelAttribute{
						{Key: "event.name", Value: otelAttrValue{StringValue: "tool_result"}},
						{Key: "tool_name", Value: otelAttrValue{StringValue: "Read"}},
					},
				}},
			}},
		}},
	}

	body, _ := json.Marshal(payload)
	h.OnLogs(body)

	select {
	case ev := <-events:
		if ev.Type != monitor.EventToolCompleted {
			t.Fatalf("Type = %v, want EventToolCompleted", ev.Type)
		}
		if ev.Data.(monitor.ToolCompletedData).ToolName != "Read" {
			t.Fatalf("unexpected tool data: %+v", ev.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestEventHandler_UnknownEvent_NoEmit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	payload := otelLogsPayload{
		ResourceLogs: []otelResourceLogs{{
			ScopeLogs: []otelScopeLogs{{
				LogRecords: []otelLogRecord{{
					Attributes: []otelAttribute{{Key: "event.name", Value: otelAttrValue{StringValue: "unknown_event"}}},
				}},
			}},
		}},
	}
	body, _ := json.Marshal(payload)
	h.OnLogs(body)

	select {
	case ev := <-events:
		t.Errorf("unexpected event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestEventHandler_InvalidJSON_NoEmit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)
	h.OnLogs([]byte("not json"))

	select {
	case ev := <-events:
		t.Errorf("unexpected event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestEventHandler_PreToolUse(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	payload, _ := json.Marshal(map[string]string{"tool_name": "Bash", "session_id": "s1"})
	h.ProcessHookEvent("PreToolUse", payload)

	got := drainEvents(events, 2)
	if got[0].Type != monitor.EventToolStarted {
		t.Fatalf("event[0].Type = %v, want EventToolStarted", got[0].Type)
	}
	if got[1].Type != monitor.EventStateChange {
		t.Fatalf("event[1].Type = %v, want EventStateChange", got[1].Type)
	}
	sc := got[1].Data.(monitor.StateChangeData)
	if sc.SubState != monitor.SubStateToolUse {
		t.Fatalf("SubState = %v, want ToolUse", sc.SubState)
	}
}

func TestEventHandler_PermissionRequest(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)
	h.ProcessHookEvent("PermissionRequest", nil)

	got := drainEvents(events, 2)
	if got[0].Type != monitor.EventApprovalRequested {
		t.Fatalf("event[0].Type = %v, want EventApprovalRequested", got[0].Type)
	}
	sc := got[1].Data.(monitor.StateChangeData)
	if sc.SubState != monitor.SubStateWaitingForPermission {
		t.Fatalf("SubState = %v, want WaitingForPermission", sc.SubState)
	}
}

func TestEventHandler_SessionStart_Idle(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)
	h.ProcessHookEvent("SessionStart", nil)

	got := drainEvents(events, 1)
	sc := got[0].Data.(monitor.StateChangeData)
	if sc.State != monitor.StateIdle {
		t.Fatalf("State = %v, want Idle", sc.State)
	}
}

func TestEventHandler_OnSessionLogLine(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)
	line, _ := json.Marshal(map[string]any{
		"type":    "assistant",
		"message": map[string]string{"role": "assistant", "content": "Hi there!"},
	})
	h.OnSessionLogLine(line)

	select {
	case ev := <-events:
		if ev.Type != monitor.EventAgentMessage {
			t.Fatalf("Type = %v, want EventAgentMessage", ev.Type)
		}
		if ev.Data.(monitor.AgentMessageData).Content != "Hi there!" {
			t.Fatalf("unexpected content: %+v", ev.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestEventHandler_SessionLogCollector_EmitsAssistantMessages(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.jsonl")

	entries := []map[string]any{
		{"type": "user", "message": map[string]string{"role": "user", "content": "hello"}},
		{"type": "assistant", "message": map[string]string{"role": "assistant", "content": "Hi there!"}},
		{"type": "assistant", "message": map[string]string{"role": "assistant", "content": "How can I help?"}},
	}
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, _ := json.Marshal(e)
		f.Write(data)
		f.Write([]byte("\n"))
	}
	f.Close()

	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sessionlogcollector.New(logPath, h.OnSessionLogLine).Run(ctx)

	var got []monitor.AgentEvent
	timeout := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case ev := <-events:
			got = append(got, ev)
		case <-timeout:
			t.Fatalf("timed out, got %d events, want 2", len(got))
		}
	}

	if got[0].Data.(monitor.AgentMessageData).Content != "Hi there!" {
		t.Fatalf("event[0].Data = %v, want 'Hi there!'", got[0].Data)
	}
	if got[1].Data.(monitor.AgentMessageData).Content != "How can I help?" {
		t.Fatalf("event[1].Data = %v, want 'How can I help?'", got[1].Data)
	}
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
