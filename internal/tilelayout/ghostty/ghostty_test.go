package ghostty

import (
	"strings"
	"testing"

	"h2/internal/tilelayout"
)

func TestGenerateScript_TwoAgents(t *testing.T) {
	layout := tilelayout.ComputeLayout([]string{"a1", "a2"}, 240, 60, tilelayout.DefaultConfig())
	script := generateScript(layout)

	// Should have 1 down split and no right splits.
	if !strings.Contains(script, "new_split:down") {
		t.Error("expected new_split:down")
	}
	if strings.Contains(script, "new_split:right") {
		t.Error("unexpected new_split:right for 2 agents in 1 column")
	}

	// Should type attach for a2 but not a1 (a1 gets exec'd).
	if !strings.Contains(script, `keystroke "h2 attach a2"`) {
		t.Error("missing keystroke for a2")
	}
	if strings.Contains(script, `keystroke "h2 attach a1"`) {
		t.Error("a1 should not be typed (it gets exec'd)")
	}
}

func TestGenerateScript_ThreeByThree(t *testing.T) {
	agents := []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9"}
	layout := tilelayout.ComputeLayout(agents, 240, 60, tilelayout.DefaultConfig())
	script := generateScript(layout)

	// Should have 2 right splits for 3 columns.
	if strings.Count(script, "new_split:right") != 2 {
		t.Errorf("expected 2 new_split:right, got %d", strings.Count(script, "new_split:right"))
	}

	// Each column has 3 rows → 2 down splits per column = 6 total.
	if strings.Count(script, "new_split:down") != 6 {
		t.Errorf("expected 6 new_split:down, got %d", strings.Count(script, "new_split:down"))
	}

	// a1 should not be typed (gets exec'd), all others should.
	if strings.Contains(script, `keystroke "h2 attach a1"`) {
		t.Error("a1 should not be typed")
	}
	for _, name := range agents[1:] {
		if !strings.Contains(script, `keystroke "h2 attach `+name+`"`) {
			t.Errorf("missing keystroke for %s", name)
		}
	}
}

func TestGenerateScript_UnevenColumns(t *testing.T) {
	agents := []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7"}
	layout := tilelayout.ComputeLayout(agents, 240, 60, tilelayout.DefaultConfig())
	script := generateScript(layout)

	// 3 cols: 2 right splits. Rows: 2+2+0 down splits for col 0,1 (3 rows each),
	// 0 down splits for col 2 (1 row). Total down = 2+2+0 = 4.
	if strings.Count(script, "new_split:right") != 2 {
		t.Errorf("expected 2 new_split:right, got %d", strings.Count(script, "new_split:right"))
	}
	if strings.Count(script, "new_split:down") != 4 {
		t.Errorf("expected 4 new_split:down, got %d", strings.Count(script, "new_split:down"))
	}
}

func TestGenerateScript_MultiTab(t *testing.T) {
	agents := make([]string, 12)
	for i := range agents {
		agents[i] = "a" + string(rune('A'+i))
	}
	layout := tilelayout.ComputeLayout(agents, 240, 60, tilelayout.DefaultConfig())
	script := generateScript(layout)

	// Should have new_tab for the second tab.
	if !strings.Contains(script, "new_tab") {
		t.Error("expected new_tab for overflow")
	}

	// Should navigate back to first tab.
	if !strings.Contains(script, "previous_tab") {
		t.Error("expected previous_tab to return to first tab")
	}
}

func TestGenerateScript_SinglePaneTab(t *testing.T) {
	// Single agent layout: no splits in the generated script.
	layout := tilelayout.ComputeLayout([]string{"solo"}, 240, 60, tilelayout.DefaultConfig())
	script := generateScript(layout)

	if strings.Contains(script, "new_split") {
		t.Error("single pane should not have splits")
	}
}
