package client

import (
	"strconv"
	"strings"
	"syscall"
	"time"

	"h2/internal/session/message"
	"h2/internal/session/virtualterminal"
)

const ptyWriteTimeout = 3 * time.Second
const scrollStep = 3

func (c *Client) setMode(mode InputMode) {
	c.Mode = mode
	if c.OnModeChange != nil {
		c.OnModeChange(mode)
	}
}

// writePTYOrHang writes to the child PTY with a timeout. If the write times
// out (child not reading), it marks the child as hung, kills it, and returns
// false. The caller should stop processing input when this returns false.
func (c *Client) writePTYOrHang(p []byte) bool {
	// Detect Ctrl+C (0x03) before writing so the agent can track interrupts.
	if c.OnInterrupt != nil {
		for _, b := range p {
			if b == 0x03 {
				c.OnInterrupt()
				break
			}
		}
	}
	_, err := c.VT.WritePTY(p, ptyWriteTimeout)
	if err != nil {
		c.VT.ChildHung = true
		c.VT.KillChild()
		c.RenderBar()
		return false
	}
	return true
}

// HandleExitedBytes processes input when the child has exited or is hung.
// Enter relaunches, q quits. ESC sequences are processed for mouse scroll.
func (c *Client) HandleExitedBytes(buf []byte, start, n int) int {
	for i := start; i < n; {
		b := buf[i]
		i++
		switch b {
		case '\r', '\n':
			if c.OnRelaunch != nil {
				c.OnRelaunch()
			}
			return n
		case 'q', 'Q':
			c.Quit = true
			if c.OnQuit != nil {
				c.OnQuit()
			}
			return n
		case 0x1B:
			consumed, handled := c.HandleEscape(buf[i:n])
			i += consumed
			if handled {
				continue
			}
		}
	}
	return n
}

func (c *Client) StartPendingEsc() {
	c.PendingEsc = true
	if c.EscTimer != nil {
		c.EscTimer.Stop()
	}
	c.EscTimer = time.AfterFunc(50*time.Millisecond, func() {
		c.VT.Mu.Lock()
		defer c.VT.Mu.Unlock()
		if !c.PendingEsc {
			return
		}
		c.PendingEsc = false
		switch c.Mode {
		case ModePassthrough:
			// Pass bare Escape through to the child process.
			c.PassthroughEsc = c.PassthroughEsc[:0]
			c.writePTYOrHang([]byte{0x1B})
		case ModeScroll:
			c.ExitScrollMode()
		}
	})
}

func (c *Client) CancelPendingEsc() {
	if c.EscTimer != nil {
		c.EscTimer.Stop()
	}
	c.PendingEsc = false
}

func (c *Client) HandlePassthroughBytes(buf []byte, start, n int) int {
	for i := start; i < n; {
		if c.VT.ChildExited || c.VT.ChildHung {
			return n
		}
		b := buf[i]
		if c.PendingEsc {
			if b != '[' && b != 'O' {
				// Not a CSI/SS3 introducer — pass ESC + this byte through to the child.
				c.CancelPendingEsc()
				c.PassthroughEsc = c.PassthroughEsc[:0]
				if !c.writePTYOrHang([]byte{0x1B, b}) {
					return n
				}
				i++
				continue
			}
			c.CancelPendingEsc()
			c.PassthroughEsc = append(c.PassthroughEsc[:0], 0x1B, b)
			c.FlushPassthroughEscIfComplete()
			if c.VT.ChildHung {
				return n
			}
			i++
			continue
		}
		if len(c.PassthroughEsc) > 0 {
			c.PassthroughEsc = append(c.PassthroughEsc, b)
			c.FlushPassthroughEscIfComplete()
			if c.VT.ChildHung {
				return n
			}
			i++
			continue
		}
		switch b {
		case 0x0D, 0x0A:
			c.CancelPendingEsc()
			c.PassthroughEsc = c.PassthroughEsc[:0]
			if !c.writePTYOrHang([]byte{'\r'}) {
				return n
			}
			i++
		case 0x1C: // ctrl+\ — exit passthrough (universal fallback)
			c.CancelPendingEsc()
			c.PassthroughEsc = c.PassthroughEsc[:0]
			c.setMode(ModeNormal)
			c.RenderBar()
			i++
		case 0x1B:
			c.StartPendingEsc()
			i++
		case 0x7F, 0x08:
			if !c.writePTYOrHang([]byte{b}) {
				return n
			}
			i++
		default:
			if !c.writePTYOrHang([]byte{b}) {
				return n
			}
			i++
		}
	}
	return n
}

func (c *Client) HandleMenuBytes(buf []byte, start, n int) int {
	for i := start; i < n; {
		b := buf[i]
		i++
		if b == 0x1B {
			consumed, handled := c.HandleEscape(buf[i:n])
			i += consumed
			if handled {
				continue
			}
			// Bare Esc — exit menu
			if i == n {
				c.setMode(ModeNormal)
				c.RenderBar()
			}
			continue
		}
		switch b {
		case 'p', 'P': // passthrough mode
			if c.TryPassthrough != nil && !c.TryPassthrough() {
				// Locked by another client — stay in menu.
				c.RenderBar()
				continue
			}
			c.setMode(ModePassthrough)
			c.RenderBar()
		case 't', 'T': // take over passthrough from another client
			if c.TakePassthrough != nil {
				c.TakePassthrough()
			}
			c.setMode(ModePassthrough)
			c.RenderBar()
		case 'c', 'C': // clear input
			c.Input = c.Input[:0]
			c.CursorPos = 0
			c.setMode(ModeNormal)
			c.RenderBar()
		case 'r', 'R': // redraw screen
			c.Output.Write([]byte("\033[2J\033[H"))
			c.RenderScreen()
			c.setMode(ModeNormal)
			c.RenderBar()
		case 'd', 'D': // detach
			if c.OnDetach != nil {
				c.setMode(ModeNormal)
				c.RenderBar()
				c.OnDetach()
				return n
			}
		case 'q', 'Q': // quit
			c.Quit = true
			c.VT.Cmd.Process.Signal(syscall.SIGTERM)
			if c.OnQuit != nil {
				c.OnQuit()
			}
		}
	}
	return n
}

func (c *Client) HandleDefaultBytes(buf []byte, start, n int) int {
	for i := start; i < n; {
		if c.VT.ChildExited || c.VT.ChildHung {
			return c.HandleExitedBytes(buf, i, n)
		}

		b := buf[i]
		i++

		if b == 0x1B {
			consumed, handled := c.HandleEscape(buf[i:n])
			i += consumed
			if handled {
				continue
			}
			continue
		}

		switch b {
		case 0x1C: // ctrl+\ — open menu (universal fallback)
			c.setMode(ModeMenu)
			c.RenderBar()

		case 0x09:
			c.CyclePriority()
			c.RenderBar()

		case 0x0D, 0x0A:
			if len(c.Input) > 0 {
				cmd := string(c.Input)
				if c.InputPriority == message.PriorityNormal {
					// Normal: direct PTY write.
					if !c.writePTYOrHang(c.Input) {
						return n
					}
					ptm := c.VT.Ptm
					go func() {
						time.Sleep(50 * time.Millisecond)
						ptm.Write([]byte{'\r'})
					}()
				} else if c.OnSubmit != nil {
					// Non-normal: route through session for priority-aware delivery.
					c.OnSubmit(cmd, c.InputPriority)
				}
				c.History = append(c.History, cmd)
				c.Input = c.Input[:0]
				c.CursorPos = 0
				c.InputPriority = message.PriorityNormal
			} else {
				if !c.writePTYOrHang([]byte{'\r'}) {
					return n
				}
			}
			c.HistIdx = -1
			c.Saved = nil
			c.RenderBar()

		case 0x7F, 0x08:
			if c.CursorPos > 0 {
				c.DeleteBackward()
				c.RenderBar()
			}

		case 0x01: // ctrl+a — move to start (pass through if input empty)
			if len(c.Input) > 0 {
				c.CursorToStart()
				c.RenderBar()
			} else {
				if !c.writePTYOrHang([]byte{b}) {
					return n
				}
			}

		case 0x05: // ctrl+e — move to end (pass through if input empty)
			if len(c.Input) > 0 {
				c.CursorToEnd()
				c.RenderBar()
			} else {
				if !c.writePTYOrHang([]byte{b}) {
					return n
				}
			}

		case 0x0B: // ctrl+k — kill to end of line (pass through if input empty)
			if len(c.Input) > 0 {
				c.KillToEnd()
				c.RenderBar()
			} else {
				if !c.writePTYOrHang([]byte{b}) {
					return n
				}
			}

		case 0x15: // ctrl+u — kill to start of line (pass through if input empty)
			if len(c.Input) > 0 {
				c.KillToStart()
				c.RenderBar()
			} else {
				if !c.writePTYOrHang([]byte{b}) {
					return n
				}
			}

		default:
			if b < 0x20 {
				if !c.writePTYOrHang([]byte{b}) {
					return n
				}
			} else {
				c.InsertByte(b)
				c.RenderBar()
			}
		}
	}
	return n
}

func (c *Client) FlushPassthroughEscIfComplete() bool {
	if len(c.PassthroughEsc) == 0 {
		return false
	}
	if !virtualterminal.IsEscSequenceComplete(c.PassthroughEsc) {
		return false
	}
	if virtualterminal.IsCtrlEscapeSequence(c.PassthroughEsc) {
		// Ctrl+Escape exits passthrough mode (don't write to PTY).
		c.PassthroughEsc = c.PassthroughEsc[:0]
		c.setMode(ModeNormal)
		c.RenderBar()
		return true
	}
	// Intercept SGR mouse events (ESC [ < ... M/m) so scroll works
	// in passthrough mode instead of forwarding raw sequences to the child.
	if isSGRMouseSequence(c.PassthroughEsc) {
		params := c.PassthroughEsc[2 : len(c.PassthroughEsc)-1]
		press := c.PassthroughEsc[len(c.PassthroughEsc)-1] == 'M'
		c.PassthroughEsc = c.PassthroughEsc[:0]
		c.HandleSGRMouse(params, press)
		return true
	}
	if virtualterminal.IsShiftEnterSequence(c.PassthroughEsc) {
		c.writePTYOrHang([]byte{'\n'})
	} else {
		c.writePTYOrHang(c.PassthroughEsc)
	}
	c.PassthroughEsc = c.PassthroughEsc[:0]
	return true
}

// HandleEscape processes bytes following an ESC (0x1B).
func (c *Client) HandleEscape(remaining []byte) (consumed int, handled bool) {
	if len(remaining) == 0 {
		return 0, false
	}

	switch remaining[0] {
	case '[':
		return c.HandleCSI(remaining[1:])
	case 'O':
		if len(remaining) >= 2 {
			return 2, true
		}
		return 1, true
	case 'f': // meta+f — forward word
		if c.Mode == ModeNormal && len(c.Input) > 0 {
			c.CursorForwardWord()
			c.RenderBar()
			return 1, true
		}
		return 0, false
	case 'b': // meta+b — backward word
		if c.Mode == ModeNormal && len(c.Input) > 0 {
			c.CursorBackwardWord()
			c.RenderBar()
			return 1, true
		}
		return 0, false
	}
	return 0, false
}

// HandleCSI processes a CSI sequence (after ESC [).
func (c *Client) HandleCSI(remaining []byte) (consumed int, handled bool) {
	if len(remaining) == 0 {
		return 1, true
	}

	i := 0
	for i < len(remaining) && remaining[i] >= 0x30 && remaining[i] <= 0x3F {
		i++
	}
	for i < len(remaining) && remaining[i] >= 0x20 && remaining[i] <= 0x2F {
		i++
	}
	if i >= len(remaining) {
		return 1 + i, true
	}

	final := remaining[i]
	totalConsumed := 1 + i + 1

	params := string(remaining[:i])

	switch final {
	case 'A', 'B':
		if c.Mode == ModePassthrough {
			c.writePTYOrHang(append([]byte{0x1B, '['}, remaining[:i+1]...))
			break
		}
		if c.Mode == ModeScroll {
			if final == 'A' {
				c.ScrollUp(1)
			} else {
				c.ScrollDown(1)
			}
			break
		}
		if c.Mode == ModeNormal {
			// Pass up/down arrow through to PTY (e.g. shell history).
			c.writePTYOrHang(append([]byte{0x1B, '['}, remaining[:i+1]...))
		}
	case 'C', 'D':
		if c.Mode == ModePassthrough {
			c.writePTYOrHang(append([]byte{0x1B, '['}, remaining[:i+1]...))
			break
		}
		if c.Mode == ModeNormal {
			if len(c.Input) > 0 {
				if final == 'D' {
					c.CursorLeft()
				} else {
					c.CursorRight()
				}
				c.RenderBar()
			} else {
				c.writePTYOrHang(append([]byte{0x1B, '['}, remaining[:i+1]...))
			}
		}
	case 'u':
		// Kitty keyboard protocol: CSI <code>;<modifiers> u
		if params == "13;5" {
			// Ctrl+Enter — open menu in normal mode.
			if c.Mode == ModeNormal {
				c.setMode(ModeMenu)
				c.RenderBar()
			}
		}
	case '~':
		// xterm modifyOtherKeys format: CSI 27;<modifiers>;<code> ~
		if params == "27;5;13" {
			// Ctrl+Enter — open menu in normal mode.
			if c.Mode == ModeNormal {
				c.setMode(ModeMenu)
				c.RenderBar()
			}
		}
	case 'M', 'm':
		c.HandleSGRMouse(remaining[:i], final == 'M')
	}

	return totalConsumed, true
}

// priorityOrder defines the Tab cycling order for input priorities.
var priorityOrder = []message.Priority{
	message.PriorityNormal,
	message.PriorityInterrupt,
	message.PriorityIdle,
	message.PriorityIdleFirst,
}

// CyclePriority advances InputPriority to the next value in the cycle.
func (c *Client) CyclePriority() {
	for i, p := range priorityOrder {
		if p == c.InputPriority {
			c.InputPriority = priorityOrder[(i+1)%len(priorityOrder)]
			return
		}
	}
	c.InputPriority = message.PriorityNormal
}

// HandleScrollBytes processes input when in scroll mode.
// Esc or q exits scroll mode. Arrow keys scroll. All other input is ignored.
func (c *Client) HandleScrollBytes(buf []byte, start, n int) int {
	for i := start; i < n; {
		b := buf[i]

		// Handle continuation of a pending ESC from a previous read.
		if c.PendingEsc {
			c.CancelPendingEsc()
			consumed, handled := c.HandleEscape(buf[i:n])
			if handled {
				i += consumed
				continue
			}
			// ESC followed by non-sequence byte — ignore, stay in scroll mode.
			i++
			continue
		}

		i++
		switch b {
		case 0x1B:
			if i < n {
				// More data in buffer — try to parse escape sequence.
				consumed, handled := c.HandleEscape(buf[i:n])
				i += consumed
				if handled {
					continue
				}
				// ESC followed by unrecognized byte — ignore.
			} else {
				// ESC at end of buffer — wait to see if it's bare Esc.
				c.StartPendingEsc()
			}
		default:
			// Pass control characters through to the PTY.
			if b < 0x20 && !c.VT.ChildExited && !c.VT.ChildHung {
				if !c.writePTYOrHang([]byte{b}) {
					return n
				}
			}
		}
	}
	return n
}

// EnterScrollMode switches to scroll mode, freezing the display.
func (c *Client) EnterScrollMode() {
	c.setMode(ModeScroll)
	c.ScrollOffset = 0
	c.RenderScreen()
	c.RenderBar()
}

// ExitScrollMode returns to default mode and re-renders the live view.
func (c *Client) ExitScrollMode() {
	c.ScrollOffset = 0
	c.setMode(ModeNormal)
	c.RenderScreen()
	c.RenderBar()
}

// ScrollUp moves the scroll view up by the given number of lines.
// If the offset is already at the maximum, this is a no-op to avoid re-rendering.
func (c *Client) ScrollUp(lines int) {
	prev := c.ScrollOffset
	c.ScrollOffset += lines
	c.ClampScrollOffset()
	if c.ScrollOffset == prev {
		return
	}
	c.RenderScreen()
	c.RenderBar()
}

// ScrollDown moves the scroll view down by the given number of lines.
// If we reach the bottom (offset 0), exits scroll mode.
func (c *Client) ScrollDown(lines int) {
	c.ScrollOffset -= lines
	if c.ScrollOffset <= 0 {
		c.ExitScrollMode()
		return
	}
	c.ClampScrollOffset()
	c.RenderScreen()
	c.RenderBar()
}

// ClampScrollOffset ensures ScrollOffset is within valid bounds.
func (c *Client) ClampScrollOffset() {
	if c.VT.Scrollback == nil {
		c.ScrollOffset = 0
		return
	}
	maxOffset := c.VT.Scrollback.Cursor.Y - c.VT.ChildRows + 1
	if maxOffset < 0 {
		maxOffset = 0
	}
	if c.ScrollOffset > maxOffset {
		c.ScrollOffset = maxOffset
	}
	if c.ScrollOffset < 0 {
		c.ScrollOffset = 0
	}
}

// isSGRMouseSequence returns true if seq is an SGR mouse event
// (ESC [ < Cb;Cx;Cy M/m).
func isSGRMouseSequence(seq []byte) bool {
	if len(seq) < 4 || seq[0] != 0x1B || seq[1] != '[' {
		return false
	}
	final := seq[len(seq)-1]
	return (final == 'M' || final == 'm') && seq[2] == '<'
}

// HandleSGRMouse processes an SGR mouse event. The params bytes contain
// the "<Cb;Cx;Cy" portion (everything between ESC[ and the final M/m).
// press is true for button press (M), false for release (m).
// Button 0 = left click, 64 = scroll up, 65 = scroll down.
func (c *Client) HandleSGRMouse(params []byte, press bool) {
	// SGR mouse format: ESC [ < Cb ; Cx ; Cy M/m
	// params should start with '<' followed by Cb;Cx;Cy
	s := string(params)
	if !strings.HasPrefix(s, "<") {
		return
	}
	s = s[1:] // strip leading '<'
	parts := strings.Split(s, ";")
	if len(parts) < 3 {
		return
	}
	button, err := strconv.Atoi(parts[0])
	if err != nil {
		return
	}

	switch button {
	case 0: // left click
		if press {
			c.ShowSelectHint()
		}
	case 64: // scroll up
		if c.Mode != ModeScroll {
			c.EnterScrollMode()
		}
		c.ScrollUp(scrollStep)
	case 65: // scroll down
		if c.Mode == ModeScroll {
			c.ScrollDown(scrollStep)
		}
	}
}

// ShowSelectHint displays a transient hint about using shift for text selection.
func (c *Client) ShowSelectHint() {
	c.SelectHint = true
	if c.SelectHintTimer != nil {
		c.SelectHintTimer.Stop()
	}
	c.RenderScreen()
	c.RenderBar()
	c.SelectHintTimer = time.AfterFunc(3*time.Second, func() {
		c.VT.Mu.Lock()
		defer c.VT.Mu.Unlock()
		c.SelectHint = false
		c.RenderScreen()
		c.RenderBar()
	})
}
