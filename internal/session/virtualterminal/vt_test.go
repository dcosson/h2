package virtualterminal

import (
	"os"
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

// --- ScanPTYOutput (scroll region detection) ---

func TestScanPTYOutput_DetectsScrollRegion(t *testing.T) {
	vt := &VT{}
	if vt.ScrollRegionUsed {
		t.Fatal("expected ScrollRegionUsed=false initially")
	}
	// Send DECSTBM: CSI 1;20 r
	vt.ScanPTYOutput([]byte("\033[1;20r"))
	if !vt.ScrollRegionUsed {
		t.Fatal("expected ScrollRegionUsed=true after DECSTBM")
	}
}

func TestScanPTYOutput_NoScrollRegionForNormalApps(t *testing.T) {
	vt := &VT{}
	// Normal output with SGR colors, cursor positioning, but no scroll regions.
	vt.ScanPTYOutput([]byte("\033[31mhello\033[0m\r\n\033[5;1Hworld\n"))
	if vt.ScrollRegionUsed {
		t.Fatal("expected ScrollRegionUsed=false without DECSTBM")
	}
}

func TestScanPTYOutput_StopsAfterDetection(t *testing.T) {
	vt := &VT{}
	vt.ScanPTYOutput([]byte("\033[1;20r"))
	if !vt.ScrollRegionUsed {
		t.Fatal("expected ScrollRegionUsed=true")
	}
	// Subsequent calls should be a no-op (early return).
	vt.scanState = 99 // corrupt state to verify early return
	vt.ScanPTYOutput([]byte("\033[m"))
	if vt.scanState != 99 {
		t.Fatal("expected scanState unchanged after ScrollRegionUsed=true")
	}
}

func TestScanPTYOutput_SplitAcrossChunks(t *testing.T) {
	vt := &VT{}
	// Split ESC [ 1 ; 2 0 r across two chunks.
	vt.ScanPTYOutput([]byte("\033[1;2"))
	if vt.ScrollRegionUsed {
		t.Fatal("should not detect scroll region mid-sequence")
	}
	vt.ScanPTYOutput([]byte("0r"))
	if !vt.ScrollRegionUsed {
		t.Fatal("expected ScrollRegionUsed=true after completing CSI...r across chunks")
	}
}

func TestScanPTYOutput_IgnoresOtherCSIFinals(t *testing.T) {
	vt := &VT{}
	// CSI H (cursor position), CSI m (SGR), CSI J (erase display)
	vt.ScanPTYOutput([]byte("\033[5;1H\033[31m\033[2J"))
	if vt.ScrollRegionUsed {
		t.Fatal("expected ScrollRegionUsed=false for non-DECSTBM sequences")
	}
}

func TestScanPTYOutput_SkipsOSCSequences(t *testing.T) {
	vt := &VT{}
	// OSC with BEL terminator, then DECSTBM
	vt.ScanPTYOutput([]byte("\033]0;window title\007\033[1;20r"))
	if !vt.ScrollRegionUsed {
		t.Fatal("expected ScrollRegionUsed=true after OSC then DECSTBM")
	}
}

func TestScanPTYOutput_SkipsOSCWithSTTerminator(t *testing.T) {
	vt := &VT{}
	// OSC with ST (ESC \) terminator, then DECSTBM
	vt.ScanPTYOutput([]byte("\033]0;title\033\\\033[1;20r"))
	if !vt.ScrollRegionUsed {
		t.Fatal("expected ScrollRegionUsed=true after OSC-ST then DECSTBM")
	}
}

func TestScanPTYOutput_BareResetScrollRegion(t *testing.T) {
	vt := &VT{}
	// CSI r with no parameters (reset scroll region) — still uses 'r' final byte.
	vt.ScanPTYOutput([]byte("\033[r"))
	if !vt.ScrollRegionUsed {
		t.Fatal("expected ScrollRegionUsed=true for bare CSI r")
	}
}
