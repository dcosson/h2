// Package ghostty implements tiled pane layout for the Ghostty terminal.
//
// It uses Ghostty's native AppleScript support (Ghostty 1.3+) for all
// operations: splits, navigation, tabs, and writing text to panes.
// No macOS Accessibility permissions are required — this uses
// `tell application "Ghostty"`, not `tell application "System Events"`.
package ghostty

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"h2/internal/tilelayout"
)

// Driver implements tiled agent attachment for Ghostty.
type Driver struct{}

// NewDriver creates a Ghostty tiling driver.
func NewDriver() *Driver { return &Driver{} }

// ScriptForTab returns the bash script for a single tab's setup.
func (d *Driver) ScriptForTab(tab tilelayout.TabLayout, isFirstTab bool) string {
	return generateTabScript(tab, isFirstTab)
}

// TileIterative sets up tiled panes iteratively — one tab at a time.
// For overflow tabs, it opens a new tab, detects its size, computes the
// layout, and sets it up before moving to the next batch.
func (d *Driver) TileIterative(tab0 tilelayout.TabLayout, overflow []string, cfg tilelayout.LayoutConfig, h2Binary string) error {
	if len(tab0.Panes) == 0 {
		return fmt.Errorf("empty layout")
	}

	firstAgent := tab0.Panes[0].AgentName

	// Single pane, no overflow: just exec directly.
	if len(tab0.Panes) == 1 && len(overflow) == 0 {
		return syscall.Exec(h2Binary, []string{"h2", "attach", firstAgent}, os.Environ())
	}

	// Execute tab 0 setup.
	if err := runTabScript(tab0, true); err != nil {
		return fmt.Errorf("tab 0 setup failed: %w", err)
	}

	// Process overflow tabs iteratively.
	remaining := overflow
	tabIdx := 1
	for len(remaining) > 0 {
		// Open new tab.
		if err := osascript(`tell application "Ghostty" to new tab in front window`); err != nil {
			return fmt.Errorf("open tab %d: %w", tabIdx, err)
		}
		time.Sleep(500 * time.Millisecond)

		// Detect the new tab's size.
		size, err := detectCurrentTabSize()
		if err != nil {
			return fmt.Errorf("detect tab %d size: %w", tabIdx, err)
		}

		// Compute layout for this tab.
		var tab tilelayout.TabLayout
		tab, remaining = tilelayout.ComputeTabLayout(remaining, size, tabIdx, cfg)

		// Execute this tab's setup.
		if err := runTabScript(tab, false); err != nil {
			return fmt.Errorf("tab %d setup failed: %w", tabIdx, err)
		}

		tabIdx++
	}

	// Navigate back to tab 0.
	if tabIdx > 1 {
		for i := 1; i < tabIdx; i++ {
			osascript(fmt.Sprintf(`tell application "Ghostty" to perform action "previous_tab" on (%s)`, ghosttyTerm))
			time.Sleep(100 * time.Millisecond)
		}
	}

	// Navigate to (0,0) in tab 0 for the exec.
	for r := 1; r < tab0.Rows; r++ {
		osascript(fmt.Sprintf(`tell application "Ghostty" to perform action "goto_split:up" on (%s)`, ghosttyTerm))
	}
	if tab0.Rows > 1 {
		time.Sleep(50 * time.Millisecond)
	}
	for c := 1; c < tab0.Cols; c++ {
		osascript(fmt.Sprintf(`tell application "Ghostty" to perform action "goto_split:left" on (%s)`, ghosttyTerm))
	}
	if tab0.Cols > 1 {
		time.Sleep(50 * time.Millisecond)
	}

	// Replace current process with h2 attach for the first agent.
	return syscall.Exec(h2Binary, []string{"h2", "attach", firstAgent}, os.Environ())
}

// detectCurrentTabSize reads the terminal size of the currently focused tab
// by typing a tput command that writes to a temp file.
func detectCurrentTabSize() (tilelayout.ScreenSize, error) {
	tmpFile, err := os.CreateTemp("", "h2-tabsize-*.txt")
	if err != nil {
		return tilelayout.ScreenSize{}, err
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// Type size detection command + press Enter.
	sizeCmd := fmt.Sprintf(`echo "$(tput cols) $(tput lines)" > %s`, tmpPath)
	escaped := strings.ReplaceAll(sizeCmd, `"`, `\"`)
	osascript(fmt.Sprintf(`tell application "Ghostty"
set t to %s
input text "%s" to t
send key "enter" to t
end tell`, ghosttyTerm, escaped))

	// Wait for the file to be written.
	for i := 0; i < 20; i++ {
		time.Sleep(200 * time.Millisecond)
		data, err := os.ReadFile(tmpPath)
		if err != nil || len(strings.TrimSpace(string(data))) == 0 {
			continue
		}
		parts := strings.Fields(strings.TrimSpace(string(data)))
		if len(parts) == 2 {
			cols, errC := strconv.Atoi(parts[0])
			rows, errR := strconv.Atoi(parts[1])
			if errC == nil && errR == nil && cols > 0 && rows > 0 {
				return tilelayout.ScreenSize{Cols: cols, Rows: rows}, nil
			}
		}
	}

	return tilelayout.ScreenSize{}, fmt.Errorf("timed out reading tab size")
}

// osascript runs an AppleScript snippet via stdin to preserve escape sequences.
func osascript(script string) error {
	cmd := exec.Command("osascript")
	cmd.Stdin = strings.NewReader(script)
	return cmd.Run()
}

// ghosttyTerm is the AppleScript expression for the focused terminal.
const ghosttyTerm = "focused terminal of selected tab of front window"

// runTabScript generates and executes the bash script for a single tab.
func runTabScript(tab tilelayout.TabLayout, isFirstTab bool) error {
	script := generateTabScript(tab, isFirstTab)

	f, err := os.CreateTemp("", "h2-tile-*.sh")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	defer os.Remove(tmpPath)

	if _, err := f.WriteString(script); err != nil {
		f.Close()
		return err
	}
	f.Close()

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return err
	}

	cmd := exec.Command("bash", tmpPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// generateTabScript produces a bash script for setting up a single tab.
func generateTabScript(tab tilelayout.TabLayout, isFirstTab bool) string {
	var b strings.Builder
	b.WriteString("#!/bin/bash\n")
	b.WriteString("# Generated by h2 attach --tile\n\n")

	writeBuildPhase(&b, tab)

	// Wait for shells in new panes to initialize.
	writeSleep(&b, 800)

	writeTypePhase(&b, tab, isFirstTab)

	return b.String()
}

// writeBuildPhase emits commands to create the split grid for one tab.
func writeBuildPhase(b *strings.Builder, tab tilelayout.TabLayout) {
	if tab.Cols <= 1 && tab.RowsInCol(0) <= 1 {
		return
	}

	b.WriteString("# Build grid structure\n")

	// Create columns by splitting right.
	for c := 1; c < tab.Cols; c++ {
		writeSplit(b, "right")
		writeSleep(b, 300)
	}

	// Equalize columns after creating them.
	if tab.Cols > 1 {
		writePerformAction(b, "equalize_splits")
		writeSleep(b, 100)
	}

	// Build rows in each column, right-to-left.
	// After column splits, focus is on the rightmost column.
	for c := tab.Cols - 1; c >= 0; c-- {
		if c < tab.Cols-1 {
			writePerformAction(b, "goto_split:left")
			writeSleep(b, 100)
		}
		colRows := tab.RowsInCol(c)
		for r := 1; r < colRows; r++ {
			writeSplit(b, "down")
			writeSleep(b, 300)
		}
	}

	// Equalize all splits after building the full grid.
	if tab.Cols > 1 || tab.RowsInCol(0) > 1 {
		writePerformAction(b, "equalize_splits")
		writeSleep(b, 100)
	}
	b.WriteByte('\n')
}

// writeTypePhase emits commands to type h2 attach in each pane.
func writeTypePhase(b *strings.Builder, tab tilelayout.TabLayout, isFirstTab bool) {
	if len(tab.Panes) <= 1 && isFirstTab {
		return
	}

	b.WriteString("# Type attach commands\n")

	// After build phase, focus is at bottom of leftmost column.
	// Navigate up to (0,0).
	col0Rows := tab.RowsInCol(0)
	for r := 1; r < col0Rows; r++ {
		writePerformAction(b, "goto_split:up")
		writeSleep(b, 50)
	}

	for c := 0; c < tab.Cols; c++ {
		if c > 0 {
			// Return to top of previous column, then move right.
			prevColRows := tab.RowsInCol(c - 1)
			for r := 1; r < prevColRows; r++ {
				writePerformAction(b, "goto_split:up")
				writeSleep(b, 50)
			}
			writePerformAction(b, "goto_split:right")
			writeSleep(b, 50)
		}

		colRows := tab.RowsInCol(c)
		for r := 0; r < colRows; r++ {
			if isFirstTab && c == 0 && r == 0 {
				// Skip (0,0) of first tab — will exec there later.
				if colRows > 1 {
					writePerformAction(b, "goto_split:down")
					writeSleep(b, 50)
				}
				continue
			}

			paneIdx := c*tab.Rows + r
			if paneIdx < len(tab.Panes) {
				writeTypeAttach(b, tab.Panes[paneIdx].AgentName)
			}

			if r < colRows-1 {
				writePerformAction(b, "goto_split:down")
				writeSleep(b, 50)
			}
		}
	}
	b.WriteByte('\n')
}

func writePerformAction(b *strings.Builder, action string) {
	fmt.Fprintf(b, "osascript -e 'tell application \"Ghostty\" to perform action \"%s\" on (%s)'\n", action, ghosttyTerm)
}

func writeSplit(b *strings.Builder, direction string) {
	fmt.Fprintf(b, "osascript -e 'tell application \"Ghostty\" to split (%s) direction %s'\n", ghosttyTerm, direction)
}

func writeSleep(b *strings.Builder, ms int) {
	fmt.Fprintf(b, "sleep %.2f\n", float64(ms)/1000)
}

func writeTypeAttach(b *strings.Builder, agentName string) {
	escaped := strings.ReplaceAll(agentName, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	fmt.Fprintf(b, `osascript <<'APPLESCRIPT'
tell application "Ghostty"
set t to %s
input text "h2 attach %s" to t
send key "enter" to t
end tell
APPLESCRIPT
`, ghosttyTerm, escaped)
	writeSleep(b, 300)
}
