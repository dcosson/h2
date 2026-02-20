package claude

import (
	"encoding/json"
	"testing"
	"time"

	"h2/internal/session/agent/monitor"
)

func TestHookHandler_PreToolUse(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewHookHandler(events, nil)

	payload, _ := json.Marshal(map[string]string{
		"tool_name":  "Bash",
		"session_id": "s1",
	})
	h.ProcessEvent("PreToolUse", payload)

	got := drainEvents(events, 2)
	if len(got) < 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}

	if got[0].Type != monitor.EventToolStarted {
		t.Errorf("event[0].Type = %v, want EventToolStarted", got[0].Type)
	}
	td := got[0].Data.(monitor.ToolStartedData)
	if td.ToolName != "Bash" {
		t.Errorf("ToolName = %q, want Bash", td.ToolName)
	}

	if got[1].Type != monitor.EventStateChange {
		t.Errorf("event[1].Type = %v, want EventStateChange", got[1].Type)
	}
	sc := got[1].Data.(monitor.StateChangeData)
	if sc.SubState != monitor.SubStateToolUse {
		t.Errorf("SubState = %v, want ToolUse", sc.SubState)
	}
}

func TestHookHandler_PostToolUse(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewHookHandler(events, nil)

	payload, _ := json.Marshal(map[string]string{"tool_name": "Read"})
	h.ProcessEvent("PostToolUse", payload)

	got := drainEvents(events, 2)
	if got[0].Type != monitor.EventToolCompleted {
		t.Errorf("event[0].Type = %v, want EventToolCompleted", got[0].Type)
	}
	if got[1].Type != monitor.EventStateChange {
		t.Errorf("event[1].Type = %v, want EventStateChange", got[1].Type)
	}
	sc := got[1].Data.(monitor.StateChangeData)
	if sc.SubState != monitor.SubStateThinking {
		t.Errorf("SubState = %v, want Thinking", sc.SubState)
	}
}

func TestHookHandler_PermissionRequest(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewHookHandler(events, nil)

	h.ProcessEvent("PermissionRequest", nil)

	got := drainEvents(events, 2)
	if got[0].Type != monitor.EventApprovalRequested {
		t.Errorf("event[0].Type = %v, want EventApprovalRequested", got[0].Type)
	}
	sc := got[1].Data.(monitor.StateChangeData)
	if sc.SubState != monitor.SubStateWaitingForPermission {
		t.Errorf("SubState = %v, want WaitingForPermission", sc.SubState)
	}
}

func TestHookHandler_PermissionDecision_AskUser(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewHookHandler(events, nil)

	payload, _ := json.Marshal(map[string]string{
		"decision": "ask_user",
		"reason":   "no matching rule",
	})
	h.ProcessEvent("permission_decision", payload)

	got := drainEvents(events, 1)
	sc := got[0].Data.(monitor.StateChangeData)
	if sc.SubState != monitor.SubStateWaitingForPermission {
		t.Errorf("SubState = %v, want WaitingForPermission", sc.SubState)
	}
}

func TestHookHandler_PermissionDecision_Allow(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewHookHandler(events, nil)

	payload, _ := json.Marshal(map[string]string{
		"decision": "allow",
	})
	h.ProcessEvent("permission_decision", payload)

	got := drainEvents(events, 1)
	sc := got[0].Data.(monitor.StateChangeData)
	if sc.SubState != monitor.SubStateToolUse {
		t.Errorf("SubState = %v, want ToolUse", sc.SubState)
	}
}

func TestHookHandler_SessionStart_Idle(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewHookHandler(events, nil)

	h.ProcessEvent("SessionStart", nil)

	got := drainEvents(events, 1)
	sc := got[0].Data.(monitor.StateChangeData)
	if sc.State != monitor.StateIdle {
		t.Errorf("State = %v, want Idle", sc.State)
	}
}

func TestHookHandler_PreCompact(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewHookHandler(events, nil)

	h.ProcessEvent("PreCompact", nil)

	got := drainEvents(events, 1)
	sc := got[0].Data.(monitor.StateChangeData)
	if sc.SubState != monitor.SubStateCompacting {
		t.Errorf("SubState = %v, want Compacting", sc.SubState)
	}
}

func TestHookHandler_NoteInterrupt(t *testing.T) {
	events := make(chan monitor.AgentEvent, 64)
	h := NewHookHandler(events, nil)

	h.NoteInterrupt()

	got := drainEvents(events, 1)
	sc := got[0].Data.(monitor.StateChangeData)
	if sc.State != monitor.StateIdle {
		t.Errorf("State = %v, want Idle", sc.State)
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
