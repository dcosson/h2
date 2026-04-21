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

	got := drainEvents(events, 1)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Type != monitor.EventStateChange {
		t.Fatalf("Type = %v, want EventStateChange", got[0].Type)
	}
	state := got[0].Data.(monitor.StateChangeData)
	if state.State != monitor.StateIdle || state.SubState != monitor.SubStateUsageLimit {
		t.Errorf("state = (%v,%v), want (Idle,UsageLimit)", state.State, state.SubState)
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

	got := drainEvents(events, 2)
	if len(got) < 2 {
		t.Fatalf("expected 2 events (usage_limit_info + agent_message), got %d", len(got))
	}

	// First event should be usage limit info.
	if got[0].Type != monitor.EventUsageLimitInfo {
		t.Fatalf("event[0].Type = %v, want EventUsageLimitInfo", got[0].Type)
	}
	data := got[0].Data.(monitor.UsageLimitData)
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

	// Second event should be the agent message.
	if got[1].Type != monitor.EventAgentMessage {
		t.Fatalf("event[1].Type = %v, want EventAgentMessage", got[1].Type)
	}
}

func TestEventHandler_OnSessionLogLine_RateLimitNoResetTime(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewEventHandler(events, nil)

	// Rate limit message without the "resets Xpm (TZ)" pattern.
	line, _ := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":    "assistant",
			"content": []map[string]string{{"type": "text", "text": "You've hit your limit"}},
		},
		"error":             "rate_limit",
		"isApiErrorMessage": true,
	})
	h.OnSessionLogLine(line)

	// Should only get the agent message, no usage limit info.
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

	got := drainEvents(events, 2)
	if len(got) < 2 {
		t.Fatalf("expected 2 events (server_error_info + agent_message), got %d", len(got))
	}

	if got[0].Type != monitor.EventServerErrorInfo {
		t.Fatalf("event[0].Type = %v, want EventServerErrorInfo", got[0].Type)
	}
	data := got[0].Data.(monitor.ServerErrorData)
	if !strings.Contains(data.Message, "Internal server error") {
		t.Errorf("expected 'Internal server error' in message, got %q", data.Message)
	}

	if got[1].Type != monitor.EventAgentMessage {
		t.Fatalf("event[1].Type = %v, want EventAgentMessage", got[1].Type)
	}
}

func TestIsServerErrorMessage(t *testing.T) {
	tests := []struct {
		content string
		want    bool
	}{
		{`API Error: 500 {"type":"error","error":{"type":"api_error","message":"Internal server error"}}`, true},
		{`API Error: {"type":"error","error":{"type":"api_error","message":"Internal server error"}}`, true},
		{"API Error: 529 overloaded", true},
		{"OAuth token has expired. Please obtain a new token.", false},
		{"authentication_error: invalid credentials", false},
		{"normal assistant message", false},
	}
	for _, tt := range tests {
		if got := isServerErrorMessage(tt.content); got != tt.want {
			t.Errorf("isServerErrorMessage(%q) = %v, want %v", tt.content, got, tt.want)
		}
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
