// Package tilelayout computes tiled pane layouts for terminal multiplexing.
// It is terminal-agnostic; concrete drivers (ghostty, tmux, etc.) consume
// the computed layout to create actual splits.
package tilelayout

// LayoutConfig holds constraints for the tiling grid.
type LayoutConfig struct {
	MinPaneWidth  int // minimum columns per pane
	MinPaneHeight int // minimum rows per pane
}

// DefaultConfig returns defaults sized for up to 9 panes (3x3) on a
// standard 27" monitor at default scaling (~267 cols, ~73 rows full-screen).
func DefaultConfig() LayoutConfig {
	return LayoutConfig{
		MinPaneWidth:  80,
		MinPaneHeight: 20,
	}
}

// PaneAssignment maps an agent to a grid position.
type PaneAssignment struct {
	AgentName string
	Tab       int // 0-indexed tab number
	Row       int // 0-indexed row within tab
	Col       int // 0-indexed column within tab
}

// TabLayout describes the grid for a single tab.
type TabLayout struct {
	Cols  int              // number of columns
	Rows  int              // max rows (last column may have fewer)
	Panes []PaneAssignment // column-major order
}

// RowsInCol returns the actual number of rows in the given column.
// All columns except the last are full (== Rows); the last may be shorter.
func (t TabLayout) RowsInCol(col int) int {
	n := len(t.Panes)
	start := col * t.Rows
	if start >= n {
		return 0
	}
	remaining := n - start
	if remaining > t.Rows {
		return t.Rows
	}
	return remaining
}

// TileLayout holds the complete layout across one or more tabs.
type TileLayout struct {
	Tabs []TabLayout
}

// ComputeLayout distributes agents across a tiled grid.
//
// Agents are arranged column-major: rows are filled top-to-bottom in each
// column before moving to the next column left-to-right. When a tab's grid
// is full, overflow agents go to additional tabs.
//
// screenCols/screenRows is the current terminal size (may already be a
// sub-pane of a larger window).
func ComputeLayout(agents []string, screenCols, screenRows int, cfg LayoutConfig) TileLayout {
	if len(agents) == 0 {
		return TileLayout{}
	}

	maxCols := max(1, screenCols/cfg.MinPaneWidth)
	maxRows := max(1, screenRows/cfg.MinPaneHeight)
	maxPerTab := maxCols * maxRows

	var tabs []TabLayout
	remaining := agents

	for len(remaining) > 0 {
		n := min(len(remaining), maxPerTab)
		batch := remaining[:n]
		remaining = remaining[n:]

		// Determine grid dimensions.
		rows := min(len(batch), maxRows)
		cols := (len(batch) + rows - 1) / rows

		var panes []PaneAssignment
		idx := 0
		for c := 0; c < cols && idx < len(batch); c++ {
			colRows := rows
			if leftover := len(batch) - idx; leftover < rows {
				colRows = leftover
			}
			for r := 0; r < colRows; r++ {
				panes = append(panes, PaneAssignment{
					AgentName: batch[idx],
					Tab:       len(tabs),
					Row:       r,
					Col:       c,
				})
				idx++
			}
		}

		tabs = append(tabs, TabLayout{
			Cols:  cols,
			Rows:  rows,
			Panes: panes,
		})
	}

	return TileLayout{Tabs: tabs}
}
