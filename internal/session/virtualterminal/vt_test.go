package virtualterminal

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestWritePTY_Success(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Drain the pipe in background so writes succeed.
	go func() {
		buf := make([]byte, 1024)
		for {
			if _, err := r.Read(buf); err != nil {
				return
			}
		}
	}()
	defer r.Close()

	vt := &VT{Ptm: w}
	n, err := vt.WritePTY([]byte("hello"), time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected n=5, got %d", n)
	}
}

func TestWritePTY_Timeout(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	// Fill the pipe buffer so subsequent writes block.
	chunk := make([]byte, 4096)
	for {
		_ = w.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
		_, err := w.Write(chunk)
		if err != nil {
			break
		}
	}
	_ = w.SetWriteDeadline(time.Time{}) // clear deadline

	vt := &VT{Ptm: w}
	start := time.Now()
	_, err = vt.WritePTY([]byte("x"), 100*time.Millisecond)
	elapsed := time.Since(start)

	if err != ErrPTYWriteTimeout {
		t.Fatalf("expected ErrPTYWriteTimeout, got %v", err)
	}
	if elapsed < 50*time.Millisecond {
		t.Fatalf("returned too fast (%v), timeout may not be working", elapsed)
	}
}

func TestWritePTY_WriteError(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// Close the read end so writes get EPIPE.
	r.Close()

	vt := &VT{Ptm: w}
	_, err = vt.WritePTY([]byte("hello"), time.Second)
	w.Close()

	if err == nil {
		t.Fatal("expected an error from writing to broken pipe")
	}
	if err == ErrPTYWriteTimeout {
		t.Fatal("expected a pipe error, not a timeout")
	}
}

// --- CapturePlainHistory ---

func TestCapturePlainHistory_SimpleLines(t *testing.T) {
	vt := &VT{}
	vt.CapturePlainHistory([]byte("hello\nworld\n"))
	if len(vt.PlainHistory) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(vt.PlainHistory))
	}
	if vt.PlainHistory[0] != "hello" || vt.PlainHistory[1] != "world" {
		t.Fatalf("unexpected lines: %v", vt.PlainHistory)
	}
}

func TestCapturePlainHistory_CRLFPreservesContent(t *testing.T) {
	vt := &VT{}
	vt.CapturePlainHistory([]byte("line one\r\nline two\r\n"))
	if len(vt.PlainHistory) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(vt.PlainHistory))
	}
	if vt.PlainHistory[0] != "line one" || vt.PlainHistory[1] != "line two" {
		t.Fatalf("unexpected lines: %v", vt.PlainHistory)
	}
}

func TestCapturePlainHistory_SGRStripped(t *testing.T) {
	// ESC[31m = red, ESC[0m = reset
	vt := &VT{}
	vt.CapturePlainHistory([]byte("\033[31mred text\033[0m\n"))
	if len(vt.PlainHistory) != 1 {
		t.Fatalf("expected 1 line, got %d", len(vt.PlainHistory))
	}
	if vt.PlainHistory[0] != "red text" {
		t.Fatalf("expected 'red text', got %q", vt.PlainHistory[0])
	}
}

func TestCapturePlainHistory_CursorPositionDiscardsAccumulated(t *testing.T) {
	// Simulate TUI repaint: cursor position to row;col, write content, repeat.
	// ESC[5;1H = cursor to row 5 col 1
	vt := &VT{}
	vt.CapturePlainHistory([]byte("\033[5;1Hviewport row 1\033[6;1Hviewport row 2\033[7;1Hviewport row 3"))
	// All viewport content should be discarded (no \n to flush).
	if len(vt.PlainHistory) != 0 {
		t.Fatalf("expected 0 lines (viewport repaint discarded), got %d: %v", len(vt.PlainHistory), vt.PlainHistory)
	}
}

func TestCapturePlainHistory_ScrollRegionThenRepaint(t *testing.T) {
	// Simulate codex inline viewport pattern:
	// 1. Set scroll region + emit history lines with \r\n
	// 2. Reset scroll region + cursor-positioned viewport repaint
	var data []byte
	// Phase 1: scroll region with real content
	data = append(data, "\033[1;20r"...) // set scroll region (CSI r)
	data = append(data, "\033[1;1H"...)  // cursor to top (CSI H)
	data = append(data, "history line 1\r\n"...)
	data = append(data, "history line 2\r\n"...)
	data = append(data, "history line 3\r\n"...)
	data = append(data, "\033[r"...) // reset scroll region (CSI r)
	// Phase 2: viewport repaint via cursor positioning
	data = append(data, "\033[5;1H"...) // cursor to row 5
	data = append(data, "viewport row A"...)
	data = append(data, "\033[6;1H"...) // cursor to row 6
	data = append(data, "viewport row B"...)
	data = append(data, "\033[7;1H"...) // cursor to row 7
	data = append(data, "viewport row C"...)

	vt := &VT{}
	vt.CapturePlainHistory(data)

	// Should capture only the 3 real history lines, not viewport repaints.
	if len(vt.PlainHistory) != 3 {
		t.Fatalf("expected 3 history lines, got %d: %v", len(vt.PlainHistory), vt.PlainHistory)
	}
	if vt.PlainHistory[0] != "history line 1" {
		t.Fatalf("line 0: expected 'history line 1', got %q", vt.PlainHistory[0])
	}
	if vt.PlainHistory[1] != "history line 2" {
		t.Fatalf("line 1: expected 'history line 2', got %q", vt.PlainHistory[1])
	}
	if vt.PlainHistory[2] != "history line 3" {
		t.Fatalf("line 2: expected 'history line 3', got %q", vt.PlainHistory[2])
	}
}

func TestCapturePlainHistory_EraseDisplayClearsLine(t *testing.T) {
	// ESC[2J = erase display
	vt := &VT{}
	vt.CapturePlainHistory([]byte("stale content\033[2Jfresh\n"))
	if len(vt.PlainHistory) != 1 {
		t.Fatalf("expected 1 line, got %d", len(vt.PlainHistory))
	}
	if vt.PlainHistory[0] != "fresh" {
		t.Fatalf("expected 'fresh', got %q", vt.PlainHistory[0])
	}
}

func TestCapturePlainHistory_MultipleCycles(t *testing.T) {
	// Simulate multiple cycles of scroll-region content + viewport repaint.
	vt := &VT{}

	for cycle := 0; cycle < 5; cycle++ {
		var data []byte
		data = append(data, "\033[1;20r"...)
		data = append(data, "\033[1;1H"...)
		data = append(data, []byte("cycle "+strings.Repeat("x", cycle)+"\r\n")...)
		data = append(data, "\033[r"...)
		data = append(data, "\033[5;1H"...)
		data = append(data, "repaint content"...)
		vt.CapturePlainHistory(data)
	}

	if len(vt.PlainHistory) != 5 {
		t.Fatalf("expected 5 lines across cycles, got %d: %v", len(vt.PlainHistory), vt.PlainHistory)
	}
}

func TestCapturePlainHistory_OSCStripped(t *testing.T) {
	// OSC sequence: ESC ] ... BEL
	vt := &VT{}
	vt.CapturePlainHistory([]byte("\033]0;window title\007visible\n"))
	if len(vt.PlainHistory) != 1 || vt.PlainHistory[0] != "visible" {
		t.Fatalf("expected ['visible'], got %v", vt.PlainHistory)
	}
}

func TestCapturePlainHistory_MaxLines(t *testing.T) {
	vt := &VT{}
	vt.plainMaxLines = 10
	for i := 0; i < 20; i++ {
		vt.CapturePlainHistory([]byte("line\n"))
	}
	if len(vt.PlainHistory) != 10 {
		t.Fatalf("expected 10 lines (trimmed), got %d", len(vt.PlainHistory))
	}
}

func TestCapturePlainHistory_DetectsScrollRegion(t *testing.T) {
	vt := &VT{}
	if vt.ScrollRegionUsed {
		t.Fatal("expected ScrollRegionUsed=false initially")
	}
	// Send DECSTBM: CSI 1;20 r
	vt.CapturePlainHistory([]byte("\033[1;20r"))
	if !vt.ScrollRegionUsed {
		t.Fatal("expected ScrollRegionUsed=true after DECSTBM")
	}
}

func TestCapturePlainHistory_NoScrollRegionForNormalApps(t *testing.T) {
	vt := &VT{}
	// Normal output with SGR colors, cursor positioning, but no scroll regions.
	vt.CapturePlainHistory([]byte("\033[31mhello\033[0m\r\n\033[5;1Hworld\n"))
	if vt.ScrollRegionUsed {
		t.Fatal("expected ScrollRegionUsed=false without DECSTBM")
	}
}
