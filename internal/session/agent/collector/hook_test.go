package collector

import (
	"encoding/json"
	"testing"
	"time"
)

func TestHookCollector_ProcessEvent_Basic(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	hc.ProcessEvent("UserPromptSubmit", nil)

	state := hc.Snapshot()
	if state.LastEvent != "UserPromptSubmit" {
		t.Fatalf("expected LastEvent 'UserPromptSubmit', got %q", state.LastEvent)
	}
	if state.LastEventTime.IsZero() {
		t.Fatal("expected non-zero LastEventTime")
	}
	if state.ToolUseCount != 0 {
		t.Fatalf("expected ToolUseCount 0, got %d", state.ToolUseCount)
	}
}

func TestHookCollector_ProcessEvent_PreToolUse(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	payload := json.RawMessage(`{"tool_name": "Bash", "tool_input": {"command": "ls"}}`)
	hc.ProcessEvent("PreToolUse", payload)

	state := hc.Snapshot()
	if state.LastEvent != "PreToolUse" {
		t.Fatalf("expected LastEvent 'PreToolUse', got %q", state.LastEvent)
	}
	if state.LastToolName != "Bash" {
		t.Fatalf("expected LastToolName 'Bash', got %q", state.LastToolName)
	}
	if state.ToolUseCount != 1 {
		t.Fatalf("expected ToolUseCount 1, got %d", state.ToolUseCount)
	}
}

func TestHookCollector_ProcessEvent_PostToolUse(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	payload := json.RawMessage(`{"tool_name": "Read"}`)
	hc.ProcessEvent("PostToolUse", payload)

	state := hc.Snapshot()
	if state.LastToolName != "Read" {
		t.Fatalf("expected LastToolName 'Read', got %q", state.LastToolName)
	}
	// PostToolUse does not increment tool count (only PreToolUse does).
	if state.ToolUseCount != 0 {
		t.Fatalf("expected ToolUseCount 0, got %d", state.ToolUseCount)
	}
}

func TestHookCollector_ProcessEvent_ToolCount(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	hc.ProcessEvent("PreToolUse", json.RawMessage(`{"tool_name": "Bash"}`))
	hc.ProcessEvent("PostToolUse", json.RawMessage(`{"tool_name": "Bash"}`))
	hc.ProcessEvent("PreToolUse", json.RawMessage(`{"tool_name": "Read"}`))
	hc.ProcessEvent("PostToolUse", json.RawMessage(`{"tool_name": "Read"}`))
	hc.ProcessEvent("PreToolUse", json.RawMessage(`{"tool_name": "Edit"}`))

	state := hc.Snapshot()
	if state.ToolUseCount != 3 {
		t.Fatalf("expected ToolUseCount 3, got %d", state.ToolUseCount)
	}
	if state.LastToolName != "Edit" {
		t.Fatalf("expected LastToolName 'Edit', got %q", state.LastToolName)
	}
}

func TestHookCollector_StateCh_ActiveOnToolUse(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	hc.ProcessEvent("PreToolUse", json.RawMessage(`{"tool_name": "Bash"}`))

	select {
	case s := <-hc.StateCh():
		if s != StateActive {
			t.Fatalf("expected StateActive, got %v", s)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for StateActive")
	}
}

func TestHookCollector_StateCh_IdleOnStop(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	hc.ProcessEvent("Stop", nil)

	select {
	case s := <-hc.StateCh():
		if s != StateIdle {
			t.Fatalf("expected StateIdle, got %v", s)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for StateIdle")
	}
}

func TestHookCollector_StateCh_ExitedOnSessionEnd(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	hc.ProcessEvent("SessionEnd", nil)

	select {
	case s := <-hc.StateCh():
		if s != StateExited {
			t.Fatalf("expected StateExited, got %v", s)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for StateExited")
	}
}

func TestHookCollector_StateCh_IdleOnSessionStart(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	hc.ProcessEvent("SessionStart", nil)

	select {
	case s := <-hc.StateCh():
		if s != StateIdle {
			t.Fatalf("expected StateIdle, got %v", s)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for StateIdle")
	}
}

func TestHookCollector_ProcessEvent_NonBlocking(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	// Fill the activity channel and state channel.
	for i := 0; i < 20; i++ {
		done := make(chan struct{})
		go func() {
			hc.ProcessEvent("PreToolUse", json.RawMessage(`{"tool_name": "Bash"}`))
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("ProcessEvent blocked")
		}
	}
}

func TestHookCollector_ExtractToolName_InvalidJSON(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	hc.ProcessEvent("PreToolUse", json.RawMessage(`not json`))

	state := hc.Snapshot()
	if state.LastToolName != "" {
		t.Fatalf("expected empty LastToolName for invalid JSON, got %q", state.LastToolName)
	}
}

func TestHookCollector_ExtractToolName_NilPayload(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	hc.ProcessEvent("PreToolUse", nil)

	state := hc.Snapshot()
	if state.LastToolName != "" {
		t.Fatalf("expected empty LastToolName for nil payload, got %q", state.LastToolName)
	}
}

func TestHookCollector_BlockedPermission(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	// Not blocked initially.
	state := hc.Snapshot()
	if state.BlockedOnPermission {
		t.Fatal("should not be blocked initially")
	}

	// Send blocked_permission event.
	payload := json.RawMessage(`{"tool_name": "Bash", "hook_event_name": "blocked_permission"}`)
	hc.ProcessEvent("blocked_permission", payload)

	state = hc.Snapshot()
	if !state.BlockedOnPermission {
		t.Fatal("should be blocked after blocked_permission event")
	}
	if state.BlockedToolName != "Bash" {
		t.Errorf("BlockedToolName = %q, want %q", state.BlockedToolName, "Bash")
	}
}

func TestHookCollector_BlockedPermission_ClearedByPreToolUse(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	// Set blocked.
	hc.ProcessEvent("blocked_permission", json.RawMessage(`{"tool_name": "Bash"}`))
	if !hc.Snapshot().BlockedOnPermission {
		t.Fatal("should be blocked")
	}

	// PreToolUse clears it (tool was approved).
	hc.ProcessEvent("PreToolUse", json.RawMessage(`{"tool_name": "Bash"}`))
	state := hc.Snapshot()
	if state.BlockedOnPermission {
		t.Fatal("should be unblocked after PreToolUse")
	}
	if state.BlockedToolName != "" {
		t.Errorf("BlockedToolName = %q, want empty", state.BlockedToolName)
	}
}

func TestHookCollector_BlockedPermission_ClearedByUserPromptSubmit(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	hc.ProcessEvent("blocked_permission", json.RawMessage(`{"tool_name": "Bash"}`))
	hc.ProcessEvent("UserPromptSubmit", nil)

	if hc.Snapshot().BlockedOnPermission {
		t.Fatal("should be unblocked after UserPromptSubmit")
	}
}

func TestHookCollector_BlockedPermission_ClearedByStop(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	hc.ProcessEvent("blocked_permission", json.RawMessage(`{"tool_name": "Bash"}`))
	hc.ProcessEvent("Stop", nil)

	if hc.Snapshot().BlockedOnPermission {
		t.Fatal("should be unblocked after Stop")
	}
}

func TestHookCollector_BlockedPermission_NotClearedByPermissionRequest(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	hc.ProcessEvent("blocked_permission", json.RawMessage(`{"tool_name": "Bash"}`))
	// PermissionRequest should NOT clear the blocked state — it's the event
	// that triggered the block in the first place.
	hc.ProcessEvent("PermissionRequest", json.RawMessage(`{"tool_name": "Bash"}`))

	if !hc.Snapshot().BlockedOnPermission {
		t.Fatal("should still be blocked after PermissionRequest")
	}
}
