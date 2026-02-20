package monitor

import "testing"

func TestState_String(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{StateInitialized, "initialized"},
		{StateActive, "active"},
		{StateIdle, "idle"},
		{StateExited, "exited"},
		{State(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
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

func TestFormatStateLabel_Basic(t *testing.T) {
	tests := []struct {
		state    string
		subState string
		want     string
	}{
		{"active", "", "Active"},
		{"idle", "", "Idle"},
		{"exited", "", "Exited"},
		{"initialized", "", "Initialized"},
		{"unknown", "", "unknown"},
		{"active", "thinking", "Active (thinking)"},
		{"active", "tool_use", "Active (tool use)"},
		{"active", "waiting_for_permission", "Active (permission)"},
		{"active", "compacting", "Active (compacting)"},
		{"active", "something_new", "Active (something_new)"},
		{"idle", "thinking", "Idle (thinking)"},
	}
	for _, tt := range tests {
		got := FormatStateLabel(tt.state, tt.subState)
		if got != tt.want {
			t.Errorf("FormatStateLabel(%q, %q) = %q, want %q", tt.state, tt.subState, got, tt.want)
		}
	}
}

func TestFormatStateLabel_ToolName(t *testing.T) {
	got := FormatStateLabel("active", "tool_use", "Bash")
	if got != "Active (tool use: Bash)" {
		t.Errorf("got %q, want %q", got, "Active (tool use: Bash)")
	}

	got = FormatStateLabel("active", "tool_use", "")
	if got != "Active (tool use)" {
		t.Errorf("got %q, want %q", got, "Active (tool use)")
	}
}

func TestStateUpdate(t *testing.T) {
	su := StateUpdate{State: StateActive, SubState: SubStateThinking}
	if su.State != StateActive {
		t.Errorf("State = %v, want StateActive", su.State)
	}
	if su.SubState != SubStateThinking {
		t.Errorf("SubState = %v, want SubStateThinking", su.SubState)
	}
}
