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
// Wrapped in synchronized output mode (DEC private mode 2026) so the
// terminal buffers all intermediate cursor movements and applies the
// final state atomically. This prevents screen tearing and avoids
// resetting the cursor blink timer on every PipeOutput chunk.
// DECSC/DECRC save and restore cursor position so the cursor stays on
// the input bar (positioned by RenderInputBar).
func (c *Client) RenderScreen() {
	var buf bytes.Buffer
	buf.WriteString("\033[?2026h") // begin synchronized update
	buf.WriteString("\0337")       // DECSC: save cursor position
	if c.Mode == ModeAgentNav {
		c.renderAgentNavView(&buf)
	} else if c.IsScrollMode() {
		c.renderScrollView(&buf)
	} else {
		c.renderLiveView(&buf)
	}
	c.renderSelectHint(&buf)
	buf.WriteString("\0338")       // DECRC: restore cursor position
	buf.WriteString("\033[?2026l") // end synchronized update
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
	rows := collectVTRows(c.VT.Vt, startRow, c.VT.ChildRows)
	spans := detectURLSpans(rows, c.VT.Cols)
	for i := 0; i < c.VT.ChildRows; i++ {
		fmt.Fprintf(buf, "\033[%d;1H", i+1)
		c.RenderLineFrom(buf, c.VT.Vt, startRow+i, spans[i])
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
	rows := collectVTRows(sb, startRow, c.VT.ChildRows)
	spans := detectURLSpans(rows, c.VT.Cols)
	for i := 0; i < c.VT.ChildRows; i++ {
		fmt.Fprintf(buf, "\033[%d;1H", i+1)
		row := startRow + i
		if row >= 0 && row < len(sb.Content) {
			c.RenderLineFrom(buf, sb, row, spans[i])
		}
		buf.WriteString("\033[0m\033[K")
	}
	c.renderScrollIndicator(buf)
}

// navRow is one display row of the agent navigator: either a section header
// or a selectable entry (an index into NavEntries).
type navRow struct {
	header   string
	entryIdx int // -1 for header rows
}

// buildNavRows converts NavEntries into display rows, inserting section
// headers mirroring `h2 list`: one per pod, one for podless live agents, and
// one for stopped sessions. Flat lists (no pods, nothing stopped) get no
// headers at all.
func (c *Client) buildNavRows() []navRow {
	hasPods := false
	hasStopped := false
	for _, e := range c.NavEntries {
		if e.Pod != "" && !e.Stopped {
			hasPods = true
		}
		if e.Stopped {
			hasStopped = true
		}
	}

	var rows []navRow
	prevHeader := ""
	for i, e := range c.NavEntries {
		var header string
		switch {
		case e.Stopped:
			if hasStopped {
				header = "Stopped"
			}
		case e.Pod != "":
			header = "pod: " + e.Pod
		default:
			if hasPods {
				header = "no pod"
			}
		}
		if header != "" && header != prevHeader {
			rows = append(rows, navRow{header: header, entryIdx: -1})
		}
		prevHeader = header
		rows = append(rows, navRow{entryIdx: i})
	}
	return rows
}

// renderAgentNavView renders the agent navigator: a full-screen list of
// agents (live first, grouped by pod; stopped sessions last) with the
// current selection highlighted. Long lists scroll to keep the selection
// visible.
func (c *Client) renderAgentNavView(buf *bytes.Buffer) {
	rows := c.VT.ChildRows
	if rows < 1 {
		return
	}
	title := " Agents — Up/Down move · Enter switch/resume · r refresh · Esc back "
	if len(title) > c.VT.Cols && c.VT.Cols > 0 {
		title = title[:c.VT.Cols]
	}
	fmt.Fprintf(buf, "\033[1;1H\033[1m%s\033[0m\033[K", title)

	listRows := rows - 1
	line := 2
	writeLine := func(s string) {
		if line > rows {
			return
		}
		fmt.Fprintf(buf, "\033[%d;1H%s\033[0m\033[K", line, s)
		line++
	}

	switch {
	case c.NavLoading:
		writeLine("  Loading agents...")
	case len(c.NavEntries) == 0:
		writeLine("  No agents found.")
	default:
		navRows := c.buildNavRows()

		// Window the display rows so the selected entry stays visible.
		selRow := 0
		for i, r := range navRows {
			if r.entryIdx == c.NavSelected {
				selRow = i
				break
			}
		}
		first := 0
		if listRows > 0 && selRow >= listRows {
			first = selRow - listRows + 1
		}

		nameWidth := 0
		for _, e := range c.NavEntries {
			if len(e.Name) > nameWidth {
				nameWidth = len(e.Name)
			}
		}
		if nameWidth > 32 {
			nameWidth = 32
		}
		for i := first; i < len(navRows) && line <= rows; i++ {
			r := navRows[i]
			if r.entryIdx == -1 {
				writeLine("\033[1m" + r.header)
				continue
			}
			writeLine(c.formatNavRow(c.NavEntries[r.entryIdx], r.entryIdx == c.NavSelected, nameWidth))
		}
	}
	// Blank out any remaining rows so stale terminal content doesn't show.
	for line <= rows {
		writeLine("")
	}
}

// formatNavRow formats one agent navigator row, truncated to the terminal width.
func (c *Client) formatNavRow(e AgentNavEntry, selected bool, nameWidth int) string {
	name := e.Name
	if len(name) > nameWidth {
		name = name[:nameWidth]
	}

	state := e.StateDisplay
	if e.StateDuration != "" {
		state += " " + e.StateDuration
	}

	var extras strings.Builder
	if e.IsSelf {
		extras.WriteString(" (this)")
	}
	if e.Role != "" {
		fmt.Fprintf(&extras, " (%s)", e.Role)
	}
	// Live entries with a pod sit under a pod header; only stopped entries
	// need the pod spelled out on the row.
	if e.Pod != "" && e.Stopped {
		fmt.Fprintf(&extras, " [pod: %s]", e.Pod)
	}
	if e.Command != "" {
		extras.WriteString(" " + e.Command)
	}

	marker := "  "
	if selected {
		marker = "> "
	}
	text := fmt.Sprintf("%s%-*s  %-18s%s", marker, nameWidth, name, state, extras.String())
	if len(text) > c.VT.Cols && c.VT.Cols > 0 {
		text = text[:c.VT.Cols]
	}
	if selected {
		return "\033[7m" + text
	}

	// Color the state dot-equivalent by coloring the state text.
	stateColor := ""
	switch e.State {
	case "active":
		stateColor = "\033[32m"
	case "idle":
		stateColor = "\033[33m"
	case "exited":
		stateColor = "\033[31m"
	case "stopped":
		stateColor = "\033[90m"
	}
	if stateColor == "" || state == "" {
		return text
	}
	// Re-render with the state segment colored (positions match the plain
	// version because escape codes add no visible width, but truncation above
	// must happen on the plain text first — so splice the color in afterwards.
	idx := strings.Index(text, state)
	if idx < 0 {
		return text
	}
	end := idx + len(state)
	return text[:idx] + stateColor + text[idx:end] + "\033[0m" + text[end:]
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

	// Build per-row rune content for URL detection across the entire visible
	// window, regardless of whether each row came from history or live VT.
	// This lets a URL detected on a history row continue into a live row.
	rows := make([][]rune, c.VT.ChildRows)
	for i := 0; i < c.VT.ChildRows; i++ {
		row := startRow + i
		if row < 0 || row >= totalRows {
			continue
		}
		if row < histLen {
			rows[i] = c.VT.ScrollHistory[row].Content
		} else {
			vtRow := row - histLen
			if vtRow < len(c.VT.Vt.Content) {
				rows[i] = c.VT.Vt.Content[vtRow]
			}
		}
	}
	spans := detectURLSpans(rows, c.VT.Cols)

	for i := 0; i < c.VT.ChildRows; i++ {
		fmt.Fprintf(buf, "\033[%d;1H", i+1)
		row := startRow + i
		if row >= 0 && row < totalRows {
			if row < histLen {
				c.renderHistoryEntry(buf, c.VT.ScrollHistory[row], spans[i])
			} else {
				vtRow := row - histLen
				c.RenderLineFrom(buf, c.VT.Vt, vtRow, spans[i])
			}
		}
		buf.WriteString("\033[0m\033[K")
	}
	c.renderScrollIndicator(buf)
}

// collectVTRows returns count rows of content from a midterm terminal starting
// at startRow. Out-of-range rows yield nil entries, which detectURLSpans
// treats as empty.
func collectVTRows(vt *midterm.Terminal, startRow, count int) [][]rune {
	rows := make([][]rune, count)
	for i := 0; i < count; i++ {
		r := startRow + i
		if r >= 0 && r < len(vt.Content) {
			rows[i] = vt.Content[r]
		}
	}
	return rows
}

// renderHistoryEntry emits one ScrollHistoryEntry to buf, sized to the current
// terminal width. Entries captured at a wider width are truncated; entries
// captured at a narrower width render short (the surrounding \033[0m\033[K
// erases the tail). This is the width-adaptation that the old "pre-rendered
// ANSI string" representation couldn't do — resizes no longer produce wrap
// collisions or stale-width artifacts in scrollback.
func (c *Client) renderHistoryEntry(buf *bytes.Buffer, entry virtualterminal.ScrollHistoryEntry, autoSpans []urlSpan) {
	cols := c.VT.Cols
	if cols <= 0 {
		return
	}
	n := cols
	if n > len(entry.Content) {
		n = len(entry.Content)
	}
	if n == 0 {
		buf.WriteString("\033[0m")
		return
	}

	formats := make([]midterm.Format, n)
	urls := make([]string, n)
	pos := 0
	for _, run := range entry.Runs {
		if pos >= n {
			break
		}
		end := pos + run.Size
		if end > n {
			end = n
		}
		url := sanitizeOSC8URL(run.URL)
		for i := pos; i < end; i++ {
			formats[i] = run.Format
			urls[i] = url
		}
		pos = end
	}
	overlayAutoSpans(urls, autoSpans, n)

	var lastFormat midterm.Format
	var lastURL string
	hover := c.HoveredURL
	for i := 0; i < n; i++ {
		f := formats[i]
		if hover != "" && urls[i] == hover {
			f.SetUnderline(true)
		}
		if f != lastFormat {
			buf.WriteString("\033[0m")
			buf.WriteString(f.Render())
			lastFormat = f
		}
		if urls[i] != lastURL {
			writeOSC8BoundaryStr(buf, lastURL, urls[i])
			lastURL = urls[i]
		}
		buf.WriteRune(entry.Content[i])
	}
	if lastURL != "" {
		buf.WriteString("\033]8;;\033\\")
	}
	buf.WriteString("\033[0m")
}

// writeOSC8BoundaryStr emits the OSC 8 close/open transitions between two
// URLs. An explicit close precedes any open — OSC 8 has no stack and a
// "new open inside an old open" can confuse terminals, so we always reset.
// next is sanitized against terminator-equivalent control bytes (ESC, BEL,
// C0/DEL) before emission; an unsafe input drops the link entirely rather
// than risk re-injecting attacker bytes into the outer terminal.
func writeOSC8BoundaryStr(buf *bytes.Buffer, prev, next string) {
	if prev != "" {
		buf.WriteString("\033]8;;\033\\")
	}
	if next == "" {
		return
	}
	safe := sanitizeOSC8URL(next)
	if safe == "" {
		return
	}
	buf.WriteString("\033]8;;")
	buf.WriteString(safe)
	buf.WriteString("\033\\")
}

// sanitizeOSC8URL rejects URLs containing any byte that could break out of an
// OSC 8 sequence on the outer terminal: ESC (0x1B) is the ST half, BEL (0x07)
// is the alternate terminator, and other C0 controls / DEL can lead to
// terminal state corruption. The xterm OSC 8 spec restricts URIs to printable
// ASCII anyway. Returns "" to signal "drop this link" — callers treat that
// as no-link rather than attempting partial recovery.
func sanitizeOSC8URL(s string) string {
	if s == "" {
		return ""
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		// Reject anything below 0x20 (C0 controls including ESC, BEL, NUL,
		// CR, LF, etc.) and 0x7F (DEL). Bytes >= 0x80 are UTF-8 continuation
		// bytes; most terminals accept UTF-8 in OSC parameters.
		if b < 0x20 || b == 0x7F {
			return ""
		}
	}
	return s
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

// RenderLineFrom writes one row of the given terminal to buf, overlaying any
// auto-detected URL spans on cells that don't already carry an explicit OSC 8
// link from the inner program. Cells with an explicit URL win — a "click
// here" → URL link shouldn't get its URL replaced by an auto-detection of
// some other URL on the same row.
//
// Uses explicit SGR resets between format regions (midterm's RenderLine does
// not reset between regions, which lets background colors bleed). OSC 8 is
// opened on entry to a URL run and closed on exit; each row stands alone, so
// a multi-row link reopens at the start of the next row.
func (c *Client) RenderLineFrom(buf *bytes.Buffer, vt *midterm.Terminal, row int, autoSpans []urlSpan) {
	if row >= len(vt.Content) {
		return
	}
	line := vt.Content[row]
	cols := len(line)
	if cols == 0 {
		buf.WriteString("\033[0m")
		return
	}

	formats := make([]midterm.Format, cols)
	urls := make([]string, cols)
	pos := 0
	for region := range vt.Format.Regions(row) {
		end := pos + region.Size
		if end > cols {
			end = cols
		}
		var url string
		if region.URLID != 0 {
			url = sanitizeOSC8URL(vt.URL(region.URLID))
		}
		for i := pos; i < end; i++ {
			formats[i] = region.F
			urls[i] = url
		}
		pos = end
	}
	overlayAutoSpans(urls, autoSpans, cols)

	var lastFormat midterm.Format
	var lastURL string
	hover := c.HoveredURL
	for i := 0; i < cols; i++ {
		f := formats[i]
		if hover != "" && urls[i] == hover {
			f.SetUnderline(true)
		}
		if f != lastFormat {
			buf.WriteString("\033[0m")
			buf.WriteString(f.Render())
			lastFormat = f
		}
		if urls[i] != lastURL {
			writeOSC8BoundaryStr(buf, lastURL, urls[i])
			lastURL = urls[i]
		}
		buf.WriteRune(line[i])
	}
	if lastURL != "" {
		buf.WriteString("\033]8;;\033\\")
	}
	buf.WriteString("\033[0m")
}

// overlayAutoSpans writes auto-detected URL strings into the per-cell urls
// slice, but only for cells that don't already carry an explicit URL from the
// inner program. The producer's OSC 8 always wins.
func overlayAutoSpans(urls []string, spans []urlSpan, cols int) {
	for _, span := range spans {
		s, e := span.startCol, span.endCol
		if s < 0 {
			s = 0
		}
		if e > cols {
			e = cols
		}
		url := sanitizeOSC8URL(span.url)
		if url == "" {
			continue
		}
		for i := s; i < e; i++ {
			if urls[i] == "" {
				urls[i] = url
			}
		}
	}
}

// RenderLine writes one row of the primary virtual terminal to buf without
// auto-detected URL spans. Callers that need URL detection should call
// detectURLSpans for the window and use RenderLineFrom directly.
func (c *Client) RenderLine(buf *bytes.Buffer, row int) {
	c.RenderLineFrom(buf, c.VT.Vt, row, nil)
}

// RenderStatusBar draws the separator line with mode, status, and help text.
// Uses DECSC/DECRC to preserve cursor position so the 1-second TickStatus
// doesn't move the cursor away from the input bar.
func (c *Client) RenderStatusBar() {
	var buf bytes.Buffer
	buf.WriteString("\033[?2026h") // begin synchronized update
	buf.WriteString("\0337")       // DECSC: save cursor position

	sepRow := c.VT.Rows - 1
	if c.DebugKeys {
		sepRow = c.VT.Rows - 2
	}

	// --- Separator line ---
	fmt.Fprintf(&buf, "\033[%d;1H\033[2K", sepRow)

	var style, label, right string
	if c.VT.ChildExited {
		style = "\033[7m\033[31m" // red inverse
		if c.IsScrollMode() {
			label = " Scroll | " + c.exitMessage() + " | Esc exit"
		} else {
			label = " " + c.exitMessage() + " | [Enter] relaunch \u00b7 [q] quit"
		}
		if c.AgentName != "" {
			right = c.AgentName + " "
		}
		if len(label)+len(right) > c.VT.Cols {
			right = ""
			if len(label) > c.VT.Cols {
				label = label[:c.VT.Cols]
			}
		}
	} else {
		style = c.ModeBarStyle()
		label, right = c.fitStatusBarSections()
	}

	buf.WriteString(style)
	buf.WriteString(label)
	gap := c.VT.Cols - len(label) - len(right)
	if gap > 0 {
		buf.WriteString(strings.Repeat(" ", gap))
	}
	buf.WriteString(right)
	buf.WriteString("\033[0m")
	buf.WriteString("\0338")       // DECRC: restore cursor position
	buf.WriteString("\033[?2026l") // end synchronized update

	c.OutputMu.Lock()
	c.Output.Write(buf.Bytes())
	c.OutputMu.Unlock()
}

// fitStatusBarSections assembles the left status-bar label and the
// right-aligned agent name, dropping sections one at a time when the bar
// is too narrow. Drop order: tokens, help, mode, agent name, working dir.
// The activity status is kept until nothing else fits, then hard-truncated
// as a last resort.
func (c *Client) fitStatusBarSections() (label, right string) {
	if c.AgentName != "" {
		right = c.AgentName + " "
	}

	mode := c.ModeStatusLabel()
	var status, wd, tokens string
	if c.Mode != ModeMenu {
		status = c.StatusLabel()
		if c.FlashText != "" {
			status = c.FlashText
		}
		if c.WorkingDir != nil {
			if w := strings.TrimSpace(c.WorkingDir()); w != "" {
				wd = c.formatWorkingDirForBar(w)
			}
		}
		// OTEL metrics (tokens and cost)
		if c.OtelMetrics != nil {
			inTok, outTok, cost, connected, port := c.OtelMetrics()
			if connected {
				tokens = monitor.FormatTokens(inTok) + "/" + monitor.FormatTokens(outTok) + " " + monitor.FormatCost(cost)
			} else {
				tokens = fmt.Sprintf("[otel:%d]", port)
			}
		}
	}
	help := c.HelpLabel()

	join := func() string {
		var b strings.Builder
		for _, part := range []string{mode, status, wd, tokens, help} {
			if part == "" {
				continue
			}
			if b.Len() == 0 {
				b.WriteString(" ")
			} else {
				b.WriteString(" | ")
			}
			b.WriteString(part)
		}
		return b.String()
	}

	drops := []*string{&tokens, &help, &mode, &right, &wd}
	if c.Mode == ModeMenu {
		// The menu items are the whole bar — keep them and drop help,
		// then the agent name.
		drops = []*string{&help, &right}
	}
	label = join()
	for _, drop := range drops {
		if len(label)+len(right) <= c.VT.Cols {
			return label, right
		}
		*drop = ""
		label = join()
	}
	if len(label) > c.VT.Cols {
		label = label[:c.VT.Cols]
	}
	return label, right
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
	prompt := c.InputPromptLabel() + " > "
	maxInput := c.VT.Cols - len(prompt)
	if maxInput < 0 {
		maxInput = 0
	}

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
	} else if c.InputAction == InputActionStash {
		promptColor = "\033[35m" // purple
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

func (c *Client) InputPromptLabel() string {
	if c.InputAction == InputActionStash {
		return "stash"
	}
	return c.InputPriority.String()
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
	case ModeAgentNav:
		return "Agents"
	case ModeScroll:
		return "Scroll"
	case ModePassthroughScroll:
		return "Scroll (PT)"
	default:
		return "Normal"
	}
}

// ModeStatusLabel returns the current mode label plus steer/idle backlog.
func (c *Client) ModeStatusLabel() string {
	label := c.ModeLabel()
	if c.QueueStatus == nil || c.Mode == ModeMenu {
		return label
	}
	backlog := c.QueueStatus().SteerAndIdleBacklog()
	if backlog > 0 {
		return fmt.Sprintf("%s [%d]", label, backlog)
	}
	return label
}

// ModeBarStyle returns the ANSI style for the current mode.
func (c *Client) ModeBarStyle() string {
	switch c.Mode {
	case ModePassthrough, ModePassthroughScroll:
		return "\033[7m\033[33m"
	case ModeMenu, ModeAgentNav:
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
	case ModeAgentNav:
		return "Up/Down move | Enter switch | Esc back"
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
	if c.OnForkSession != nil {
		items += " | f:fork"
	}
	if c.OnRequestAgentList != nil {
		items += " | a:agents"
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
