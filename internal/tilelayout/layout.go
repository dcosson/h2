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
		MinPaneWidth:  79,
		MinPaneHeight: 19,
	}
}

// ScreenSize holds terminal dimensions for a tab.
type ScreenSize struct {
	Cols int
	Rows int
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
	Tabs []TabLayout
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
// currentSize is the terminal size of the pane where the command is run
// (may be a sub-split). overflowSize is the size of new tabs created for
// overflow agents (typically the full window size). If overflowSize is zero,
// it defaults to currentSize.
func ComputeLayout(agents []string, currentSize, overflowSize ScreenSize, cfg LayoutConfig) TileLayout {
	if len(agents) == 0 {
		return TileLayout{}
	}

	if overflowSize.Cols == 0 || overflowSize.Rows == 0 {
		overflowSize = currentSize
	}

	var tabs []TabLayout
	remaining := agents

	for len(remaining) > 0 {
		// First tab uses current pane size; overflow tabs use full window size.
		screen := overflowSize
		if len(tabs) == 0 {
			screen = currentSize
		}

		maxCols := max(1, screen.Cols/cfg.MinPaneWidth)
		maxRows := max(1, screen.Rows/cfg.MinPaneHeight)
		maxPerTab := maxCols * maxRows

		n := min(len(remaining), maxPerTab)
		batch := remaining[:n]
		remaining = remaining[n:]

		// Determine grid dimensions: fill columns (horizontal) first,
		// then add rows. This keeps panes wider on wide monitors.
		cols := min(len(batch), maxCols)
		rows := (len(batch) + cols - 1) / cols

		// Compute pane dimensions. Last column/row absorbs remainder
		// so the total adds up to the screen size exactly.
		baseWidth := screen.Cols / max(1, cols)

		var panes []PaneAssignment
		idx := 0
		for c := 0; c < cols && idx < len(batch); c++ {
			colRows := rows
			if leftover := len(batch) - idx; leftover < rows {
				colRows = leftover
			}
			baseHeight := screen.Rows / max(1, colRows)

			paneWidth := baseWidth
			if c == cols-1 {
				paneWidth = screen.Cols - baseWidth*(cols-1)
			}

			for r := 0; r < colRows; r++ {
				paneHeight := baseHeight
				if r == colRows-1 {
					paneHeight = screen.Rows - baseHeight*(colRows-1)
				}
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
			ScreenCols: screen.Cols,
			ScreenRows: screen.Rows,
			Panes:      panes,
		})
	}

	return TileLayout{Tabs: tabs}
}

// PrintDryRun writes a human-readable summary of the layout to w.
func PrintDryRun(layout TileLayout, w io.Writer) {
	total := layout.TotalPanes()
	nTabs := len(layout.Tabs)

	fmt.Fprintf(w, "Tile layout: %d panes across %d tab(s)\n", total, nTabs)

	paneNum := 1
	for tabIdx, tab := range layout.Tabs {
		label := "current pane"
		if tabIdx > 0 {
			label = "new tab"
		}
		fmt.Fprintf(w, "\nTab %d — %s (%d cols x %d rows, %d x %d grid, %d panes):\n",
			tabIdx+1, label, tab.ScreenCols, tab.ScreenRows, tab.Cols, tab.Rows, len(tab.Panes))
		fmt.Fprintf(w, "  %-4s %-28s %5s %5s %7s %8s\n", "#", "Agent", "Col", "Row", "Width", "Height")
		fmt.Fprintf(w, "  %-4s %-28s %5s %5s %7s %8s\n", "---", "---", "---", "---", "-----", "------")

		for _, p := range tab.Panes {
			fmt.Fprintf(w, "  %-4d %-28s %5d %5d %7d %8d\n",
				paneNum, p.AgentName, p.Col, p.Row, p.Width, p.Height)
			paneNum++
		}
	}
}
