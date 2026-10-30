package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestEventHandler_APIError_429_UsageLimit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	payload := otelLogsPayload{
		ResourceLogs: []otelResourceLogs{{
			ScopeLogs: []otelScopeLogs{{
				LogRecords: []otelLogRecord{{
					Attributes: []otelAttribute{
						{Key: "event.name", Value: otelAttrValue{StringValue: "api_error"}},
						{Key: "status_code", Value: otelAttrValue{StringValue: "429"}},
						{Key: "error", Value: otelAttrValue{StringValue: "usage limit reached"}},
					},
				}},
			}},
		}},
	}
	body, _ := json.Marshal(payload)
	h.OnLogs(body)

	got := drainEvents(events, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	if got[0].Type != monitor.EventStateChange {
		t.Fatalf("Type = %v, want EventStateChange", got[0].Type)
	}
	state := got[0].Data.(monitor.StateChangeData)
	if state.State != monitor.StateIdle || state.SubState != monitor.SubStateUsageLimit {
		t.Errorf("state = (%v,%v), want (Idle,UsageLimit)", state.State, state.SubState)
	}
	if got[1].Type != monitor.EventUsageLimitInfo {
		t.Fatalf("event[1].Type = %v, want EventUsageLimitInfo", got[1].Type)
	}
	ul := got[1].Data.(monitor.UsageLimitData)
	if !ul.ResetsAt.IsZero() {
		t.Fatalf("ResetsAt = %v, want zero when Claude provides no reset time", ul.ResetsAt)
	}
	if !strings.Contains(ul.Message, "usage limit") {
		t.Fatalf("unexpected message: %q", ul.Message)
	}
}

func TestEventHandler_APIError_429_SessionLimit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	payload := otelLogsPayload{
		ResourceLogs: []otelResourceLogs{{
			ScopeLogs: []otelScopeLogs{{
				LogRecords: []otelLogRecord{{
					Attributes: []otelAttribute{
						{Key: "event.name", Value: otelAttrValue{StringValue: "api_error"}},
						{Key: "status_code", Value: otelAttrValue{StringValue: "429"}},
						{Key: "error", Value: otelAttrValue{StringValue: "You've hit your session limit · resets 6pm (America/Los_Angeles)"}},
					},
				}},
			}},
		}},
	}
	body, _ := json.Marshal(payload)
	h.OnLogs(body)

	got := drainEvents(events, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 events (state_change + usage_limit_info), got %d", len(got))
	}
	state := got[0].Data.(monitor.StateChangeData)
	if state.State != monitor.StateIdle || state.SubState != monitor.SubStateUsageLimit {
		t.Errorf("state = (%v,%v), want (Idle,UsageLimit)", state.State, state.SubState)
	}
	if got[1].Type != monitor.EventUsageLimitInfo {
		t.Fatalf("event[1].Type = %v, want EventUsageLimitInfo", got[1].Type)
	}
	ul := got[1].Data.(monitor.UsageLimitData)
	if ul.ResetsAt.IsZero() {
		t.Fatal("ResetsAt should be parsed from 'resets 6pm (America/Los_Angeles)'")
	}
}

func TestEventHandler_APIError_500_ServerError(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	payload := otelLogsPayload{
		ResourceLogs: []otelResourceLogs{{
			ScopeLogs: []otelScopeLogs{{
				LogRecords: []otelLogRecord{{
					Attributes: []otelAttribute{
						{Key: "event.name", Value: otelAttrValue{StringValue: "api_error"}},
						{Key: "status_code", Value: otelAttrValue{StringValue: "500"}},
						{Key: "error", Value: otelAttrValue{StringValue: "internal server error"}},
					},
				}},
			}},
		}},
	}
	body, _ := json.Marshal(payload)
	h.OnLogs(body)

	got := drainEvents(events, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 events (state_change + server_error_info), got %d", len(got))
	}
	if got[0].Type != monitor.EventStateChange {
		t.Fatalf("event[0].Type = %v, want EventStateChange", got[0].Type)
	}
	state := got[0].Data.(monitor.StateChangeData)
	if state.State != monitor.StateIdle || state.SubState != monitor.SubStateServerError {
		t.Errorf("state = (%v,%v), want (Idle,ServerError)", state.State, state.SubState)
	}
	if got[1].Type != monitor.EventServerErrorInfo {
		t.Fatalf("event[1].Type = %v, want EventServerErrorInfo", got[1].Type)
	}
	data := got[1].Data.(monitor.ServerErrorData)
	if data.StatusCode != "500" {
		t.Errorf("StatusCode = %q, want %q", data.StatusCode, "500")
	}
}

func TestEventHandler_APIError_502_ServerError(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	payload := otelLogsPayload{
		ResourceLogs: []otelResourceLogs{{
			ScopeLogs: []otelScopeLogs{{
				LogRecords: []otelLogRecord{{
					Attributes: []otelAttribute{
						{Key: "event.name", Value: otelAttrValue{StringValue: "api_error"}},
						{Key: "status_code", Value: otelAttrValue{StringValue: "502"}},
						{Key: "error", Value: otelAttrValue{StringValue: "bad gateway"}},
					},
				}},
			}},
		}},
	}
	body, _ := json.Marshal(payload)
	h.OnLogs(body)

	got := drainEvents(events, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	state := got[0].Data.(monitor.StateChangeData)
	if state.SubState != monitor.SubStateServerError {
		t.Errorf("SubState = %v, want ServerError", state.SubState)
	}
	data := got[1].Data.(monitor.ServerErrorData)
	if data.StatusCode != "502" {
		t.Errorf("StatusCode = %q, want %q", data.StatusCode, "502")
	}
}

func TestEventHandler_APIError_Non4xx5xx_NoEmit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	payload := otelLogsPayload{
		ResourceLogs: []otelResourceLogs{{
			ScopeLogs: []otelScopeLogs{{
				LogRecords: []otelLogRecord{{
					Attributes: []otelAttribute{
						{Key: "event.name", Value: otelAttrValue{StringValue: "api_error"}},
						{Key: "status_code", Value: otelAttrValue{StringValue: "400"}},
						{Key: "error", Value: otelAttrValue{StringValue: "bad request"}},
					},
				}},
			}},
		}},
	}
	body, _ := json.Marshal(payload)
	h.OnLogs(body)

	select {
	case ev := <-events:
		t.Errorf("unexpected event for 400 api_error: %+v", ev)
	case <-time.After(50 * time.Millisecond):
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

func TestEventHandler_ConfigureDebug_Enabled(t *testing.T) {
	t.Setenv("H2_OTEL_DEBUG_LOGGING_ENABLED", "1")
	t.Setenv("OTEL_DEBUG_LOGGING_ENABLED", "")

	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)
	debugPath := filepath.Join(t.TempDir(), "claude-otel-debug.log")
	h.ConfigureDebug(debugPath)
	h.OnMetrics([]byte(`{"resourceMetrics":[]}`))

	data, err := os.ReadFile(debugPath)
	if err != nil {
		t.Fatalf("read debug log: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "startup parser=claude_otel") {
		t.Fatalf("missing startup line: %q", s)
	}
	if !strings.Contains(s, "received /v1/metrics payload bytes=") {
		t.Fatalf("missing metrics line: %q", s)
	}
}

func TestEventHandler_ConfigureDebug_Disabled(t *testing.T) {
	t.Setenv("H2_OTEL_DEBUG_LOGGING_ENABLED", "")
	t.Setenv("OTEL_DEBUG_LOGGING_ENABLED", "")

	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)
	debugPath := filepath.Join(t.TempDir(), "claude-otel-debug.log")
	h.ConfigureDebug(debugPath)
	h.OnMetrics([]byte(`{"resourceMetrics":[]}`))

	if _, err := os.Stat(debugPath); !os.IsNotExist(err) {
		t.Fatalf("expected no debug log file when disabled, got err=%v", err)
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
	if sc.SubState != monitor.SubStatePermissionReview {
		t.Fatalf("SubState = %v, want PermissionReview", sc.SubState)
	}
}

func TestEventHandler_PermissionDecisionAllow(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	payload, _ := json.Marshal(map[string]string{
		"decision": "allow", "reason": "safe", "processed_by": "dcg", "role": "default",
	})
	h.ProcessHookEvent("permission_decision", payload)

	got := drainEvents(events, 2)
	pd := got[0].Data.(monitor.PermissionDecisionData)
	if pd.Decision != "allow" || pd.ProcessedBy != "dcg" || pd.Role != "default" {
		t.Fatalf("PermissionDecisionData = %+v", pd)
	}
	sc := got[1].Data.(monitor.StateChangeData)
	if sc.SubState != monitor.SubStateToolUse {
		t.Fatalf("SubState = %v, want ToolUse", sc.SubState)
	}
}

func TestEventHandler_PermissionDecisionAskUser(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	payload, _ := json.Marshal(map[string]string{"decision": "ask_user", "processed_by": "none"})
	h.ProcessHookEvent("permission_decision", payload)

	got := drainEvents(events, 2)
	pd := got[0].Data.(monitor.PermissionDecisionData)
	if pd.Decision != "ask_user" {
		t.Fatalf("Decision = %v, want ask_user", pd.Decision)
	}
	sc := got[1].Data.(monitor.StateChangeData)
	if sc.SubState != monitor.SubStateBlockedOnPermission {
		t.Fatalf("SubState = %v, want BlockedOnPermission", sc.SubState)
	}
}

func TestEventHandler_PermissionDecisionDeny(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	payload, _ := json.Marshal(map[string]string{
		"decision": "deny", "reason": "destructive", "processed_by": "ai_reviewer",
	})
	h.ProcessHookEvent("permission_decision", payload)

	got := drainEvents(events, 2)
	pd := got[0].Data.(monitor.PermissionDecisionData)
	if pd.Decision != "deny" || pd.Reason != "destructive" {
		t.Fatalf("PermissionDecisionData = %+v", pd)
	}
	sc := got[1].Data.(monitor.StateChangeData)
	if sc.SubState != monitor.SubStateThinking {
		t.Fatalf("SubState = %v, want Thinking", sc.SubState)
	}
}

func TestEventHandler_IgnoresMismatchedSessionHookEvents(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)
	h.SetExpectedSessionID("parent-session")

	payload, _ := json.Marshal(map[string]string{"tool_name": "Bash", "session_id": "reviewer-session"})
	if !h.ProcessHookEvent("PreToolUse", payload) {
		t.Fatal("expected PreToolUse to be recognized")
	}

	select {
	case ev := <-events:
		t.Fatalf("unexpected event emitted for mismatched session: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestEventHandler_IgnoresMismatchedSessionPermissionDecision(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)
	h.SetExpectedSessionID("parent-session")

	payload, _ := json.Marshal(map[string]string{"decision": "allow", "session_id": "reviewer-session"})
	if !h.ProcessHookEvent("permission_decision", payload) {
		t.Fatal("expected permission_decision to be recognized")
	}

	select {
	case ev := <-events:
		t.Fatalf("unexpected event emitted for mismatched session: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestEventHandler_PostToolUseFailure(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	payload, _ := json.Marshal(map[string]string{"tool_name": "Bash", "session_id": "s1"})
	h.ProcessHookEvent("PostToolUseFailure", payload)

	got := drainEvents(events, 2)
	if got[0].Type != monitor.EventToolCompleted {
		t.Fatalf("event[0].Type = %v, want EventToolCompleted", got[0].Type)
	}
	tc := got[0].Data.(monitor.ToolCompletedData)
	if tc.Success {
		t.Fatal("expected Success=false for PostToolUseFailure")
	}
	if tc.ToolName != "Bash" {
		t.Fatalf("ToolName = %q, want Bash", tc.ToolName)
	}
	if got[1].Type != monitor.EventStateChange {
		t.Fatalf("event[1].Type = %v, want EventStateChange", got[1].Type)
	}
	sc := got[1].Data.(monitor.StateChangeData)
	if sc.SubState != monitor.SubStateThinking {
		t.Fatalf("SubState = %v, want Thinking", sc.SubState)
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

func TestEventHandler_OnSessionLogLine_RateLimitWithResetTime(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	// Replicate the real Claude Code session JSONL format for rate limit messages.
	line, _ := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":    "assistant",
			"model":   "<synthetic>",
			"content": []map[string]string{{"type": "text", "text": "You've hit your limit · resets 12pm (America/Los_Angeles)"}},
		},
		"error":             "rate_limit",
		"isApiErrorMessage": true,
	})
	h.OnSessionLogLine(line)

	got := drainEvents(events, 3)
	if len(got) < 3 {
		t.Fatalf("expected 3 events (state_change + usage_limit_info + agent_message), got %d", len(got))
	}

	if got[0].Type != monitor.EventStateChange {
		t.Fatalf("event[0].Type = %v, want EventStateChange", got[0].Type)
	}
	state := got[0].Data.(monitor.StateChangeData)
	if state.State != monitor.StateIdle || state.SubState != monitor.SubStateUsageLimit {
		t.Fatalf("state = (%v,%v), want (Idle,UsageLimit)", state.State, state.SubState)
	}

	if got[1].Type != monitor.EventUsageLimitInfo {
		t.Fatalf("event[1].Type = %v, want EventUsageLimitInfo", got[1].Type)
	}
	data := got[1].Data.(monitor.UsageLimitData)
	if data.ResetsAt.IsZero() {
		t.Fatal("ResetsAt should not be zero")
	}
	if !strings.Contains(data.Message, "resets 12pm") {
		t.Fatalf("unexpected message: %q", data.Message)
	}

	// Verify the parsed time is in the right timezone and at noon.
	loc, _ := time.LoadLocation("America/Los_Angeles")
	inLA := data.ResetsAt.In(loc)
	if inLA.Hour() != 12 || inLA.Minute() != 0 {
		t.Fatalf("expected 12:00 PM LA time, got %v", inLA)
	}

	if got[2].Type != monitor.EventAgentMessage {
		t.Fatalf("event[2].Type = %v, want EventAgentMessage", got[2].Type)
	}
}

func TestEventHandler_OnSessionLogLine_UsageLimitNoResetTime(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	line, _ := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":    "assistant",
			"content": []map[string]string{{"type": "text", "text": "You've hit your org's monthly usage limit"}},
		},
		"error":             "rate_limit",
		"isApiErrorMessage": true,
		"apiErrorStatus":    429,
	})
	h.OnSessionLogLine(line)

	got := drainEvents(events, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d", len(got))
	}
	state := got[0].Data.(monitor.StateChangeData)
	if state.State != monitor.StateIdle || state.SubState != monitor.SubStateUsageLimit {
		t.Fatalf("state = (%v,%v), want (Idle,UsageLimit)", state.State, state.SubState)
	}
	if got[1].Type != monitor.EventUsageLimitInfo {
		t.Fatalf("event[1].Type = %v, want EventUsageLimitInfo", got[1].Type)
	}
	data := got[1].Data.(monitor.UsageLimitData)
	if !data.ResetsAt.IsZero() {
		t.Fatalf("ResetsAt = %v, want zero when Claude provides no reset time", data.ResetsAt)
	}
	if data.Message != "You've hit your org's monthly usage limit" {
		t.Fatalf("Message = %q", data.Message)
	}
	if got[2].Type != monitor.EventAgentMessage {
		t.Fatalf("event[2].Type = %v, want EventAgentMessage", got[2].Type)
	}
}

// TestEventHandler_OnSessionLogLine_SessionLimitRealShape feeds the verbatim
// session JSONL line Claude Code 2.1.206 writes when the account session limit
// is hit (captured from a real session on 2026-07-10, only UUIDs shortened).
func TestEventHandler_OnSessionLogLine_SessionLimitRealShape(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	line := `{"parentUuid":"864bea6c-bc3a-498a-a8dc-710fa8af0e7c","isSidechain":false,"type":"assistant","uuid":"6b596ec0-b5d6-4de1-af47-0666cd98ef33","timestamp":"2026-07-10T21:35:00.999Z","message":{"id":"27a26c22-3b28-42e3-a754-ac42abf01a10","container":null,"model":"<synthetic>","role":"assistant","stop_details":null,"stop_reason":"stop_sequence","stop_sequence":"","type":"message","usage":{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":null,"cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0},"inference_geo":null,"iterations":null,"speed":null},"content":[{"type":"text","text":"You've hit your session limit · resets 6pm (America/Los_Angeles)"}],"context_management":null},"requestId":"req_011Ccu6VkxhLpaRaY1FW5mxw","error":"rate_limit","isApiErrorMessage":true,"apiErrorStatus":429,"session_id":"546c6473-241c-4a9f-aa64-4c2c2b0da3c7","userType":"external","entrypoint":"cli","cwd":"/tmp/w","sessionId":"546c6473-241c-4a9f-aa64-4c2c2b0da3c7","version":"2.1.206","gitBranch":"HEAD"}`
	h.OnSessionLogLine([]byte(line))

	got := drainEvents(events, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 events (state_change + usage_limit_info + agent_message), got %d", len(got))
	}
	state := got[0].Data.(monitor.StateChangeData)
	if state.State != monitor.StateIdle || state.SubState != monitor.SubStateUsageLimit {
		t.Fatalf("state = (%v,%v), want (Idle,UsageLimit)", state.State, state.SubState)
	}
	if got[1].Type != monitor.EventUsageLimitInfo {
		t.Fatalf("event[1].Type = %v, want EventUsageLimitInfo", got[1].Type)
	}
	data := got[1].Data.(monitor.UsageLimitData)
	loc, _ := time.LoadLocation("America/Los_Angeles")
	inLA := data.ResetsAt.In(loc)
	if inLA.Hour() != 18 || inLA.Minute() != 0 {
		t.Fatalf("expected 6:00 PM LA time, got %v", inLA)
	}
}

// TestEventHandler_OnSessionLogLine_WeeklyLimitRealShape feeds the verbatim
// session JSONL line Claude Code writes when the seven-day limit is hit
// (captured from a real session on 2026-10-30, trailing fields trimmed). The
// wording is "weekly limit" and the reset carries a date, so the reset time
// must come from the structured quotaLimits.resetsAt epoch.
func TestEventHandler_OnSessionLogLine_WeeklyLimitRealShape(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	line := `{"parentUuid":"89624bd1-7de4-4d51-a361-1dd18a3d56e1","isSidechain":false,"type":"assistant","uuid":"c4d9a9e4-dc5a-45c2-9ff1-5bb465e357c8","timestamp":"2026-10-30T17:25:14.960Z","message":{"diagnostics":null,"id":"79e5c421-f7de-4168-a2f3-33337da2a564","container":null,"model":"<synthetic>","role":"assistant","stop_details":null,"stop_reason":"stop_sequence","stop_sequence":"","type":"message","usage":{"input_tokens":0,"output_tokens":0},"content":[{"type":"text","text":"You've hit your weekly limit · resets Nov 3 at 8pm (America/Los_Angeles)"}],"context_management":null},"requestId":"req_011CgYp3cfbtE8bcQcG92Seu","apiError":"usage_limit_reached","apiErrorParams":{"rate_limit_info":{"status":"rejected","resetsAt":1793764800,"rateLimitType":"seven_day","overageStatus":"rejected","overageDisabledReason":"out_of_credits","isUsingOverage":false}},"quotaLimits":{"status":"rejected","resetsAt":1793764800,"unifiedRateLimitFallbackAvailable":false,"rateLimitType":"seven_day","overageStatus":"rejected","overageDisabledReason":"out_of_credits","upgradePaths":["overage"],"isUsingOverage":false},"error":"rate_limit","isApiErrorMessage":true,"apiErrorStatus":429}`
	h.OnSessionLogLine([]byte(line))

	got := drainEvents(events, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 events (state_change + usage_limit_info + agent_message), got %d", len(got))
	}
	state := got[0].Data.(monitor.StateChangeData)
	if state.State != monitor.StateIdle || state.SubState != monitor.SubStateUsageLimit {
		t.Fatalf("state = (%v,%v), want (Idle,UsageLimit)", state.State, state.SubState)
	}
	if got[1].Type != monitor.EventUsageLimitInfo {
		t.Fatalf("event[1].Type = %v, want EventUsageLimitInfo", got[1].Type)
	}
	data := got[1].Data.(monitor.UsageLimitData)
	if want := time.Unix(1793764800, 0); !data.ResetsAt.Equal(want) {
		t.Fatalf("ResetsAt = %v, want %v", data.ResetsAt, want)
	}
}

// TestEventHandler_OnSessionLogLine_StructuredUsageLimitUnknownWording verifies
// that apiError=usage_limit_reached marks a usage limit even when the text is
// a wording isUsageLimitMessage does not know.
func TestEventHandler_OnSessionLogLine_StructuredUsageLimitUnknownWording(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	line, _ := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":    "assistant",
			"model":   "<synthetic>",
			"content": []map[string]string{{"type": "text", "text": "Out of quota for now"}},
		},
		"apiError":          "usage_limit_reached",
		"error":             "rate_limit",
		"isApiErrorMessage": true,
		"apiErrorStatus":    429,
	})
	h.OnSessionLogLine(line)

	got := drainEvents(events, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d", len(got))
	}
	state := got[0].Data.(monitor.StateChangeData)
	if state.State != monitor.StateIdle || state.SubState != monitor.SubStateUsageLimit {
		t.Fatalf("state = (%v,%v), want (Idle,UsageLimit)", state.State, state.SubState)
	}
	if got[1].Type != monitor.EventUsageLimitInfo {
		t.Fatalf("event[1].Type = %v, want EventUsageLimitInfo", got[1].Type)
	}
}

// TestEventHandler_OnSessionLogLine_SessionLimitNoResetTime covers the
// "session limit" wording alone (no "resets ..." suffix for the regex to
// match) — isUsageLimitMessage must recognize it directly.
func TestEventHandler_OnSessionLogLine_SessionLimitNoResetTime(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	line, _ := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":    "assistant",
			"model":   "<synthetic>",
			"content": []map[string]string{{"type": "text", "text": "You've hit your session limit"}},
		},
		"error":             "rate_limit",
		"isApiErrorMessage": true,
		"apiErrorStatus":    429,
	})
	h.OnSessionLogLine(line)

	got := drainEvents(events, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d", len(got))
	}
	state := got[0].Data.(monitor.StateChangeData)
	if state.State != monitor.StateIdle || state.SubState != monitor.SubStateUsageLimit {
		t.Fatalf("state = (%v,%v), want (Idle,UsageLimit)", state.State, state.SubState)
	}
	if got[1].Type != monitor.EventUsageLimitInfo {
		t.Fatalf("event[1].Type = %v, want EventUsageLimitInfo", got[1].Type)
	}
}

func TestIsUsageLimitMessage(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"You've hit your session limit · resets 6pm (America/Los_Angeles)", true},
		{"You've hit your session limit", true},
		{"Session limit reached · resets 3:50pm (America/Los_Angeles)", true},
		{"You've hit your usage limit.", true},
		{"You've hit your limit · resets 12pm (America/Los_Angeles)", true},
		{"You've hit your org's monthly usage limit", true},
		{"You've hit your weekly limit · resets Nov 3 at 8pm (America/Los_Angeles)", true},
		{"You've hit your Opus limit", true},
		{"usage_limit_reached", true},
		{"Claude usage limit reached|1783720800", true},
		{"API Error: Server is temporarily limiting requests (not your usage limit) · Rate limited", false},
		{"internal server error", false},
	}
	for _, tt := range tests {
		if got := isUsageLimitMessage(tt.msg); got != tt.want {
			t.Errorf("isUsageLimitMessage(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

func TestEventHandler_OnSessionLogLine_TemporaryRateLimitNotUsageLimit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	line, _ := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":    "assistant",
			"content": []map[string]string{{"type": "text", "text": "API Error: Server is temporarily limiting requests (not your usage limit) · Rate limited"}},
		},
		"error":             "rate_limit",
		"isApiErrorMessage": true,
		"apiErrorStatus":    429,
	})
	h.OnSessionLogLine(line)

	got := drainEvents(events, 1)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Type != monitor.EventAgentMessage {
		t.Fatalf("event[0].Type = %v, want EventAgentMessage", got[0].Type)
	}
}

func TestParseResetsAt(t *testing.T) {
	loc, _ := time.LoadLocation("America/Los_Angeles")

	tests := []struct {
		name         string
		message      string
		ref          time.Time
		wantOK       bool
		wantHour     int
		wantMin      int
		wantTZ       string
		wantTomorrow bool
	}{
		{
			name:     "noon reset",
			message:  "You've hit your limit · resets 12pm (America/Los_Angeles)",
			ref:      time.Date(2026, 3, 12, 10, 0, 0, 0, loc), // 10am, before noon
			wantOK:   true,
			wantHour: 12,
			wantMin:  0,
			wantTZ:   "America/Los_Angeles",
		},
		{
			name:         "noon reset but already past noon",
			message:      "resets 12pm (America/Los_Angeles)",
			ref:          time.Date(2026, 3, 12, 14, 0, 0, 0, loc), // 2pm, past noon
			wantOK:       true,
			wantHour:     12,
			wantMin:      0,
			wantTZ:       "America/Los_Angeles",
			wantTomorrow: true,
		},
		{
			name:     "5am reset",
			message:  "resets 5am (UTC)",
			ref:      time.Date(2026, 3, 12, 3, 0, 0, 0, time.UTC),
			wantOK:   true,
			wantHour: 5,
			wantMin:  0,
			wantTZ:   "UTC",
		},
		{
			name:     "with minutes",
			message:  "resets 5:30pm (America/New_York)",
			ref:      time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC),
			wantOK:   true,
			wantHour: 17,
			wantMin:  30,
			wantTZ:   "America/New_York",
		},
		{
			name:    "no match",
			message: "Something went wrong",
			ref:     time.Now(),
			wantOK:  false,
		},
		{
			name:    "bad timezone",
			message: "resets 12pm (Fake/Timezone)",
			ref:     time.Now(),
			wantOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseResetsAt(tt.message, tt.ref)
			if ok != tt.wantOK {
				t.Fatalf("parseResetsAt() ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}

			wantLoc, _ := time.LoadLocation(tt.wantTZ)
			inTZ := got.In(wantLoc)
			if inTZ.Hour() != tt.wantHour || inTZ.Minute() != tt.wantMin {
				t.Errorf("got %v, want %d:%02d in %s", inTZ, tt.wantHour, tt.wantMin, tt.wantTZ)
			}
			if !got.After(tt.ref) {
				t.Errorf("reset time %v should be after reference %v", got, tt.ref)
			}
			if tt.wantTomorrow {
				refInTZ := tt.ref.In(wantLoc)
				if inTZ.Day() == refInTZ.Day() {
					t.Errorf("expected tomorrow, but got same day: %v", inTZ)
				}
			}
		})
	}
}

// TestParseResetsAt_WithDate covers the dated reset form used by weekly
// limits ("resets Nov 3 at 8pm (America/Los_Angeles)").
func TestParseResetsAt_WithDate(t *testing.T) {
	loc, _ := time.LoadLocation("America/Los_Angeles")

	tests := []struct {
		name    string
		message string
		ref     time.Time
		want    time.Time
	}{
		{
			name:    "later this year",
			message: "You've hit your weekly limit · resets Nov 3 at 8pm (America/Los_Angeles)",
			ref:     time.Date(2026, 10, 30, 10, 25, 0, 0, loc),
			want:    time.Date(2026, 11, 3, 20, 0, 0, 0, loc),
		},
		{
			name:    "with minutes",
			message: "resets Nov 3 at 8:30am (America/Los_Angeles)",
			ref:     time.Date(2026, 10, 30, 10, 25, 0, 0, loc),
			want:    time.Date(2026, 11, 3, 8, 30, 0, 0, loc),
		},
		{
			name:    "wraps into next year",
			message: "resets Jan 2 at 9am (America/Los_Angeles)",
			ref:     time.Date(2026, 12, 30, 10, 0, 0, 0, loc),
			want:    time.Date(2027, 1, 2, 9, 0, 0, 0, loc),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseResetsAt(tt.message, tt.ref)
			if !ok {
				t.Fatalf("parseResetsAt(%q) ok = false, want true", tt.message)
			}
			if !got.Equal(tt.want) {
				t.Fatalf("parseResetsAt(%q) = %v, want %v", tt.message, got, tt.want)
			}
		})
	}
}

func TestEventHandler_OnSessionLogLine_ContentArrayFormat(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	// Content as array of blocks (real Claude format).
	line, _ := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":    "assistant",
			"content": []map[string]string{{"type": "text", "text": "Hello from array format!"}},
		},
	})
	h.OnSessionLogLine(line)

	select {
	case ev := <-events:
		if ev.Type != monitor.EventAgentMessage {
			t.Fatalf("Type = %v, want EventAgentMessage", ev.Type)
		}
		if ev.Data.(monitor.AgentMessageData).Content != "Hello from array format!" {
			t.Fatalf("unexpected content: %+v", ev.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestEventHandler_APIError_401_AuthError(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	payload := otelLogsPayload{
		ResourceLogs: []otelResourceLogs{{
			ScopeLogs: []otelScopeLogs{{
				LogRecords: []otelLogRecord{{
					Attributes: []otelAttribute{
						{Key: "event.name", Value: otelAttrValue{StringValue: "api_error"}},
						{Key: "status_code", Value: otelAttrValue{StringValue: "401"}},
						{Key: "error", Value: otelAttrValue{StringValue: "authentication_error"}},
					},
				}},
			}},
		}},
	}
	body, _ := json.Marshal(payload)
	h.OnLogs(body)

	got := drainEvents(events, 1)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Type != monitor.EventStateChange {
		t.Fatalf("Type = %v, want EventStateChange", got[0].Type)
	}
	state := got[0].Data.(monitor.StateChangeData)
	if state.State != monitor.StateIdle || state.SubState != monitor.SubStateAuthError {
		t.Errorf("state = (%v,%v), want (Idle,AuthError)", state.State, state.SubState)
	}
}

func TestEventHandler_OnSessionLogLine_AuthError(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	line, _ := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":    "assistant",
			"content": "Please run /login · API Error: 401 {\"type\":\"error\",\"error\":{\"type\":\"authentication_error\",\"message\":\"OAuth token has expired.\"}}",
		},
		"isApiErrorMessage": true,
	})
	h.OnSessionLogLine(line)

	got := drainEvents(events, 2)
	if len(got) < 2 {
		t.Fatalf("expected 2 events (auth_error_info + agent_message), got %d", len(got))
	}

	if got[0].Type != monitor.EventAuthErrorInfo {
		t.Fatalf("event[0].Type = %v, want EventAuthErrorInfo", got[0].Type)
	}
	data := got[0].Data.(monitor.AuthErrorData)
	if !strings.Contains(data.Message, "authentication_error") {
		t.Errorf("expected authentication_error in message, got %q", data.Message)
	}

	if got[1].Type != monitor.EventAgentMessage {
		t.Fatalf("event[1].Type = %v, want EventAgentMessage", got[1].Type)
	}
}

func TestEventHandler_OnSessionLogLine_ServerError(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	line, _ := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":    "assistant",
			"content": "API Error: 500 {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"Internal server error\"},\"request_id\":\"req_abc123\"}",
		},
		"isApiErrorMessage": true,
	})
	h.OnSessionLogLine(line)

	got := drainEvents(events, 3)
	if len(got) < 3 {
		t.Fatalf("expected 3 events (state_change + server_error_info + agent_message), got %d", len(got))
	}

	if got[0].Type != monitor.EventStateChange {
		t.Fatalf("event[0].Type = %v, want EventStateChange", got[0].Type)
	}
	state := got[0].Data.(monitor.StateChangeData)
	if state.State != monitor.StateIdle || state.SubState != monitor.SubStateServerError {
		t.Errorf("state = (%v,%v), want (Idle,ServerError)", state.State, state.SubState)
	}

	if got[1].Type != monitor.EventServerErrorInfo {
		t.Fatalf("event[1].Type = %v, want EventServerErrorInfo", got[1].Type)
	}
	data := got[1].Data.(monitor.ServerErrorData)
	if !strings.Contains(data.Message, "Internal server error") {
		t.Errorf("expected 'Internal server error' in message, got %q", data.Message)
	}

	if got[2].Type != monitor.EventAgentMessage {
		t.Fatalf("event[2].Type = %v, want EventAgentMessage", got[2].Type)
	}
}

// TestEventHandler_OnSessionLogLine_Overloaded529 covers the concierge-fog
// failure mode: a synthetic "529 Overloaded" give-up message (isApiErrorMessage
// with apiErrorStatus 529) and no following Stop hook. The agent must be driven
// to idle (server_error) rather than left frozen at "Active (thinking)".
func TestEventHandler_OnSessionLogLine_Overloaded529(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	line, _ := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":    "assistant",
			"model":   "<synthetic>",
			"content": []map[string]any{{"type": "text", "text": "API Error: 529 Overloaded. This is a server-side issue, usually temporary — try again in a moment. If it persists, check https://status.claude.com."}},
		},
		"isApiErrorMessage": true,
		"apiErrorStatus":    529,
		"error":             "server_error",
	})
	h.OnSessionLogLine(line)

	got := drainEvents(events, 3)
	if len(got) < 3 {
		t.Fatalf("expected 3 events (state_change + server_error_info + agent_message), got %d", len(got))
	}
	if got[0].Type != monitor.EventStateChange {
		t.Fatalf("event[0].Type = %v, want EventStateChange", got[0].Type)
	}
	state := got[0].Data.(monitor.StateChangeData)
	if state.State != monitor.StateIdle || state.SubState != monitor.SubStateServerError {
		t.Errorf("state = (%v,%v), want (Idle,ServerError)", state.State, state.SubState)
	}
	if got[1].Type != monitor.EventServerErrorInfo {
		t.Fatalf("event[1].Type = %v, want EventServerErrorInfo", got[1].Type)
	}
	data := got[1].Data.(monitor.ServerErrorData)
	if data.StatusCode != "529" {
		t.Errorf("StatusCode = %q, want \"529\"", data.StatusCode)
	}
	if !strings.Contains(data.Message, "529 Overloaded") {
		t.Errorf("expected '529 Overloaded' in message, got %q", data.Message)
	}
	if got[2].Type != monitor.EventAgentMessage {
		t.Fatalf("event[2].Type = %v, want EventAgentMessage", got[2].Type)
	}
}

func TestIsNetworkErrorMessage(t *testing.T) {
	tests := []struct {
		content string
		want    bool
	}{
		{"API Error: Unable to connect to API (ConnectionRefused)", true},
		{"API Error: Unable to connect to API (FailedToOpenSocket)", true},
		{"Connection error.", true},
		{"connection refused", true},
		{`API Error: 500 {"type":"error","error":{"type":"api_error","message":"Internal server error"}}`, false},
		{"OAuth token has expired. Please obtain a new token.", false},
		{"normal assistant message", false},
	}
	for _, tt := range tests {
		if got := isNetworkErrorMessage(tt.content); got != tt.want {
			t.Errorf("isNetworkErrorMessage(%q) = %v, want %v", tt.content, got, tt.want)
		}
	}
}

// TestEventHandler_OnSessionLogLine_NetworkError covers the concierge-leaf
// failure mode: a synthetic connection-error message with no HTTP status code
// and no following Stop hook. The agent must be driven to idle (server_error)
// rather than left frozen in its prior Active sub-state.
func TestEventHandler_OnSessionLogLine_NetworkError(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	line, _ := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":    "assistant",
			"model":   "<synthetic>",
			"content": []map[string]any{{"type": "text", "text": "API Error: Unable to connect to API (ConnectionRefused)"}},
		},
		"isApiErrorMessage": true,
		"error":             "unknown",
	})
	h.OnSessionLogLine(line)

	got := drainEvents(events, 3)
	if len(got) < 3 {
		t.Fatalf("expected 3 events (state_change + server_error_info + agent_message), got %d", len(got))
	}
	if got[0].Type != monitor.EventStateChange {
		t.Fatalf("event[0].Type = %v, want EventStateChange", got[0].Type)
	}
	state := got[0].Data.(monitor.StateChangeData)
	if state.State != monitor.StateIdle || state.SubState != monitor.SubStateServerError {
		t.Errorf("state = (%v,%v), want (Idle,ServerError)", state.State, state.SubState)
	}
	if got[1].Type != monitor.EventServerErrorInfo {
		t.Fatalf("event[1].Type = %v, want EventServerErrorInfo", got[1].Type)
	}
	data := got[1].Data.(monitor.ServerErrorData)
	if !strings.Contains(data.Message, "ConnectionRefused") {
		t.Errorf("expected 'ConnectionRefused' in message, got %q", data.Message)
	}
	if got[2].Type != monitor.EventAgentMessage {
		t.Fatalf("event[2].Type = %v, want EventAgentMessage", got[2].Type)
	}
}

// TestEventHandler_APIError_ConnectionRefused covers the OTEL api_error path
// for a connection-level failure: no status_code, network error message.
func TestEventHandler_APIError_ConnectionRefused(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	payload := otelLogsPayload{
		ResourceLogs: []otelResourceLogs{{
			ScopeLogs: []otelScopeLogs{{
				LogRecords: []otelLogRecord{{
					Attributes: []otelAttribute{
						{Key: "event.name", Value: otelAttrValue{StringValue: "api_error"}},
						{Key: "error", Value: otelAttrValue{StringValue: "Connection error."}},
					},
				}},
			}},
		}},
	}
	body, _ := json.Marshal(payload)
	h.OnLogs(body)

	got := drainEvents(events, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 events (state_change + server_error_info), got %d", len(got))
	}
	state := got[0].Data.(monitor.StateChangeData)
	if state.State != monitor.StateIdle || state.SubState != monitor.SubStateServerError {
		t.Errorf("state = (%v,%v), want (Idle,ServerError)", state.State, state.SubState)
	}
	if got[1].Type != monitor.EventServerErrorInfo {
		t.Fatalf("event[1].Type = %v, want EventServerErrorInfo", got[1].Type)
	}
}

func TestIsAuthErrorMessage(t *testing.T) {
	tests := []struct {
		content string
		want    bool
	}{
		{"OAuth token has expired. Please obtain a new token.", true},
		{"authentication_error: invalid credentials", true},
		{"You've hit your limit · resets 12pm (America/Los_Angeles)", false},
		{"normal assistant message", false},
	}
	for _, tt := range tests {
		if got := isAuthErrorMessage(tt.content); got != tt.want {
			t.Errorf("isAuthErrorMessage(%q) = %v, want %v", tt.content, got, tt.want)
		}
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
