package overlay

import (
	"time"
	"unicode/utf8"

	"h2/internal/virtualterminal"
)

const ptyWriteTimeout = 3 * time.Second

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
// Enter relaunches, q quits.
func (o *Overlay) HandleExitedBytes(buf []byte, i, n int) int {
	for ; i < n; i++ {
		switch buf[i] {
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
		if o.PendingEsc && o.Mode == ModePassthrough {
			o.PendingEsc = false
			o.PassthroughEsc = o.PassthroughEsc[:0]
			o.setMode(ModeDefault)
			o.RenderBar()
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
			if i == n {
				o.setMode(ModeDefault)
				o.RenderBar()
			}
			continue
		}
		switch b {
		case 0x0D, 0x0A:
			o.MenuSelect()
			o.setMode(ModeDefault)
			o.RenderBar()
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

		if o.PendingSlash {
			o.CancelPendingSlash()
			if b == '/' {
				o.setMode(ModeMenu)
				o.MenuIdx = 0
				o.RenderBar()
				continue
			}
			o.setMode(ModePassthrough)
			if !o.writePTYOrHang([]byte{'/'}) {
				return n
			}
			o.RenderBar()
			switch b {
			case 0x0D, 0x0A:
				if !o.writePTYOrHang([]byte{'\r'}) {
					return n
				}
				o.setMode(ModeDefault)
				o.RenderBar()
			case 0x1B:
				if i == n {
					o.setMode(ModeDefault)
					o.RenderBar()
				} else {
					if !o.writePTYOrHang([]byte{0x1B}) {
						return n
					}
				}
			default:
				if !o.writePTYOrHang([]byte{b}) {
					return n
				}
			}
			continue
		}

		if b == '/' && len(o.Input) == 0 {
			o.StartPendingSlash()
			o.RenderBar()
			continue
		}

		if b == 0x1B {
			consumed, handled := o.HandleEscape(buf[i:n])
			i += consumed
			if handled {
				continue
			}
			continue
		}

		switch b {
		case 0x09:
			if !o.writePTYOrHang([]byte{'\t'}) {
				return n
			}

		case 0x0D, 0x0A:
			if len(o.Input) > 0 {
				cmd := string(o.Input)
				if !o.writePTYOrHang(o.Input) {
					return n
				}
				o.History = append(o.History, cmd)
				o.Input = o.Input[:0]
				ptm := o.VT.Ptm
				go func() {
					time.Sleep(50 * time.Millisecond)
					ptm.Write([]byte{'\r'})
				}()
			} else {
				if !o.writePTYOrHang([]byte{'\r'}) {
					return n
				}
			}
			o.HistIdx = -1
			o.Saved = nil
			o.RenderBar()

		case 0x7F, 0x08:
			if len(o.Input) > 0 {
				_, size := utf8.DecodeLastRune(o.Input)
				o.Input = o.Input[:len(o.Input)-size]
				o.RenderBar()
			}

		default:
			if b < 0x20 {
				if !o.writePTYOrHang([]byte{b}) {
					return n
				}
			} else {
				o.Input = append(o.Input, b)
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
		if o.Mode == ModeMenu {
			if final == 'A' {
				o.MenuPrev()
			} else {
				o.MenuNext()
			}
			o.RenderBar()
			break
		}
		if final == 'A' {
			o.HistoryUp()
		} else {
			o.HistoryDown()
		}
		o.RenderBar()
	case 'C', 'D':
		if o.Mode == ModePassthrough {
			o.writePTYOrHang(append([]byte{0x1B, '['}, remaining[:i+1]...))
			break
		}
		if o.Mode == ModeMenu {
			if final == 'D' {
				o.MenuPrev()
			} else {
				o.MenuNext()
			}
			o.RenderBar()
		}
	}

	return totalConsumed, true
}

func (o *Overlay) StartPendingSlash() {
	o.PendingSlash = true
	if o.SlashTimer != nil {
		o.SlashTimer.Stop()
	}
	o.SlashTimer = time.AfterFunc(250*time.Millisecond, func() {
		o.VT.Mu.Lock()
		defer o.VT.Mu.Unlock()
		if !o.PendingSlash || o.Mode != ModeDefault {
			return
		}
		o.PendingSlash = false
		o.setMode(ModePassthrough)
		if !o.writePTYOrHang([]byte{'/'}) {
			return
		}
		o.RenderBar()
	})
}

func (o *Overlay) CancelPendingSlash() {
	o.PendingSlash = false
	if o.SlashTimer != nil {
		o.SlashTimer.Stop()
	}
}
