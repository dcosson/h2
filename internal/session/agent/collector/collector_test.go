package collector

import "testing"

func TestFormatStateLabel(t *testing.T) {
	tests := []struct {
		state    string
		subState string
		want     string
	}{
		// No sub-state — just capitalized state.
		{"active", "", "Active"},
		{"idle", "", "Idle"},
		{"exited", "", "Exited"},
		{"initialized", "", "Initialized"},
		{"unknown", "", "unknown"},

		// Active with sub-states.
		{"active", "thinking", "Active (thinking)"},
		{"active", "tool_use", "Active (tool use)"},
		{"active", "waiting_for_permission", "Active (permission)"},

		// Unknown sub-state passed through.
		{"active", "something_new", "Active (something_new)"},

		// Non-active state with sub-state (unlikely but handled).
		{"idle", "thinking", "Idle (thinking)"},
	}
	for _, tt := range tests {
		got := FormatStateLabel(tt.state, tt.subState)
		if got != tt.want {
			t.Errorf("FormatStateLabel(%q, %q) = %q, want %q", tt.state, tt.subState, got, tt.want)
		}
	}
}

func TestFormatStateLabel_WithToolName(t *testing.T) {
	// Tool name only appears for tool_use sub-state.
	got := FormatStateLabel("active", "tool_use", "Bash")
	if got != "Active (tool use: Bash)" {
		t.Errorf("got %q, want %q", got, "Active (tool use: Bash)")
	}

	// Empty tool name — no suffix.
	got = FormatStateLabel("active", "tool_use", "")
	if got != "Active (tool use)" {
		t.Errorf("got %q, want %q", got, "Active (tool use)")
	}

	// Tool name ignored for non-tool_use sub-states.
	got = FormatStateLabel("active", "thinking", "Bash")
	if got != "Active (thinking)" {
		t.Errorf("got %q, want %q", got, "Active (thinking)")
	}
}
