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

func (o *Overlay) setMode(mode InputMode) {
	o.Mode = mode
	if o.OnModeChange != nil {
		o.OnModeChange(mode)
	}
}

// writePTYOrHang writes to the child PTY with a timeout. If the write times
// out (child not reading), it marks the child as hung, kills it, and returns
// false. The caller should stop processing input when this returns false.
func (o *Overlay) writePTYOrHang(p []byte) bool {
	_, err := o.VT.WritePTY(p, ptyWriteTimeout)
	if err != nil {
		o.ChildHung = true
		o.KillChild()
		o.RenderBar()
		return false
	}
	return true
}

// HandleExitedBytes processes input when the child has exited or is hung.
// Enter relaunches, q quits. ESC sequences are processed for mouse scroll.
func (o *Overlay) HandleExitedBytes(buf []byte, start, n int) int {
	for i := start; i < n; {
		b := buf[i]
		i++
		switch b {
		case '\r', '\n':
			select {
			case o.relaunchCh <- struct{}{}:
			default:
			}
			return n
		case 'q', 'Q':
			select {
			case o.quitCh <- struct{}{}:
			default:
			}
			o.Quit = true
			return n
		case 0x1B:
			consumed, handled := o.HandleEscape(buf[i:n])
			i += consumed
			if handled {
				continue
			}
		}
	}
	return n
}

func (o *Overlay) StartPendingEsc() {
	o.PendingEsc = true
	if o.EscTimer != nil {
		o.EscTimer.Stop()
	}
	o.EscTimer = time.AfterFunc(50*time.Millisecond, func() {
		o.VT.Mu.Lock()
		defer o.VT.Mu.Unlock()
		if !o.PendingEsc {
			return
		}
		o.PendingEsc = false
		switch o.Mode {
		case ModePassthrough:
			o.PassthroughEsc = o.PassthroughEsc[:0]
			o.setMode(ModeDefault)
			o.RenderBar()
		case ModeScroll:
			o.ExitScrollMode()
		}
	})
}

func (o *Overlay) CancelPendingEsc() {
	if o.EscTimer != nil {
		o.EscTimer.Stop()
	}
	o.PendingEsc = false
}

func (o *Overlay) HandlePassthroughBytes(buf []byte, start, n int) int {
	for i := start; i < n; {
		if o.ChildExited || o.ChildHung {
			return n
		}
		b := buf[i]
		if o.PendingEsc {
			if b != '[' && b != 'O' {
				o.CancelPendingEsc()
				o.PassthroughEsc = o.PassthroughEsc[:0]
				o.setMode(ModeDefault)
				o.RenderBar()
				return i
			}
			o.CancelPendingEsc()
			o.PassthroughEsc = append(o.PassthroughEsc[:0], 0x1B, b)
			o.FlushPassthroughEscIfComplete()
			if o.ChildHung {
				return n
			}
			i++
			continue
		}
		if len(o.PassthroughEsc) > 0 {
			o.PassthroughEsc = append(o.PassthroughEsc, b)
			o.FlushPassthroughEscIfComplete()
			if o.ChildHung {
				return n
			}
			i++
			continue
		}
		switch b {
		case 0x0D, 0x0A:
			o.CancelPendingEsc()
			o.PassthroughEsc = o.PassthroughEsc[:0]
			if !o.writePTYOrHang([]byte{'\r'}) {
				return n
			}
			o.setMode(ModeDefault)
			o.RenderBar()
			i++
		case 0x1B:
			o.StartPendingEsc()
			i++
		case 0x7F, 0x08:
			if !o.writePTYOrHang([]byte{b}) {
				return n
			}
			i++
		default:
			if !o.writePTYOrHang([]byte{b}) {
				return n
			}
			i++
		}
	}
	return n
}

func (o *Overlay) HandleMenuBytes(buf []byte, start, n int) int {
	for i := start; i < n; {
		b := buf[i]
		i++
		if b == 0x1B {
			consumed, handled := o.HandleEscape(buf[i:n])
			i += consumed
			if handled {
				continue
			}
			// Bare Esc — exit menu
			if i == n {
				o.setMode(ModeDefault)
				o.RenderBar()
			}
			continue
		}
		switch b {
		case 0x0D, 0x0A: // Enter — passthrough mode
			o.setMode(ModePassthrough)
			o.RenderBar()
		case 'c', 'C': // clear input
			o.Input = o.Input[:0]
			o.CursorPos = 0
			o.setMode(ModeDefault)
			o.RenderBar()
		case 'r', 'R': // redraw screen
			o.VT.Output.Write([]byte("\033[2J\033[H"))
			o.RenderScreen()
			o.setMode(ModeDefault)
			o.RenderBar()
		case 'd', 'D': // detach
			if o.OnDetach != nil {
				o.setMode(ModeDefault)
				o.RenderBar()
				o.OnDetach()
				return n
			}
		case 'q', 'Q': // quit
			o.Quit = true
			o.VT.Cmd.Process.Signal(syscall.SIGTERM)
		}
	}
	return n
}

func (o *Overlay) HandleDefaultBytes(buf []byte, start, n int) int {
	for i := start; i < n; {
		if o.ChildExited || o.ChildHung {
			return o.HandleExitedBytes(buf, i, n)
		}

		b := buf[i]
		i++

		if b == 0x1B {
			consumed, handled := o.HandleEscape(buf[i:n])
			i += consumed
			if handled {
				continue
			}
			continue
		}

		switch b {
		case 0x02: // ctrl+b — open menu
			o.setMode(ModeMenu)
			o.RenderBar()

		case 0x10: // ctrl+p — history up
			o.HistoryUp()
			o.RenderBar()

		case 0x0E: // ctrl+n — history down
			o.HistoryDown()
			o.RenderBar()

		case 0x09:
			o.CyclePriority()
			o.RenderBar()

		case 0x0D, 0x0A:
			if len(o.Input) > 0 {
				cmd := string(o.Input)
				if o.InputPriority == message.PriorityNormal {
					// Normal: direct PTY write.
					if !o.writePTYOrHang(o.Input) {
						return n
					}
					ptm := o.VT.Ptm
					go func() {
						time.Sleep(50 * time.Millisecond)
						ptm.Write([]byte{'\r'})
					}()
				} else if o.OnSubmit != nil {
					// Non-normal: route through session for priority-aware delivery.
					o.OnSubmit(cmd, o.InputPriority)
				}
				o.History = append(o.History, cmd)
				o.Input = o.Input[:0]
				o.CursorPos = 0
				o.InputPriority = message.PriorityNormal
			} else {
				if !o.writePTYOrHang([]byte{'\r'}) {
					return n
				}
			}
			o.HistIdx = -1
			o.Saved = nil
			o.RenderBar()

		case 0x7F, 0x08:
			if o.CursorPos > 0 {
				o.DeleteBackward()
				o.RenderBar()
			}

		case 0x01: // ctrl+a — move to start (pass through if input empty)
			if len(o.Input) > 0 {
				o.CursorToStart()
				o.RenderBar()
			} else {
				if !o.writePTYOrHang([]byte{b}) {
					return n
				}
			}

		case 0x05: // ctrl+e — move to end (pass through if input empty)
			if len(o.Input) > 0 {
				o.CursorToEnd()
				o.RenderBar()
			} else {
				if !o.writePTYOrHang([]byte{b}) {
					return n
				}
			}

		case 0x0B: // ctrl+k — kill to end of line (pass through if input empty)
			if len(o.Input) > 0 {
				o.KillToEnd()
				o.RenderBar()
			} else {
				if !o.writePTYOrHang([]byte{b}) {
					return n
				}
			}

		case 0x15: // ctrl+u — kill to start of line (pass through if input empty)
			if len(o.Input) > 0 {
				o.KillToStart()
				o.RenderBar()
			} else {
				if !o.writePTYOrHang([]byte{b}) {
					return n
				}
			}

		default:
			if b < 0x20 {
				if !o.writePTYOrHang([]byte{b}) {
					return n
				}
			} else {
				o.InsertByte(b)
				o.RenderBar()
			}
		}
	}
	return n
}

func (o *Overlay) FlushPassthroughEscIfComplete() bool {
	if len(o.PassthroughEsc) == 0 {
		return false
	}
	if !virtualterminal.IsEscSequenceComplete(o.PassthroughEsc) {
		return false
	}
	if virtualterminal.IsShiftEnterSequence(o.PassthroughEsc) {
		o.writePTYOrHang([]byte{'\n'})
	} else {
		o.writePTYOrHang(o.PassthroughEsc)
	}
	o.PassthroughEsc = o.PassthroughEsc[:0]
	return true
}

// HandleEscape processes bytes following an ESC (0x1B).
func (o *Overlay) HandleEscape(remaining []byte) (consumed int, handled bool) {
	if len(remaining) == 0 {
		return 0, false
	}

	switch remaining[0] {
	case '[':
		return o.HandleCSI(remaining[1:])
	case 'O':
		if len(remaining) >= 2 {
			return 2, true
		}
		return 1, true
	case 'f': // meta+f — forward word
		if o.Mode == ModeDefault && len(o.Input) > 0 {
			o.CursorForwardWord()
			o.RenderBar()
			return 1, true
		}
		return 0, false
	case 'b': // meta+b — backward word
		if o.Mode == ModeDefault && len(o.Input) > 0 {
			o.CursorBackwardWord()
			o.RenderBar()
			return 1, true
		}
		return 0, false
	}
	return 0, false
}

// HandleCSI processes a CSI sequence (after ESC [).
func (o *Overlay) HandleCSI(remaining []byte) (consumed int, handled bool) {
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

	switch final {
	case 'A', 'B':
		if o.Mode == ModePassthrough {
			o.writePTYOrHang(append([]byte{0x1B, '['}, remaining[:i+1]...))
			break
		}
		if o.Mode == ModeScroll {
			if final == 'A' {
				o.ScrollUp(1)
			} else {
				o.ScrollDown(1)
			}
			break
		}
		// Up/Down in default or menu mode: no-op
	case 'C', 'D':
		if o.Mode == ModePassthrough {
			o.writePTYOrHang(append([]byte{0x1B, '['}, remaining[:i+1]...))
			break
		}
		if o.Mode == ModeDefault && len(o.Input) > 0 {
			if final == 'D' {
				o.CursorLeft()
			} else {
				o.CursorRight()
			}
			o.RenderBar()
		}
	case 'M', 'm':
		o.HandleSGRMouse(remaining[:i], final == 'M')
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
func (o *Overlay) CyclePriority() {
	for i, p := range priorityOrder {
		if p == o.InputPriority {
			o.InputPriority = priorityOrder[(i+1)%len(priorityOrder)]
			return
		}
	}
	o.InputPriority = message.PriorityNormal
}

// HandleScrollBytes processes input when in scroll mode.
// Esc or q exits scroll mode. Arrow keys scroll. All other input is ignored.
func (o *Overlay) HandleScrollBytes(buf []byte, start, n int) int {
	for i := start; i < n; {
		b := buf[i]

		// Handle continuation of a pending ESC from a previous read.
		if o.PendingEsc {
			o.CancelPendingEsc()
			consumed, handled := o.HandleEscape(buf[i:n])
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
				consumed, handled := o.HandleEscape(buf[i:n])
				i += consumed
				if handled {
					continue
				}
				// ESC followed by unrecognized byte — ignore.
			} else {
				// ESC at end of buffer — wait to see if it's bare Esc.
				o.StartPendingEsc()
			}
		default:
			// Pass control characters through to the PTY.
			if b < 0x20 && !o.ChildExited && !o.ChildHung {
				if !o.writePTYOrHang([]byte{b}) {
					return n
				}
			}
		}
	}
	return n
}

// EnterScrollMode switches to scroll mode, freezing the display.
func (o *Overlay) EnterScrollMode() {
	o.setMode(ModeScroll)
	o.ScrollOffset = 0
	o.RenderScreen()
	o.RenderBar()
}

// ExitScrollMode returns to default mode and re-renders the live view.
func (o *Overlay) ExitScrollMode() {
	o.ScrollOffset = 0
	o.setMode(ModeDefault)
	o.RenderScreen()
	o.RenderBar()
}

// ScrollUp moves the scroll view up by the given number of lines.
// If the offset is already at the maximum, this is a no-op to avoid re-rendering.
func (o *Overlay) ScrollUp(lines int) {
	prev := o.ScrollOffset
	o.ScrollOffset += lines
	o.ClampScrollOffset()
	if o.ScrollOffset == prev {
		return
	}
	o.RenderScreen()
	o.RenderBar()
}

// ScrollDown moves the scroll view down by the given number of lines.
// If we reach the bottom (offset 0), exits scroll mode.
func (o *Overlay) ScrollDown(lines int) {
	o.ScrollOffset -= lines
	if o.ScrollOffset <= 0 {
		o.ExitScrollMode()
		return
	}
	o.ClampScrollOffset()
	o.RenderScreen()
	o.RenderBar()
}

// ClampScrollOffset ensures ScrollOffset is within valid bounds.
func (o *Overlay) ClampScrollOffset() {
	if o.VT.Scrollback == nil {
		o.ScrollOffset = 0
		return
	}
	maxOffset := o.VT.Scrollback.Cursor.Y - o.VT.ChildRows + 1
	if maxOffset < 0 {
		maxOffset = 0
	}
	if o.ScrollOffset > maxOffset {
		o.ScrollOffset = maxOffset
	}
	if o.ScrollOffset < 0 {
		o.ScrollOffset = 0
	}
}

// HandleSGRMouse processes an SGR mouse event. The params bytes contain
// the "<Cb;Cx;Cy" portion (everything between ESC[ and the final M/m).
// press is true for button press (M), false for release (m).
// Button 0 = left click, 64 = scroll up, 65 = scroll down.
func (o *Overlay) HandleSGRMouse(params []byte, press bool) {
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
			o.ShowSelectHint()
		}
	case 64: // scroll up
		if o.Mode == ModePassthrough {
			return
		}
		if o.Mode != ModeScroll {
			o.EnterScrollMode()
		}
		o.ScrollUp(scrollStep)
	case 65: // scroll down
		if o.Mode == ModeScroll {
			o.ScrollDown(scrollStep)
		}
	}
}

// ShowSelectHint displays a transient hint about using shift for text selection.
func (o *Overlay) ShowSelectHint() {
	o.SelectHint = true
	if o.SelectHintTimer != nil {
		o.SelectHintTimer.Stop()
	}
	o.RenderScreen()
	o.RenderBar()
	o.SelectHintTimer = time.AfterFunc(3*time.Second, func() {
		o.VT.Mu.Lock()
		defer o.VT.Mu.Unlock()
		o.SelectHint = false
		o.RenderScreen()
		o.RenderBar()
	})
}
