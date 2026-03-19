package ghostty

import (
	"strings"
	"testing"

	"h2/internal/tilelayout"
)

func TestGenerateScript_TwoAgents(t *testing.T) {
	layout := tilelayout.ComputeLayout([]string{"a1", "a2"}, tilelayout.ScreenSize{Cols: 240, Rows: 60}, tilelayout.ScreenSize{}, tilelayout.DefaultConfig())
	script := generateScript(layout)

	// Cols-first: 2 agents → 2 columns, 1 right split, no down splits.
	if !strings.Contains(script, "split") && !strings.Contains(script, "direction right") {
		t.Error("expected split right")
	}
	if strings.Contains(script, "direction down") {
		t.Error("unexpected split down for 2 agents in 2 columns")
	}

	// Should type attach for a2 but not a1 (a1 gets exec'd).
	if !strings.Contains(script, `h2 attach a2`) {
		t.Error("missing attach command for a2")
	}
	if strings.Contains(script, `input text "h2 attach a1`) {
		t.Error("a1 should not be typed (it gets exec'd)")
	}
}

func TestGenerateScript_ThreeByThree(t *testing.T) {
	agents := []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9"}
	layout := tilelayout.ComputeLayout(agents, tilelayout.ScreenSize{Cols: 240, Rows: 60}, tilelayout.ScreenSize{}, tilelayout.DefaultConfig())
	script := generateScript(layout)

	// Should have 2 right splits for 3 columns.
	if strings.Count(script, "direction right") != 2 {
		t.Errorf("expected 2 split right, got %d", strings.Count(script, "direction right"))
	}

	// Each column has 3 rows → 2 down splits per column = 6 total.
	if strings.Count(script, "direction down") != 6 {
		t.Errorf("expected 6 split down, got %d", strings.Count(script, "direction down"))
	}

	// a1 should not be typed (gets exec'd), all others should.
	if strings.Contains(script, `input text "h2 attach a1`) {
		t.Error("a1 should not be typed")
	}
	for _, name := range agents[1:] {
		if !strings.Contains(script, `h2 attach `+name) {
			t.Errorf("missing attach command for %s", name)
		}
	}
}

func TestGenerateScript_UnevenColumns(t *testing.T) {
	agents := []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7"}
	layout := tilelayout.ComputeLayout(agents, tilelayout.ScreenSize{Cols: 240, Rows: 60}, tilelayout.ScreenSize{}, tilelayout.DefaultConfig())
	script := generateScript(layout)

	// 3 cols: 2 right splits.
	if strings.Count(script, "direction right") != 2 {
		t.Errorf("expected 2 split right, got %d", strings.Count(script, "direction right"))
	}
	// 7 agents in 3x3 grid: col 0 has 3 rows (2 down splits), col 1 has 3 rows (2 down splits),
	// col 2 has 1 row (0 down splits). Total = 4.
	if strings.Count(script, "direction down") != 4 {
		t.Errorf("expected 4 split down, got %d", strings.Count(script, "direction down"))
	}
}

func TestGenerateScript_MultiTab(t *testing.T) {
	agents := make([]string, 12)
	for i := range agents {
		agents[i] = "a" + string(rune('A'+i))
	}
	layout := tilelayout.ComputeLayout(agents, tilelayout.ScreenSize{Cols: 240, Rows: 60}, tilelayout.ScreenSize{}, tilelayout.DefaultConfig())
	script := generateScript(layout)

	// Should have new tab for the second tab.
	if !strings.Contains(script, "new tab in front window") {
		t.Error("expected new tab for overflow")
	}

	// Should navigate back to first tab.
	if !strings.Contains(script, `perform action "previous_tab"`) {
		t.Error("expected previous_tab to return to first tab")
	}
}

func TestGenerateScript_SinglePaneTab(t *testing.T) {
	// Single agent layout: no splits in the generated script.
	layout := tilelayout.ComputeLayout([]string{"solo"}, tilelayout.ScreenSize{Cols: 240, Rows: 60}, tilelayout.ScreenSize{}, tilelayout.DefaultConfig())
	script := generateScript(layout)

	if strings.Contains(script, "split") {
		t.Error("single pane should not have splits")
	}
}
