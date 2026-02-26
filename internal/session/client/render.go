package client

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/vito/midterm"

	"h2/internal/session/agent/monitor"
	"h2/internal/session/message"
	"h2/internal/session/virtualterminal"
)

// RenderScreen renders the virtual terminal buffer to the output.
// Uses DECSC/DECRC to save and restore cursor position so the cursor
// stays on the input bar (positioned by RenderInputBar). Re-asserts
// cursor visibility afterward because child output may contain
// \033[?25l which gets forwarded to the outer terminal. This only
// fires during active output (PipeOutput), so it doesn't affect
// cursor blink during idle.
func (c *Client) RenderScreen() {
	var buf bytes.Buffer
	buf.WriteString("\0337") // DECSC: save cursor position
	if c.IsScrollMode() {
		c.renderScrollView(&buf)
	} else {
		c.renderLiveView(&buf)
	}
	c.renderSelectHint(&buf)
	buf.WriteString("\0338") // DECRC: restore cursor position
	// Re-assert cursor visibility — child output may toggle it via
	// forwarded escape sequences. During active output the blink timer
	// reset is invisible; during idle PipeOutput doesn't fire.
	if c.Mode != ModePassthrough && c.Mode != ModePassthroughScroll {
		buf.WriteString("\033[?25h")
	}
	c.OutputMu.Lock()
	c.Output.Write(buf.Bytes())
	c.OutputMu.Unlock()
}

// renderSelectHint draws the "hold shift to select" hint when active.
func (c *Client) renderSelectHint(buf *bytes.Buffer) {
	if !c.SelectHint {
		return
	}
	hint := "(hold shift to select)"
	row := 1
	if c.IsScrollMode() {
		row = 2
	}
	col := c.VT.Cols - len(hint) + 1
	if col < 1 {
		col = 1
	}
	fmt.Fprintf(buf, "\033[%d;%dH\033[7m%s\033[0m", row, col, hint)
}

// renderLiveView renders the live terminal content, anchored to the cursor.
// midterm can grow Content/Height beyond ChildRows (via ensureHeight), so
// the cursor position—not row 0 or len(Content)—determines the visible window.
func (c *Client) renderLiveView(buf *bytes.Buffer) {
	startRow := c.VT.Vt.Cursor.Y - c.VT.ChildRows + 1
	if startRow < 0 {
		startRow = 0
	}
	for i := 0; i < c.VT.ChildRows; i++ {
		fmt.Fprintf(buf, "\033[%d;1H", i+1)
		c.RenderLineFrom(buf, c.VT.Vt, startRow+i)
		buf.WriteString("\033[0m\033[K") // erase trailing stale content with default bg
	}
}

// renderScrollView renders the scrollback buffer at the current ScrollOffset.
func (c *Client) renderScrollView(buf *bytes.Buffer) {
	// Prefer ScrollHistory (captured via midterm OnScrollback) when available.
	// This is populated for apps that use scroll regions (e.g. codex inline viewport).
	if c.hasScrollHistory() {
		c.renderScrollViewHistory(buf)
		return
	}
	// Fallback to AppendOnly scrollback for apps without scroll regions (e.g. Claude Code).
	sb := c.VT.Scrollback
	if sb == nil {
		c.renderLiveView(buf)
		return
	}
	bottom := c.scrollbackScrollBottom()
	startRow := bottom - c.VT.ChildRows + 1 - c.ScrollOffset
	if startRow < 0 {
		startRow = 0
	}
	for i := 0; i < c.VT.ChildRows; i++ {
		fmt.Fprintf(buf, "\033[%d;1H", i+1)
		row := startRow + i
		if row >= 0 && row < len(sb.Content) {
			c.RenderLineFrom(buf, sb, row)
		}
		buf.WriteString("\033[0m\033[K")
	}
	c.renderScrollIndicator(buf)
}

// renderScrollViewHistory renders using ScrollHistory (scrolled-off lines from
// VT.Vt's OnScrollback callback) combined with the live VT.Vt screen content.
// The full content is: [ScrollHistory...] ++ [VT.Vt.Content rows].
func (c *Client) renderScrollViewHistory(buf *bytes.Buffer) {
	histLen := c.scrollHistoryLen()
	totalRows := histLen + c.VT.ChildRows

	// startRow is the index into the combined [ScrollHistory + live] buffer.
	startRow := totalRows - c.VT.ChildRows - c.ScrollOffset
	if startRow < 0 {
		startRow = 0
	}

	for i := 0; i < c.VT.ChildRows; i++ {
		fmt.Fprintf(buf, "\033[%d;1H", i+1)
		row := startRow + i
		if row >= 0 && row < totalRows {
			if row < histLen {
				// Render from ScrollHistory (ANSI-formatted string).
				line := c.VT.ScrollHistory[row]
				buf.WriteString(line)
			} else {
				// Render from live VT.Vt content.
				vtRow := row - histLen
				c.RenderLineFrom(buf, c.VT.Vt, vtRow)
			}
		}
		buf.WriteString("\033[0m\033[K")
	}
	c.renderScrollIndicator(buf)
}

// renderScrollIndicator draws the "(scrolling)" indicator at row 1, right-aligned.
func (c *Client) renderScrollIndicator(buf *bytes.Buffer) {
	indicator := "(scrolling)"
	if c.DebugScroll {
		maxOffset, _ := c.scrollMaxOffset()
		mode := "sb"
		if c.hasScrollHistory() {
			mode = "hist"
		}
		sbLen, sbCurY := 0, 0
		if c.VT.Scrollback != nil {
			sbLen = len(c.VT.Scrollback.Content)
			sbCurY = c.VT.Scrollback.Cursor.Y
		}
		indicator = fmt.Sprintf(
			"(scroll %s off=%d/%d hist=%d sb=%d curY=%d child=%d sr=%v)",
			mode,
			c.ScrollOffset,
			maxOffset,
			len(c.VT.ScrollHistory),
			sbLen,
			sbCurY,
			c.VT.ChildRows,
			c.VT.ScrollRegionUsed,
		)
	}
	col := c.VT.Cols - len(indicator) + 1
	if col < 1 {
		col = 1
	}
	fmt.Fprintf(buf, "\033[1;%dH\033[7m%s\033[0m", col, indicator)
}

// RenderLineFrom writes one row of the given terminal to buf.
// This uses explicit SGR resets between format regions to prevent
// background colors from bleeding across regions (midterm's RenderLine
// does not reset between regions).
func (c *Client) RenderLineFrom(buf *bytes.Buffer, vt *midterm.Terminal, row int) {
	if row >= len(vt.Content) {
		return
	}
	line := vt.Content[row]
	var pos int
	var lastFormat midterm.Format
	for region := range vt.Format.Regions(row) {
		f := region.F
		if f != lastFormat {
			buf.WriteString("\033[0m")
			buf.WriteString(f.Render())
			lastFormat = f
		}
		end := pos + region.Size
		if pos < len(line) {
			contentEnd := end
			if contentEnd > len(line) {
				contentEnd = len(line)
			}
			buf.WriteString(string(line[pos:contentEnd]))
		}
		pos = end
	}
	buf.WriteString("\033[0m")
}

// RenderLine writes one row of the primary virtual terminal to buf.
func (c *Client) RenderLine(buf *bytes.Buffer, row int) {
	c.RenderLineFrom(buf, c.VT.Vt, row)
}

// RenderStatusBar draws the separator line with mode, status, and help text.
// Uses DECSC/DECRC to preserve cursor position so the 1-second TickStatus
// doesn't move the cursor away from the input bar.
func (c *Client) RenderStatusBar() {
	var buf bytes.Buffer
	buf.WriteString("\0337") // DECSC: save cursor position

	sepRow := c.VT.Rows - 1
	if c.DebugKeys {
		sepRow = c.VT.Rows - 2
	}

	// --- Separator line ---
	fmt.Fprintf(&buf, "\033[%d;1H\033[2K", sepRow)

	var style, label string
	if c.VT.ChildExited {
		style = "\033[7m\033[31m" // red inverse
		if c.IsScrollMode() {
			label = " Scroll | " + c.exitMessage() + " | Esc exit"
		} else {
			label = " " + c.exitMessage() + " | [Enter] relaunch \u00b7 [q] quit"
		}
	} else {
		style = c.ModeBarStyle()
		help := c.HelpLabel()
		label = " " + c.ModeLabel()

		if c.Mode != ModeMenu {
			status := c.StatusLabel()
			label += " | " + status
			if c.WorkingDir != nil {
				if wd := strings.TrimSpace(c.WorkingDir()); wd != "" {
					label += " | " + c.formatWorkingDirForBar(wd)
				}
			}

			// OTEL metrics (tokens and cost)
			if c.OtelMetrics != nil {
				inTok, outTok, cost, connected, port := c.OtelMetrics()
				if connected {
					label += " | " + monitor.FormatTokens(inTok) + "/" + monitor.FormatTokens(outTok) + " " + monitor.FormatCost(cost)
				} else {
					label += fmt.Sprintf(" | [otel:%d]", port)
				}
			}

			// Queue indicator
			if c.QueueStatus != nil {
				count, paused := c.QueueStatus()
				if count > 0 {
					if paused {
						label += fmt.Sprintf(" | [%d paused]", count)
					} else {
						label += fmt.Sprintf(" | [%d queued]", count)
					}
				}
			}
		}

		if help != "" {
			label += " | " + help
		}
	}

	right := ""
	if c.AgentName != "" {
		right = c.AgentName + " "
	}

	if len(label)+len(right) > c.VT.Cols {
		if !c.VT.ChildExited {
			// Tight on space - drop help first, then right-align.
			label = " " + c.ModeLabel()
			if c.Mode != ModeMenu {
				label += " | " + c.StatusLabel()
			}
		}
		if len(label)+len(right) > c.VT.Cols {
			if len(label) > c.VT.Cols {
				label = label[:c.VT.Cols]
			}
			right = ""
		}
	}

	buf.WriteString(style)
	buf.WriteString(label)
	gap := c.VT.Cols - len(label) - len(right)
	if gap > 0 {
		buf.WriteString(strings.Repeat(" ", gap))
	}
	buf.WriteString(right)
	buf.WriteString("\033[0m")
	buf.WriteString("\0338") // DECRC: restore cursor position

	c.OutputMu.Lock()
	c.Output.Write(buf.Bytes())
	c.OutputMu.Unlock()
}

// RenderInputBar draws the input prompt, text, cursor, and debug line.
// Cursor visibility (show/hide) is managed here so that it only changes
// on user-initiated renders, preserving the terminal's cursor blink timer.
func (c *Client) RenderInputBar() {
	var buf bytes.Buffer

	inputRow := c.VT.Rows
	debugRow := 0
	if c.DebugKeys {
		inputRow = c.VT.Rows - 1
		debugRow = c.VT.Rows
	}

	// --- Input line ---
	prompt := c.InputPriority.String() + " > "
	maxInput := c.VT.Cols - len(prompt)

	inputRunes := []rune(string(c.Input))
	totalRunes := len(inputRunes)
	cursorRunePos := utf8.RuneCount(c.Input[:c.CursorPos])

	// Determine the visible window of runes, keeping the cursor in view.
	displayStart := 0
	if totalRunes > maxInput && maxInput > 0 {
		displayStart = cursorRunePos - maxInput + 1
		if displayStart < 0 {
			displayStart = 0
		}
		if displayStart+maxInput > totalRunes {
			displayStart = totalRunes - maxInput
			if displayStart < 0 {
				displayStart = 0
			}
		}
	}
	displayEnd := displayStart + maxInput
	if displayEnd > totalRunes {
		displayEnd = totalRunes
	}

	displayInput := string(inputRunes[displayStart:displayEnd])

	fmt.Fprintf(&buf, "\033[%d;1H\033[2K", inputRow)
	promptColor := "\033[36m" // cyan
	if c.InputPriority == message.PriorityInterrupt {
		promptColor = "\033[31m" // red
	}
	fmt.Fprintf(&buf, "%s%s\033[0m%s", promptColor, prompt, displayInput)

	cursorCol := len(prompt) + (cursorRunePos - displayStart) + 1
	if cursorCol > c.VT.Cols {
		cursorCol = c.VT.Cols
	}
	fmt.Fprintf(&buf, "\033[%d;%dH", inputRow, cursorCol)

	if c.DebugKeys {
		fmt.Fprintf(&buf, "\033[%d;1H\033[2K", debugRow)
		debugLabel := c.DebugLabel()
		if len(debugLabel) > c.VT.Cols {
			debugLabel = virtualterminal.TrimLeftToWidth(debugLabel, c.VT.Cols)
		}
		buf.WriteString(debugLabel)
		if pad := c.VT.Cols - len(debugLabel); pad > 0 {
			buf.WriteString(strings.Repeat(" ", pad))
		}
		// Reposition cursor back to input line after debug row.
		fmt.Fprintf(&buf, "\033[%d;%dH", inputRow, cursorCol)
	}

	if c.Mode == ModePassthrough || c.Mode == ModePassthroughScroll {
		buf.WriteString("\033[?25l")
	} else {
		buf.WriteString("\033[?25h")
	}

	c.OutputMu.Lock()
	c.Output.Write(buf.Bytes())
	c.OutputMu.Unlock()
}

// RenderBar draws both the status bar and input bar.
// Use this for full bar repaints (mode changes, resize, child exit).
// For targeted updates, prefer RenderStatusBar or RenderInputBar.
func (c *Client) RenderBar() {
	c.RenderStatusBar()
	c.RenderInputBar()
}

// ModeLabel returns the display name for the current mode.
func (c *Client) ModeLabel() string {
	switch c.Mode {
	case ModePassthrough:
		return "Passthrough"
	case ModeMenu:
		return c.MenuLabel()
	case ModeScroll:
		return "Scroll"
	case ModePassthroughScroll:
		return "Scroll (PT)"
	default:
		return "Normal"
	}
}

// ModeBarStyle returns the ANSI style for the current mode.
func (c *Client) ModeBarStyle() string {
	switch c.Mode {
	case ModePassthrough, ModePassthroughScroll:
		return "\033[7m\033[33m"
	case ModeMenu:
		return "\033[7m\033[34m"
	case ModeScroll:
		return "\033[7m\033[36m"
	default:
		return "\033[7m\033[36m"
	}
}

// HelpLabel returns context-sensitive help text.
func (c *Client) HelpLabel() string {
	switch c.Mode {
	case ModePassthrough:
		return c.keybindingHelp().PassthroughMode
	case ModeMenu:
		return `Ctrl+\ back | Up/Down history`
	case ModeScroll, ModePassthroughScroll:
		return "Scroll/Up/Down navigate | Esc exit scroll"
	default:
		return c.keybindingHelp().NormalMode
	}
}

// StatusLabel returns the current activity status.
func (c *Client) StatusLabel() string {
	// Use Agent's derived state when available (higher fidelity than PTY timing).
	if c.AgentState != nil {
		state, subState, dur := c.AgentState()
		var toolName string
		if c.HookState != nil && state == "active" {
			toolName = c.HookState()
		}
		label := monitor.FormatStateLabel(state, subState, toolName)
		if state == "idle" && dur != "" {
			label += " " + dur
		}
		return label
	}

	// Fallback: PTY output timing.
	const idleThreshold = 2 * time.Second
	if c.VT.LastOut.IsZero() {
		return "Active"
	}
	idleFor := time.Since(c.VT.LastOut)
	if idleFor <= idleThreshold {
		return "Active"
	}
	return "Idle " + virtualterminal.FormatIdleDuration(idleFor)
}

func (c *Client) formatWorkingDirForBar(cwd string) string {
	cleanCWD := filepath.Clean(cwd)
	h2Dir := strings.TrimSpace(os.Getenv("H2_DIR"))
	if h2Dir != "" {
		cleanH2 := filepath.Clean(h2Dir)
		if rel, ok := relToRoot(cleanCWD, cleanH2); ok {
			if rel == "." {
				return "."
			}
			return lastPathParts(rel, 2, "")
		}
	}
	return lastPathParts(cleanCWD, 2, "../")
}

func relToRoot(path, root string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	if rel == "." {
		return rel, true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

func lastPathParts(path string, n int, truncatedPrefix string) string {
	if n <= 0 {
		return ""
	}
	clean := filepath.Clean(path)
	parts := strings.Split(clean, string(filepath.Separator))
	filtered := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" && p != "." {
			filtered = append(filtered, p)
		}
	}
	if len(filtered) == 0 {
		return clean
	}
	if len(filtered) <= n {
		if filepath.IsAbs(clean) {
			return string(filepath.Separator) + strings.Join(filtered, string(filepath.Separator))
		}
		return strings.Join(filtered, string(filepath.Separator))
	}
	tail := strings.Join(filtered[len(filtered)-n:], string(filepath.Separator))
	if truncatedPrefix != "" {
		return truncatedPrefix + tail
	}
	return tail
}

// MenuLabel returns the formatted menu display.
func (c *Client) MenuLabel() string {
	var items string
	if c.IsPassthroughLocked != nil && c.IsPassthroughLocked() {
		items = "Menu | p:LOCKED | t:take over | c:clear | r:redraw"
	} else {
		items = "Menu | p:passthrough | c:clear | r:redraw"
	}
	if c.OnDetach != nil {
		items += " | d:detach"
	}
	items += " | q:quit"
	return items
}

// DebugLabel returns the debug keystroke display.
func (c *Client) DebugLabel() string {
	prefix := " debug keystrokes: "
	if len(c.DebugKeyBuf) == 0 {
		return prefix
	}
	keys := strings.Join(c.DebugKeyBuf, " ")
	available := c.VT.Cols - len(prefix)
	if available <= 0 {
		if c.VT.Cols > 0 {
			return prefix[:c.VT.Cols]
		}
		return ""
	}
	if len(keys) > available {
		keys = keys[len(keys)-available:]
	}
	return prefix + keys
}

// AppendDebugBytes records keystrokes for the debug display.
func (c *Client) AppendDebugBytes(data []byte) {
	for _, b := range data {
		c.DebugKeyBuf = append(c.DebugKeyBuf, virtualterminal.FormatDebugKey(b))
		if len(c.DebugKeyBuf) > 10 {
			c.DebugKeyBuf = c.DebugKeyBuf[len(c.DebugKeyBuf)-10:]
		}
	}
}

// exitMessage returns a human-readable description of why the child exited.
func (c *Client) exitMessage() string {
	if c.VT.ChildHung {
		return "process not responding (killed)"
	}
	if c.VT.ExitError != nil {
		var exitErr *exec.ExitError
		if errors.As(c.VT.ExitError, &exitErr) {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				return fmt.Sprintf("process killed (%s)", status.Signal())
			}
			return fmt.Sprintf("process exited (code %d)", exitErr.ExitCode())
		}
		return fmt.Sprintf("process error: %s", c.VT.ExitError)
	}
	return "process exited"
}
