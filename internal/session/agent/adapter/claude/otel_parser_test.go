package claude

import (
	"encoding/json"
	"testing"
	"time"

	"h2/internal/session/agent/monitor"
)

func TestOtelParser_APIRequest(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewOtelParser(events)

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
	p.OnLogs(body)

	select {
	case ev := <-events:
		if ev.Type != monitor.EventTurnCompleted {
			t.Fatalf("Type = %v, want EventTurnCompleted", ev.Type)
		}
		data := ev.Data.(monitor.TurnCompletedData)
		if data.InputTokens != 100 {
			t.Errorf("InputTokens = %d, want 100", data.InputTokens)
		}
		if data.OutputTokens != 200 {
			t.Errorf("OutputTokens = %d, want 200", data.OutputTokens)
		}
		if data.CostUSD != 0.05 {
			t.Errorf("CostUSD = %f, want 0.05", data.CostUSD)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestOtelParser_ToolResult(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewOtelParser(events)

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
	p.OnLogs(body)

	select {
	case ev := <-events:
		if ev.Type != monitor.EventToolCompleted {
			t.Fatalf("Type = %v, want EventToolCompleted", ev.Type)
		}
		data := ev.Data.(monitor.ToolCompletedData)
		if data.ToolName != "Read" {
			t.Errorf("ToolName = %q, want %q", data.ToolName, "Read")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestOtelParser_UnknownEvent_NoEmit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewOtelParser(events)

	payload := otelLogsPayload{
		ResourceLogs: []otelResourceLogs{{
			ScopeLogs: []otelScopeLogs{{
				LogRecords: []otelLogRecord{{
					Attributes: []otelAttribute{
						{Key: "event.name", Value: otelAttrValue{StringValue: "unknown_event"}},
					},
				}},
			}},
		}},
	}

	body, _ := json.Marshal(payload)
	p.OnLogs(body)

	select {
	case ev := <-events:
		t.Errorf("unexpected event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// OK, no event emitted.
	}
}

func TestOtelParser_InvalidJSON_NoEmit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	p := NewOtelParser(events)

	p.OnLogs([]byte("not json"))

	select {
	case ev := <-events:
		t.Errorf("unexpected event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// OK
	}
}
