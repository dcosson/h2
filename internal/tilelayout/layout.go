// Package tilelayout computes tiled pane layouts for terminal multiplexing.
// It is terminal-agnostic; concrete drivers (ghostty, tmux, etc.) consume
// the computed layout to create actual splits.
package tilelayout

import (
	"fmt"
	"io"
)

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

// PaneAssignment maps an agent to a grid position with computed dimensions.
type PaneAssignment struct {
	AgentName string
	Tab       int // 0-indexed tab number
	Row       int // 0-indexed row within tab
	Col       int // 0-indexed column within tab
	Width     int // approximate pane width (columns)
	Height    int // approximate pane height (rows)
}

// TabLayout describes the grid for a single tab.
type TabLayout struct {
	Cols       int              // number of columns
	Rows       int              // max rows (last column may have fewer)
	ScreenCols int              // available terminal columns for this tab
	ScreenRows int              // available terminal rows for this tab
	Panes      []PaneAssignment // column-major order
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
	Tabs       []TabLayout
	ScreenCols int // original terminal width
	ScreenRows int // original terminal height
}

// TotalPanes returns the total number of panes across all tabs.
func (l TileLayout) TotalPanes() int {
	n := 0
	for _, tab := range l.Tabs {
		n += len(tab.Panes)
	}
	return n
}

// ComputeLayout distributes agents across a tiled grid.
//
// Agents are arranged column-major: rows are filled top-to-bottom in each
// column before moving to the next column left-to-right. When a tab's grid
// is full, overflow agents go to additional tabs.
//
// screenCols/screenRows is the current terminal size (may already be a
// sub-pane of a larger window). Pane dimensions are computed from the grid.
func ComputeLayout(agents []string, screenCols, screenRows int, cfg LayoutConfig) TileLayout {
	if len(agents) == 0 {
		return TileLayout{ScreenCols: screenCols, ScreenRows: screenRows}
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

		// Compute pane dimensions.
		paneWidth := screenCols / max(1, cols)

		var panes []PaneAssignment
		idx := 0
		for c := 0; c < cols && idx < len(batch); c++ {
			colRows := rows
			if leftover := len(batch) - idx; leftover < rows {
				colRows = leftover
			}
			paneHeight := screenRows / max(1, colRows)

			for r := 0; r < colRows; r++ {
				panes = append(panes, PaneAssignment{
					AgentName: batch[idx],
					Tab:       len(tabs),
					Row:       r,
					Col:       c,
					Width:     paneWidth,
					Height:    paneHeight,
				})
				idx++
			}
		}

		tabs = append(tabs, TabLayout{
			Cols:       cols,
			Rows:       rows,
			ScreenCols: screenCols,
			ScreenRows: screenRows,
			Panes:      panes,
		})
	}

	return TileLayout{Tabs: tabs, ScreenCols: screenCols, ScreenRows: screenRows}
}

// PrintDryRun writes a human-readable summary of the layout to w.
func PrintDryRun(layout TileLayout, w io.Writer) {
	total := layout.TotalPanes()
	nTabs := len(layout.Tabs)

	fmt.Fprintf(w, "Tile layout: %d panes across %d tab(s)\n", total, nTabs)
	fmt.Fprintf(w, "Terminal size: %d cols x %d rows\n", layout.ScreenCols, layout.ScreenRows)

	paneNum := 1
	for tabIdx, tab := range layout.Tabs {
		fmt.Fprintf(w, "\nTab %d (%d x %d grid, %d panes):\n", tabIdx+1, tab.Cols, tab.Rows, len(tab.Panes))
		fmt.Fprintf(w, "  %-4s %-28s %5s %5s %7s %8s\n", "#", "Agent", "Col", "Row", "Width", "Height")
		fmt.Fprintf(w, "  %-4s %-28s %5s %5s %7s %8s\n", "---", "---", "---", "---", "-----", "------")

		for _, p := range tab.Panes {
			fmt.Fprintf(w, "  %-4d %-28s %5d %5d %7d %8d\n",
				paneNum, p.AgentName, p.Col, p.Row, p.Width, p.Height)
			paneNum++
		}
	}
}
