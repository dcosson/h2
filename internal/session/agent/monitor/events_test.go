package monitor

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAgentEventType_Values(t *testing.T) {
	// Verify the iota ordering matches the design doc.
	if EventSessionStarted != 0 {
		t.Errorf("EventSessionStarted = %d, want 0", EventSessionStarted)
	}
	if EventUserPrompt != 1 {
		t.Errorf("EventUserPrompt = %d, want 1", EventUserPrompt)
	}
	if EventTurnCompleted != 2 {
		t.Errorf("EventTurnCompleted = %d, want 2", EventTurnCompleted)
	}
	if EventToolStarted != 3 {
		t.Errorf("EventToolStarted = %d, want 3", EventToolStarted)
	}
	if EventToolCompleted != 4 {
		t.Errorf("EventToolCompleted = %d, want 4", EventToolCompleted)
	}
	if EventApprovalRequested != 5 {
		t.Errorf("EventApprovalRequested = %d, want 5", EventApprovalRequested)
	}
	if EventAgentMessage != 6 {
		t.Errorf("EventAgentMessage = %d, want 6", EventAgentMessage)
	}
	if EventStateChange != 7 {
		t.Errorf("EventStateChange = %d, want 7", EventStateChange)
	}
	if EventSessionEnded != 8 {
		t.Errorf("EventSessionEnded = %d, want 8", EventSessionEnded)
	}
	if EventUsageLimitInfo != 9 {
		t.Errorf("EventUsageLimitInfo = %d, want 9", EventUsageLimitInfo)
	}
}

func TestAgentEvent_PayloadTypes(t *testing.T) {
	now := time.Now()

	// SessionStartedData
	ev := AgentEvent{
		Type:      EventSessionStarted,
		Timestamp: now,
		Data:      SessionStartedData{SessionID: "t1", Model: "claude-opus-4-6"},
	}
	data, ok := ev.Data.(SessionStartedData)
	if !ok {
		t.Fatal("expected SessionStartedData")
	}
	if data.SessionID != "t1" || data.Model != "claude-opus-4-6" {
		t.Errorf("unexpected data: %+v", data)
	}

	// TurnCompletedData
	ev = AgentEvent{
		Type:      EventTurnCompleted,
		Timestamp: now,
		Data: TurnCompletedData{
			TurnID:       "turn-1",
			InputTokens:  1000,
			OutputTokens: 500,
			CachedTokens: 200,
			CostUSD:      0.05,
		},
	}
	tc, ok := ev.Data.(TurnCompletedData)
	if !ok {
		t.Fatal("expected TurnCompletedData")
	}
	if tc.InputTokens != 1000 || tc.OutputTokens != 500 || tc.CachedTokens != 200 {
		t.Errorf("unexpected token counts: %+v", tc)
	}

	// ToolCompletedData
	ev = AgentEvent{
		Type:      EventToolCompleted,
		Timestamp: now,
		Data: ToolCompletedData{
			ToolName:   "Bash",
			CallID:     "call-1",
			DurationMs: 150,
			Success:    true,
		},
	}
	td, ok := ev.Data.(ToolCompletedData)
	if !ok {
		t.Fatal("expected ToolCompletedData")
	}
	if td.ToolName != "Bash" || !td.Success {
		t.Errorf("unexpected tool data: %+v", td)
	}

	// StateChangeData
	ev = AgentEvent{
		Type:      EventStateChange,
		Timestamp: now,
		Data: StateChangeData{
			State:    StateActive,
			SubState: SubStateThinking,
		},
	}
	sc, ok := ev.Data.(StateChangeData)
	if !ok {
		t.Fatal("expected StateChangeData")
	}
	if sc.State != StateActive || sc.SubState != SubStateThinking {
		t.Errorf("unexpected state change: %+v", sc)
	}
}

func TestSessionStartedData_UnmarshalJSON_LegacyThreadID(t *testing.T) {
	// Old eventstore JSONL has "ThreadID" instead of "SessionID".
	raw := `{"ThreadID":"old-session-123","Model":"claude-4"}`
	var d SessionStartedData
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.SessionID != "old-session-123" {
		t.Errorf("SessionID = %q, want %q", d.SessionID, "old-session-123")
	}
	if d.Model != "claude-4" {
		t.Errorf("Model = %q, want %q", d.Model, "claude-4")
	}
}

func TestSessionStartedData_UnmarshalJSON_NewSessionID(t *testing.T) {
	raw := `{"SessionID":"new-session-456","Model":"claude-4"}`
	var d SessionStartedData
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.SessionID != "new-session-456" {
		t.Errorf("SessionID = %q, want %q", d.SessionID, "new-session-456")
	}
}

func TestSessionStartedData_UnmarshalJSON_SessionIDPrecedence(t *testing.T) {
	// If both are present (shouldn't happen but be safe), SessionID wins.
	raw := `{"SessionID":"new","ThreadID":"old","Model":"m"}`
	var d SessionStartedData
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.SessionID != "new" {
		t.Errorf("SessionID = %q, want %q (SessionID should take precedence)", d.SessionID, "new")
	}
}

func TestAgentEventType_String(t *testing.T) {
	tests := []struct {
		typ  AgentEventType
		want string
	}{
		{EventSessionStarted, "session_started"},
		{EventUserPrompt, "user_prompt"},
		{EventTurnCompleted, "turn_completed"},
		{EventToolStarted, "tool_started"},
		{EventToolCompleted, "tool_completed"},
		{EventApprovalRequested, "approval_requested"},
		{EventAgentMessage, "agent_message"},
		{EventStateChange, "state_change"},
		{EventSessionEnded, "session_ended"},
		{EventUsageLimitInfo, "usage_limit_info"},
		{EventAuthErrorInfo, "auth_error_info"},
		{AgentEventType(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.typ.String(); got != tt.want {
			t.Errorf("AgentEventType(%d).String() = %q, want %q", tt.typ, got, tt.want)
		}
	}
}
