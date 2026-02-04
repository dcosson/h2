package overlay

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/vito/midterm"

	"h2/internal/virtualterminal"
)

// RenderScreen renders the virtual terminal buffer to the output.
func (o *Overlay) RenderScreen() {
	var buf bytes.Buffer
	buf.WriteString("\033[?25l")
	if o.Mode == ModeScroll {
		o.renderScrollView(&buf)
	} else {
		o.renderLiveView(&buf)
	}
	o.VT.Output.Write(buf.Bytes())
}

// renderLiveView renders the live terminal content.
// The primary terminal has a fixed height and handles its own scrolling
// internally, so we always render rows 0 through ChildRows-1.
func (o *Overlay) renderLiveView(buf *bytes.Buffer) {
	for row := 0; row < o.VT.ChildRows; row++ {
		fmt.Fprintf(buf, "\033[%d;1H\033[2K", row+1)
		o.RenderLineFrom(buf, o.VT.Vt, row)
	}
}

// renderScrollView renders the scrollback buffer at the current ScrollOffset.
func (o *Overlay) renderScrollView(buf *bytes.Buffer) {
	sb := o.VT.Scrollback
	if sb == nil {
		o.renderLiveView(buf)
		return
	}
	bottom := sb.Cursor.Y
	startRow := bottom - o.VT.ChildRows + 1 - o.ScrollOffset
	if startRow < 0 {
		startRow = 0
	}
	for i := 0; i < o.VT.ChildRows; i++ {
		fmt.Fprintf(buf, "\033[%d;1H\033[2K", i+1)
		o.RenderLineFrom(buf, sb, startRow+i)
	}
	// Draw "(scrolling)" indicator at row 1, right-aligned, in inverse video.
	indicator := "(scrolling)"
	col := o.VT.Cols - len(indicator) + 1
	if col < 1 {
		col = 1
	}
	fmt.Fprintf(buf, "\033[1;%dH\033[7m%s\033[0m", col, indicator)
}

// RenderLineFrom writes one row of the given terminal to buf.
func (o *Overlay) RenderLineFrom(buf *bytes.Buffer, vt *midterm.Terminal, row int) {
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

		padStart := len(line)
		if padStart < pos {
			padStart = pos
		}
		if padStart < end {
			buf.WriteString(strings.Repeat(" ", end-padStart))
		}

		pos = end
	}
	buf.WriteString("\033[0m")
}

// RenderLine writes one row of the primary virtual terminal to buf.
func (o *Overlay) RenderLine(buf *bytes.Buffer, row int) {
	o.RenderLineFrom(buf, o.VT.Vt, row)
}

// RenderBar draws the separator line and input bar.
func (o *Overlay) RenderBar() {
	var buf bytes.Buffer

	sepRow := o.VT.Rows - 1
	inputRow := o.VT.Rows
	debugRow := 0
	if o.DebugKeys {
		sepRow = o.VT.Rows - 2
		inputRow = o.VT.Rows - 1
		debugRow = o.VT.Rows
	}

	// --- Separator line ---
	fmt.Fprintf(&buf, "\033[%d;1H\033[2K", sepRow)

	var style, label string
	if o.ChildExited {
		style = "\033[7m\033[31m" // red inverse
		label = " " + o.exitMessage() + " | [Enter] relaunch \u00b7 [q] quit"
	} else {
		style = o.ModeBarStyle()
		help := o.HelpLabel()
		status := o.StatusLabel()
		label = " " + o.ModeLabel() + " | " + status

		// Queue indicator
		if o.QueueStatus != nil {
			count, paused := o.QueueStatus()
			if count > 0 {
				if paused {
					label += fmt.Sprintf(" | [%d paused]", count)
				} else {
					label += fmt.Sprintf(" | [%d queued]", count)
				}
			}
		}

		if help != "" {
			label += " | " + help
		}
	}

	right := ""
	if o.AgentName != "" {
		right = o.AgentName + " "
	}

	if len(label)+len(right) > o.VT.Cols {
		if !o.ChildExited {
			// Tight on space - drop help first, then right-align.
			status := o.StatusLabel()
			label = " " + o.ModeLabel() + " | " + status
		}
		if len(label)+len(right) > o.VT.Cols {
			if len(label) > o.VT.Cols {
				label = label[:o.VT.Cols]
			}
			right = ""
		}
	}

	buf.WriteString(style)
	buf.WriteString(label)
	gap := o.VT.Cols - len(label) - len(right)
	if gap > 0 {
		buf.WriteString(strings.Repeat(" ", gap))
	}
	buf.WriteString(right)
	buf.WriteString("\033[0m")

	// --- Input line ---
	prompt := "> "
	inputStr := string(o.Input)
	maxInput := o.VT.Cols - len(prompt)

	displayInput := inputStr
	runeCount := utf8.RuneCountInString(displayInput)
	if runeCount > maxInput && maxInput > 0 {
		runes := []rune(displayInput)
		displayInput = string(runes[len(runes)-maxInput:])
		runeCount = maxInput
	}
	fmt.Fprintf(&buf, "\033[%d;1H\033[2K", inputRow)
	fmt.Fprintf(&buf, "\033[36m%s\033[0m%s", prompt, displayInput)

	cursorCol := len(prompt) + runeCount + 1
	if cursorCol > o.VT.Cols {
		cursorCol = o.VT.Cols
	}
	fmt.Fprintf(&buf, "\033[%d;%dH", inputRow, cursorCol)

	if o.DebugKeys {
		fmt.Fprintf(&buf, "\033[%d;1H\033[2K", debugRow)
		debugLabel := o.DebugLabel()
		if len(debugLabel) > o.VT.Cols {
			debugLabel = virtualterminal.TrimLeftToWidth(debugLabel, o.VT.Cols)
		}
		buf.WriteString(debugLabel)
		if pad := o.VT.Cols - len(debugLabel); pad > 0 {
			buf.WriteString(strings.Repeat(" ", pad))
		}
	}

	buf.WriteString("\033[?25h")
	o.VT.Output.Write(buf.Bytes())
}

// ModeLabel returns the display name for the current mode.
func (o *Overlay) ModeLabel() string {
	switch o.Mode {
	case ModePassthrough:
		return "Passthrough"
	case ModeMenu:
		return o.MenuLabel()
	case ModeScroll:
		return "Scroll"
	default:
		return "Default"
	}
}

// ModeBarStyle returns the ANSI style for the current mode.
func (o *Overlay) ModeBarStyle() string {
	switch o.Mode {
	case ModePassthrough:
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
func (o *Overlay) HelpLabel() string {
	switch o.Mode {
	case ModePassthrough:
		return "Enter/Esc exit"
	case ModeMenu:
		return "Left/Right move | Enter select | Esc exit"
	case ModeScroll:
		return "Scroll/Up/Down navigate | Esc exit"
	default:
		return "Up/Down history | Enter send | / passthrough | // menu"
	}
}

// StatusLabel returns the current activity status.
func (o *Overlay) StatusLabel() string {
	const idleThreshold = 2 * time.Second
	if o.VT.LastOut.IsZero() {
		return "Active"
	}
	idleFor := time.Since(o.VT.LastOut)
	if idleFor <= idleThreshold {
		return "Active"
	}
	return "Idle " + virtualterminal.FormatIdleDuration(idleFor)
}

// MenuLabel returns the formatted menu display.
func (o *Overlay) MenuLabel() string {
	var parts []string
	for i, item := range MenuItems {
		if i == o.MenuIdx {
			parts = append(parts, ">"+item)
		} else {
			parts = append(parts, item)
		}
	}
	return "Menu: " + strings.Join(parts, " | ")
}

// DebugLabel returns the debug keystroke display.
func (o *Overlay) DebugLabel() string {
	prefix := " debug keystrokes: "
	if len(o.DebugKeyBuf) == 0 {
		return prefix
	}
	keys := strings.Join(o.DebugKeyBuf, " ")
	available := o.VT.Cols - len(prefix)
	if available <= 0 {
		if o.VT.Cols > 0 {
			return prefix[:o.VT.Cols]
		}
		return ""
	}
	if len(keys) > available {
		keys = keys[len(keys)-available:]
	}
	return prefix + keys
}

// AppendDebugBytes records keystrokes for the debug display.
func (o *Overlay) AppendDebugBytes(data []byte) {
	for _, b := range data {
		o.DebugKeyBuf = append(o.DebugKeyBuf, virtualterminal.FormatDebugKey(b))
		if len(o.DebugKeyBuf) > 10 {
			o.DebugKeyBuf = o.DebugKeyBuf[len(o.DebugKeyBuf)-10:]
		}
	}
}

// exitMessage returns a human-readable description of why the child exited.
func (o *Overlay) exitMessage() string {
	if o.ChildHung {
		return "process not responding (killed)"
	}
	if o.ExitError != nil {
		var exitErr *exec.ExitError
		if errors.As(o.ExitError, &exitErr) {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				return fmt.Sprintf("process killed (%s)", status.Signal())
			}
			return fmt.Sprintf("process exited (code %d)", exitErr.ExitCode())
		}
		return fmt.Sprintf("process error: %s", o.ExitError)
	}
	return "process exited"
}
