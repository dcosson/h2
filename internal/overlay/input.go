package overlay

import (
	"time"
	"unicode/utf8"

	"h2/internal/virtualterminal"
)

func (o *Overlay) setMode(mode InputMode) {
	o.Mode = mode
	if o.OnModeChange != nil {
		o.OnModeChange(mode)
	}
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
			if o.FlushPassthroughEscIfComplete() {
				i++
				continue
			}
			i++
			continue
		}
		if len(o.PassthroughEsc) > 0 {
			o.PassthroughEsc = append(o.PassthroughEsc, b)
			if o.FlushPassthroughEscIfComplete() {
				i++
				continue
			}
			i++
			continue
		}
		switch b {
		case 0x0D, 0x0A:
			o.CancelPendingEsc()
			o.PassthroughEsc = o.PassthroughEsc[:0]
			o.VT.Ptm.Write([]byte{'\r'})
			o.setMode(ModeDefault)
			o.RenderBar()
			i++
		case 0x1B:
			o.StartPendingEsc()
			i++
		case 0x7F, 0x08:
			o.VT.Ptm.Write([]byte{b})
			i++
		default:
			o.VT.Ptm.Write([]byte{b})
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
			o.VT.Ptm.Write([]byte{'/'})
			o.RenderBar()
			switch b {
			case 0x0D, 0x0A:
				o.VT.Ptm.Write([]byte{'\r'})
				o.setMode(ModeDefault)
				o.RenderBar()
			case 0x1B:
				if i == n {
					o.setMode(ModeDefault)
					o.RenderBar()
				} else {
					o.VT.Ptm.Write([]byte{0x1B})
				}
			default:
				o.VT.Ptm.Write([]byte{b})
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
			o.VT.Ptm.Write([]byte{'\t'})

		case 0x0D, 0x0A:
			if len(o.Input) > 0 {
				cmd := string(o.Input)
				o.VT.Ptm.Write(o.Input)
				o.History = append(o.History, cmd)
				o.Input = o.Input[:0]
				ptm := o.VT.Ptm
				go func() {
					time.Sleep(50 * time.Millisecond)
					ptm.Write([]byte{'\r'})
				}()
			} else {
				o.VT.Ptm.Write([]byte{'\r'})
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
				o.VT.Ptm.Write([]byte{b})
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
		o.VT.Ptm.Write([]byte{'\n'})
	} else {
		o.VT.Ptm.Write(o.PassthroughEsc)
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
			o.VT.Ptm.Write(append([]byte{0x1B, '['}, remaining[:i+1]...))
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
			o.VT.Ptm.Write(append([]byte{0x1B, '['}, remaining[:i+1]...))
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
		o.VT.Ptm.Write([]byte{'/'})
		o.RenderBar()
	})
}

func (o *Overlay) CancelPendingSlash() {
	o.PendingSlash = false
	if o.SlashTimer != nil {
		o.SlashTimer.Stop()
	}
}
