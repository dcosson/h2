package tilelayout

import (
	"fmt"
	"testing"
)

func TestComputeLayout_Empty(t *testing.T) {
	layout := ComputeLayout(nil, 240, 60, DefaultConfig())
	if len(layout.Tabs) != 0 {
		t.Errorf("expected 0 tabs, got %d", len(layout.Tabs))
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
	if len(tab.Panes) != 1 || tab.Panes[0].AgentName != "a1" {
		t.Errorf("unexpected pane: %+v", tab.Panes)
	}
}

func TestComputeLayout_TwoAgents(t *testing.T) {
	layout := ComputeLayout([]string{"a1", "a2"}, 240, 60, DefaultConfig())
	tab := layout.Tabs[0]
	if tab.Cols != 1 || tab.Rows != 2 {
		t.Errorf("expected 1x2, got %dx%d", tab.Cols, tab.Rows)
	}
	if tab.Panes[0].Row != 0 || tab.Panes[1].Row != 1 {
		t.Errorf("expected rows 0,1; got %d,%d", tab.Panes[0].Row, tab.Panes[1].Row)
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
	// Last agent should be in col 1, row 1.
	last := tab.Panes[4]
	if last.AgentName != "a5" || last.Col != 1 || last.Row != 1 {
		t.Errorf("last pane: %+v", last)
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
	// Tab 1 agents should be a10, a11, a12 in a single column.
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
	// 160/80=2 cols, 60/20=3 rows. 4 agents → 2 cols, col0=3, col1=1? No:
	// rows = min(4, 3) = 3, cols = ceil(4/3) = 2. col0=3, col1=1.
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
