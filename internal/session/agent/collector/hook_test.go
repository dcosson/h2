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
	case su := <-hc.StateCh():
		if su.State != StateActive {
			t.Fatalf("expected StateActive, got %v", su.State)
		}
		if su.SubState != SubStateToolUse {
			t.Fatalf("expected SubStateToolUse, got %v", su.SubState)
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
	case su := <-hc.StateCh():
		if su.State != StateIdle {
			t.Fatalf("expected StateIdle, got %v", su.State)
		}
		if su.SubState != SubStateNone {
			t.Fatalf("expected SubStateNone, got %v", su.SubState)
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
	case su := <-hc.StateCh():
		if su.State != StateExited {
			t.Fatalf("expected StateExited, got %v", su.State)
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
	case su := <-hc.StateCh():
		if su.State != StateIdle {
			t.Fatalf("expected StateIdle, got %v", su.State)
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

// --- HookState.SubState() unit tests ---

func TestHookState_SubState_Thinking(t *testing.T) {
	for _, event := range []string{"UserPromptSubmit", "PostToolUse"} {
		hs := HookState{LastEvent: event}
		if got := hs.SubState(); got != SubStateThinking {
			t.Errorf("SubState() for %q = %v, want SubStateThinking", event, got)
		}
	}
}

func TestHookState_SubState_ToolUse(t *testing.T) {
	hs := HookState{LastEvent: "PreToolUse"}
	if got := hs.SubState(); got != SubStateToolUse {
		t.Fatalf("SubState() for PreToolUse = %v, want SubStateToolUse", got)
	}
}

func TestHookState_SubState_WaitingForPermission_FromEvent(t *testing.T) {
	hs := HookState{LastEvent: "PermissionRequest"}
	if got := hs.SubState(); got != SubStateWaitingForPermission {
		t.Fatalf("SubState() for PermissionRequest = %v, want SubStateWaitingForPermission", got)
	}
}

func TestHookState_SubState_WaitingForPermission_BlockedFlag(t *testing.T) {
	// BlockedOnPermission takes priority over lastEvent-based classification.
	hs := HookState{LastEvent: "PreToolUse", BlockedOnPermission: true}
	if got := hs.SubState(); got != SubStateWaitingForPermission {
		t.Fatalf("SubState() with BlockedOnPermission = %v, want SubStateWaitingForPermission", got)
	}
}

func TestHookState_SubState_None_UnknownEvent(t *testing.T) {
	hs := HookState{LastEvent: "SessionStart"}
	if got := hs.SubState(); got != SubStateNone {
		t.Fatalf("SubState() for SessionStart = %v, want SubStateNone", got)
	}
}

func TestSubState_String(t *testing.T) {
	tests := []struct {
		ss   SubState
		want string
	}{
		{SubStateNone, ""},
		{SubStateThinking, "thinking"},
		{SubStateToolUse, "tool_use"},
		{SubStateWaitingForPermission, "waiting_for_permission"},
		{SubStateCompacting, "compacting"},
		{SubState(99), ""},
	}
	for _, tt := range tests {
		if got := tt.ss.String(); got != tt.want {
			t.Errorf("SubState(%d).String() = %q, want %q", tt.ss, got, tt.want)
		}
	}
}

func TestHookCollector_StateCh_PermissionDecisionEmitsSubState(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	// permission_decision with ask_user should emit WaitingForPermission sub-state.
	payload := json.RawMessage(`{"tool_name": "Bash", "decision": "ask_user"}`)
	hc.ProcessEvent("permission_decision", payload)

	select {
	case su := <-hc.StateCh():
		// State stays Initialized (permission_decision doesn't change State),
		// but SubState should be WaitingForPermission.
		if su.SubState != SubStateWaitingForPermission {
			t.Fatalf("expected SubStateWaitingForPermission, got %v", su.SubState)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for StateUpdate from permission_decision")
	}
}

func TestHookCollector_PermissionAllow_TransitionsToActive(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	// Reproduce the exact bug scenario:
	// 1. PreToolUse → Active
	// 2. PermissionRequest → Active
	// 3. SessionStart (reviewer) → Idle
	// 4. Stop (reviewer ends) → Idle
	// 5. permission_decision (allow) → should be Active (was staying Idle!)

	hc.ProcessEvent("PreToolUse", json.RawMessage(`{"tool_name": "Bash"}`))
	<-hc.StateCh() // drain: Active

	hc.ProcessEvent("PermissionRequest", json.RawMessage(`{"tool_name": "Bash"}`))
	<-hc.StateCh() // drain: Active

	hc.ProcessEvent("SessionStart", nil)
	su := <-hc.StateCh()
	if su.State != StateIdle {
		t.Fatalf("after SessionStart: expected StateIdle, got %v", su.State)
	}

	hc.ProcessEvent("Stop", nil)
	su = <-hc.StateCh()
	if su.State != StateIdle {
		t.Fatalf("after Stop: expected StateIdle, got %v", su.State)
	}

	// permission_decision with allow should transition back to Active.
	hc.ProcessEvent("permission_decision", json.RawMessage(`{"tool_name": "Bash", "decision": "allow"}`))
	select {
	case su = <-hc.StateCh():
		if su.State != StateActive {
			t.Fatalf("after permission_decision allow: expected StateActive, got %v", su.State)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for StateActive after permission_decision allow")
	}
}

func TestHookCollector_PermissionAskUser_StaysIdle(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	// Same sequence but with ask_user — should stay idle.
	hc.ProcessEvent("PreToolUse", json.RawMessage(`{"tool_name": "Bash"}`))
	<-hc.StateCh()

	hc.ProcessEvent("SessionStart", nil)
	<-hc.StateCh()

	hc.ProcessEvent("Stop", nil)
	<-hc.StateCh()

	hc.ProcessEvent("permission_decision", json.RawMessage(`{"tool_name": "Bash", "decision": "ask_user"}`))
	select {
	case su := <-hc.StateCh():
		// ask_user: state should NOT transition to Active.
		if su.State != StateIdle {
			t.Fatalf("after permission_decision ask_user: expected StateIdle, got %v", su.State)
		}
		if su.SubState != SubStateWaitingForPermission {
			t.Fatalf("expected SubStateWaitingForPermission, got %v", su.SubState)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for StateUpdate")
	}
}

func TestHookCollector_PreCompact_SetsCompactingSubState(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	// Simulate: agent is active, then PreCompact fires.
	hc.ProcessEvent("PostToolUse", json.RawMessage(`{"tool_name": "Bash"}`))
	<-hc.StateCh() // drain: Active/Thinking

	hc.ProcessEvent("PreCompact", nil)
	select {
	case su := <-hc.StateCh():
		if su.State != StateActive {
			t.Fatalf("expected StateActive during compaction, got %v", su.State)
		}
		if su.SubState != SubStateCompacting {
			t.Fatalf("expected SubStateCompacting, got %v", su.SubState)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for compacting state")
	}

	// Verify snapshot also shows compacting.
	snap := hc.Snapshot()
	if !snap.Compacting {
		t.Fatal("Snapshot.Compacting should be true after PreCompact")
	}
}

func TestHookCollector_CompactionCleared_BySessionStart(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	// Start compaction.
	hc.ProcessEvent("PreCompact", nil)
	<-hc.StateCh() // drain

	// SessionStart after compaction — should clear compacting and go idle.
	hc.ProcessEvent("SessionStart", nil)
	select {
	case su := <-hc.StateCh():
		if su.State != StateIdle {
			t.Fatalf("expected StateIdle after compaction SessionStart, got %v", su.State)
		}
		if su.SubState != SubStateNone {
			t.Fatalf("expected SubStateNone after compaction, got %v", su.SubState)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}

	if hc.Snapshot().Compacting {
		t.Fatal("Compacting should be false after SessionStart")
	}
}

func TestHookCollector_CompactionFullSequence(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	// Reproduce the real compaction sequence from logs:
	// 1. PostToolUse → Active/Thinking
	// 2. PreCompact → Active/Compacting
	// 3. SessionStart → Idle (compaction done, session resumes)
	// 4. PreToolUse Read → Active/ToolUse (agent re-reads files)

	hc.ProcessEvent("PostToolUse", json.RawMessage(`{"tool_name": "Bash"}`))
	su := <-hc.StateCh()
	if su.SubState != SubStateThinking {
		t.Fatalf("step 1: expected Thinking, got %v", su.SubState)
	}

	hc.ProcessEvent("PreCompact", nil)
	su = <-hc.StateCh()
	if su.State != StateActive || su.SubState != SubStateCompacting {
		t.Fatalf("step 2: expected Active/Compacting, got %v/%v", su.State, su.SubState)
	}

	hc.ProcessEvent("SessionStart", nil)
	su = <-hc.StateCh()
	if su.State != StateIdle || su.SubState != SubStateNone {
		t.Fatalf("step 3: expected Idle/None, got %v/%v", su.State, su.SubState)
	}

	hc.ProcessEvent("PreToolUse", json.RawMessage(`{"tool_name": "Read"}`))
	su = <-hc.StateCh()
	if su.State != StateActive || su.SubState != SubStateToolUse {
		t.Fatalf("step 4: expected Active/ToolUse, got %v/%v", su.State, su.SubState)
	}
}

func TestHookCollector_NoteInterrupt_TransitionsToIdle(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	// First go Active via a tool use event.
	hc.ProcessEvent("PreToolUse", json.RawMessage(`{"tool_name": "Bash"}`))
	select {
	case su := <-hc.StateCh():
		if su.State != StateActive {
			t.Fatalf("expected StateActive, got %v", su.State)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for StateActive")
	}

	// NoteInterrupt should transition to Idle.
	hc.NoteInterrupt()
	select {
	case su := <-hc.StateCh():
		if su.State != StateIdle {
			t.Fatalf("expected StateIdle after NoteInterrupt, got %v", su.State)
		}
		if su.SubState != SubStateNone {
			t.Fatalf("expected SubStateNone after NoteInterrupt, got %v", su.SubState)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for StateIdle after NoteInterrupt")
	}
}

func TestHookCollector_NoteInterrupt_ClearsBlockedPermission(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	// Set blocked on permission.
	hc.ProcessEvent("permission_decision", json.RawMessage(`{"tool_name": "Bash", "decision": "ask_user"}`))
	if !hc.Snapshot().BlockedOnPermission {
		t.Fatal("should be blocked after permission_decision ask_user")
	}

	// Drain the state channel from the permission_decision event.
	select {
	case <-hc.StateCh():
	case <-time.After(time.Second):
		t.Fatal("timed out draining StateCh")
	}

	// NoteInterrupt should clear blocked state.
	hc.NoteInterrupt()
	snap := hc.Snapshot()
	if snap.BlockedOnPermission {
		t.Fatal("should not be blocked after NoteInterrupt")
	}
	if snap.BlockedToolName != "" {
		t.Fatalf("BlockedToolName should be empty after NoteInterrupt, got %q", snap.BlockedToolName)
	}
	if snap.LastEvent != "Interrupt" {
		t.Fatalf("LastEvent should be 'Interrupt', got %q", snap.LastEvent)
	}
}

func TestHookCollector_NoteInterrupt_SnapshotFields(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	before := time.Now()
	hc.NoteInterrupt()
	after := time.Now()

	snap := hc.Snapshot()
	if snap.LastEvent != "Interrupt" {
		t.Fatalf("expected LastEvent 'Interrupt', got %q", snap.LastEvent)
	}
	if snap.LastEventTime.Before(before) || snap.LastEventTime.After(after) {
		t.Fatalf("LastEventTime %v not between %v and %v", snap.LastEventTime, before, after)
	}
}

func TestHookCollector_StateCh_EventSequence(t *testing.T) {
	hc := NewHookCollector(nil)
	defer hc.Stop()

	// Simulate a typical sequence and verify each emitted StateUpdate.
	type expected struct {
		event    string
		payload  json.RawMessage
		state    State
		subState SubState
	}
	steps := []expected{
		{"UserPromptSubmit", nil, StateActive, SubStateThinking},
		{"PreToolUse", json.RawMessage(`{"tool_name": "Bash"}`), StateActive, SubStateToolUse},
		{"PostToolUse", json.RawMessage(`{"tool_name": "Bash"}`), StateActive, SubStateThinking},
	}
	for _, step := range steps {
		hc.ProcessEvent(step.event, step.payload)
		select {
		case su := <-hc.StateCh():
			if su.State != step.state {
				t.Fatalf("after %s: State = %v, want %v", step.event, su.State, step.state)
			}
			if su.SubState != step.subState {
				t.Fatalf("after %s: SubState = %v, want %v", step.event, su.SubState, step.subState)
			}
		case <-time.After(time.Second):
			t.Fatalf("after %s: timed out waiting for StateUpdate", step.event)
		}
	}
}
