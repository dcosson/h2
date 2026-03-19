package tilelayout

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestComputeLayout_Empty(t *testing.T) {
	layout := ComputeLayout(nil, 240, 60, DefaultConfig())
	if len(layout.Tabs) != 0 {
		t.Errorf("expected 0 tabs, got %d", len(layout.Tabs))
	}
	if layout.ScreenCols != 240 || layout.ScreenRows != 60 {
		t.Errorf("screen size not preserved: %dx%d", layout.ScreenCols, layout.ScreenRows)
	}
}

func TestComputeLayout_SingleAgent(t *testing.T) {
	layout := ComputeLayout([]string{"a1"}, 240, 60, DefaultConfig())
	if len(layout.Tabs) != 1 {
		t.Fatalf("expected 1 tab, got %d", len(layout.Tabs))
	}
	tab := layout.Tabs[0]
	if tab.Cols != 1 || tab.Rows != 1 {
		t.Errorf("expected 1x1, got %dx%d", tab.Cols, tab.Rows)
	}
	p := tab.Panes[0]
	if p.AgentName != "a1" || p.Width != 240 || p.Height != 60 {
		t.Errorf("pane: %+v", p)
	}
}

func TestComputeLayout_TwoAgents(t *testing.T) {
	layout := ComputeLayout([]string{"a1", "a2"}, 240, 60, DefaultConfig())
	tab := layout.Tabs[0]
	if tab.Cols != 1 || tab.Rows != 2 {
		t.Errorf("expected 1x2, got %dx%d", tab.Cols, tab.Rows)
	}
	// Single column, 2 rows: each pane 240 wide, 30 tall.
	for _, p := range tab.Panes {
		if p.Width != 240 || p.Height != 30 {
			t.Errorf("pane %s: %dx%d, want 240x30", p.AgentName, p.Width, p.Height)
		}
	}
}

func TestComputeLayout_ThreeByThree(t *testing.T) {
	agents := []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9"}
	// 240/80=3 cols, 60/20=3 rows
	layout := ComputeLayout(agents, 240, 60, DefaultConfig())
	if len(layout.Tabs) != 1 {
		t.Fatalf("expected 1 tab, got %d", len(layout.Tabs))
	}
	tab := layout.Tabs[0]
	if tab.Cols != 3 || tab.Rows != 3 {
		t.Errorf("expected 3x3, got %dx%d", tab.Cols, tab.Rows)
	}

	// Column-major: a1-a3 in col 0, a4-a6 in col 1, a7-a9 in col 2.
	expected := []struct {
		name     string
		row, col int
	}{
		{"a1", 0, 0}, {"a2", 1, 0}, {"a3", 2, 0},
		{"a4", 0, 1}, {"a5", 1, 1}, {"a6", 2, 1},
		{"a7", 0, 2}, {"a8", 1, 2}, {"a9", 2, 2},
	}
	for i, p := range tab.Panes {
		if p.AgentName != expected[i].name || p.Row != expected[i].row || p.Col != expected[i].col {
			t.Errorf("pane %d: got %+v, want name=%s row=%d col=%d",
				i, p, expected[i].name, expected[i].row, expected[i].col)
		}
		// All panes: 240/3=80 wide, 60/3=20 tall.
		if p.Width != 80 || p.Height != 20 {
			t.Errorf("pane %d: %dx%d, want 80x20", i, p.Width, p.Height)
		}
	}
}

func TestComputeLayout_UnevenLastColumn(t *testing.T) {
	agents := []string{"a1", "a2", "a3", "a4", "a5"}
	layout := ComputeLayout(agents, 240, 60, DefaultConfig())
	tab := layout.Tabs[0]
	if tab.Cols != 2 || tab.Rows != 3 {
		t.Errorf("expected 2x3, got %dx%d", tab.Cols, tab.Rows)
	}
	if tab.RowsInCol(0) != 3 {
		t.Errorf("col 0: expected 3 rows, got %d", tab.RowsInCol(0))
	}
	if tab.RowsInCol(1) != 2 {
		t.Errorf("col 1: expected 2 rows, got %d", tab.RowsInCol(1))
	}

	// Col 0 panes: 120 wide, 20 tall (60/3).
	for i, p := range tab.Panes[:3] {
		if p.Width != 120 || p.Height != 20 {
			t.Errorf("col0 pane %d: %dx%d, want 120x20", i, p.Width, p.Height)
		}
	}
	// Col 1 panes: 120 wide, 30 tall (60/2).
	for i, p := range tab.Panes[3:] {
		if p.Width != 120 || p.Height != 30 {
			t.Errorf("col1 pane %d: %dx%d, want 120x30", i, p.Width, p.Height)
		}
	}
}

func TestComputeLayout_SevenAgents(t *testing.T) {
	agents := []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7"}
	layout := ComputeLayout(agents, 240, 60, DefaultConfig())
	tab := layout.Tabs[0]
	if tab.Cols != 3 {
		t.Fatalf("expected 3 cols, got %d", tab.Cols)
	}
	if tab.RowsInCol(0) != 3 {
		t.Errorf("col 0: %d rows, want 3", tab.RowsInCol(0))
	}
	if tab.RowsInCol(1) != 3 {
		t.Errorf("col 1: %d rows, want 3", tab.RowsInCol(1))
	}
	if tab.RowsInCol(2) != 1 {
		t.Errorf("col 2: %d rows, want 1", tab.RowsInCol(2))
	}
	// Col 2 single pane gets full height.
	last := tab.Panes[6]
	if last.Height != 60 {
		t.Errorf("col2 single pane height: %d, want 60", last.Height)
	}
}

func TestComputeLayout_Overflow(t *testing.T) {
	agents := make([]string, 12)
	for i := range agents {
		agents[i] = fmt.Sprintf("a%d", i+1)
	}
	// 3x3 = 9 per tab, 12 agents → 2 tabs (9 + 3).
	layout := ComputeLayout(agents, 240, 60, DefaultConfig())
	if len(layout.Tabs) != 2 {
		t.Fatalf("expected 2 tabs, got %d", len(layout.Tabs))
	}
	if len(layout.Tabs[0].Panes) != 9 {
		t.Errorf("tab 0: expected 9 panes, got %d", len(layout.Tabs[0].Panes))
	}
	if len(layout.Tabs[1].Panes) != 3 {
		t.Errorf("tab 1: expected 3 panes, got %d", len(layout.Tabs[1].Panes))
	}
	tab1 := layout.Tabs[1]
	if tab1.Cols != 1 || tab1.Rows != 3 {
		t.Errorf("tab 1: expected 1x3, got %dx%d", tab1.Cols, tab1.Rows)
	}
}

func TestComputeLayout_SmallScreen(t *testing.T) {
	// Screen can only fit 1 pane.
	agents := []string{"a1", "a2", "a3"}
	layout := ComputeLayout(agents, 80, 20, DefaultConfig())
	if len(layout.Tabs) != 3 {
		t.Fatalf("expected 3 tabs (1 per agent), got %d", len(layout.Tabs))
	}
	for i, tab := range layout.Tabs {
		if len(tab.Panes) != 1 {
			t.Errorf("tab %d: expected 1 pane, got %d", i, len(tab.Panes))
		}
	}
}

func TestComputeLayout_ColumnMajorOrder(t *testing.T) {
	agents := []string{"a1", "a2", "a3", "a4"}
	layout := ComputeLayout(agents, 160, 60, DefaultConfig())
	tab := layout.Tabs[0]
	if tab.Panes[0].AgentName != "a1" || tab.Panes[0].Col != 0 {
		t.Errorf("pane 0: %+v", tab.Panes[0])
	}
	if tab.Panes[3].AgentName != "a4" || tab.Panes[3].Col != 1 {
		t.Errorf("pane 3: %+v", tab.Panes[3])
	}
}

func TestRowsInCol(t *testing.T) {
	tab := TabLayout{Cols: 3, Rows: 3, Panes: make([]PaneAssignment, 7)}
	if got := tab.RowsInCol(0); got != 3 {
		t.Errorf("col 0: got %d, want 3", got)
	}
	if got := tab.RowsInCol(1); got != 3 {
		t.Errorf("col 1: got %d, want 3", got)
	}
	if got := tab.RowsInCol(2); got != 1 {
		t.Errorf("col 2: got %d, want 1", got)
	}
	if got := tab.RowsInCol(3); got != 0 {
		t.Errorf("col 3 (out of range): got %d, want 0", got)
	}
}

func TestTotalPanes(t *testing.T) {
	layout := ComputeLayout([]string{"a1", "a2", "a3"}, 80, 20, DefaultConfig())
	// 3 tabs, 1 pane each.
	if got := layout.TotalPanes(); got != 3 {
		t.Errorf("TotalPanes: got %d, want 3", got)
	}
}

func TestPrintDryRun(t *testing.T) {
	agents := []string{"agent-a", "agent-b", "agent-c", "agent-d", "agent-e"}
	layout := ComputeLayout(agents, 240, 60, DefaultConfig())

	var buf bytes.Buffer
	PrintDryRun(layout, &buf)
	out := buf.String()

	if !strings.Contains(out, "5 panes across 1 tab") {
		t.Errorf("missing summary line in:\n%s", out)
	}
	if !strings.Contains(out, "240 cols x 60 rows") {
		t.Errorf("missing terminal size in:\n%s", out)
	}
	if !strings.Contains(out, "agent-a") || !strings.Contains(out, "agent-e") {
		t.Errorf("missing agent names in:\n%s", out)
	}
	// Should show Width and Height columns.
	if !strings.Contains(out, "Width") || !strings.Contains(out, "Height") {
		t.Errorf("missing dimension headers in:\n%s", out)
	}
}
