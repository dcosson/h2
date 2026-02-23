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

func TestScanPTYOutput_ContinuesScanningAfterScrollRegion(t *testing.T) {
	vt := &VT{}
	vt.ScanPTYOutput([]byte("\033[1;20r"))
	if !vt.ScrollRegionUsed {
		t.Fatal("expected ScrollRegionUsed=true")
	}
	// After ScrollRegionUsed is set, scanner should still detect ?1007h.
	vt.ScanPTYOutput([]byte("\033[?1007h"))
	if !vt.AltScrollEnabled {
		t.Fatal("expected AltScrollEnabled=true after ?1007h")
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

// --- ScanPTYOutput (alternate scroll detection) ---

func TestScanPTYOutput_DetectsAltScrollEnable(t *testing.T) {
	vt := &VT{}
	vt.ScanPTYOutput([]byte("\033[?1007h"))
	if !vt.AltScrollEnabled {
		t.Fatal("expected AltScrollEnabled=true after ?1007h")
	}
}

func TestScanPTYOutput_DetectsAltScrollDisable(t *testing.T) {
	vt := &VT{}
	vt.AltScrollEnabled = true
	vt.ScanPTYOutput([]byte("\033[?1007l"))
	if vt.AltScrollEnabled {
		t.Fatal("expected AltScrollEnabled=false after ?1007l")
	}
}

func TestScanPTYOutput_AltScrollToggle(t *testing.T) {
	vt := &VT{}
	vt.ScanPTYOutput([]byte("\033[?1007h"))
	if !vt.AltScrollEnabled {
		t.Fatal("expected AltScrollEnabled=true")
	}
	vt.ScanPTYOutput([]byte("\033[?1007l"))
	if vt.AltScrollEnabled {
		t.Fatal("expected AltScrollEnabled=false after disable")
	}
}

func TestScanPTYOutput_AltScrollSplitAcrossChunks(t *testing.T) {
	vt := &VT{}
	// Split ESC [ ? 1 0 0 7 h across two chunks.
	vt.ScanPTYOutput([]byte("\033[?10"))
	if vt.AltScrollEnabled {
		t.Fatal("should not enable mid-sequence")
	}
	vt.ScanPTYOutput([]byte("07h"))
	if !vt.AltScrollEnabled {
		t.Fatal("expected AltScrollEnabled=true after completing ?1007h across chunks")
	}
}

func TestScanPTYOutput_IgnoresOtherPrivateModes(t *testing.T) {
	vt := &VT{}
	// ?1049h (alt screen) should not affect AltScrollEnabled.
	vt.ScanPTYOutput([]byte("\033[?1049h"))
	if vt.AltScrollEnabled {
		t.Fatal("expected AltScrollEnabled=false for ?1049h")
	}
}

func TestScanPTYOutput_AltScrollAfterScrollRegion(t *testing.T) {
	vt := &VT{}
	// DECSTBM first, then ?1007h in the same chunk.
	vt.ScanPTYOutput([]byte("\033[1;20r\033[?1007h"))
	if !vt.ScrollRegionUsed {
		t.Fatal("expected ScrollRegionUsed=true")
	}
	if !vt.AltScrollEnabled {
		t.Fatal("expected AltScrollEnabled=true")
	}
}

func TestScanPTYOutput_PrivateModeWithSemicolon(t *testing.T) {
	vt := &VT{}
	// Compound private mode: ?1000;1006h — semicolon should bail out.
	vt.ScanPTYOutput([]byte("\033[?1000;1006h"))
	if vt.AltScrollEnabled {
		t.Fatal("compound private mode should not set AltScrollEnabled")
	}
}

func TestResetScanState(t *testing.T) {
	vt := &VT{}
	vt.ScrollRegionUsed = true
	vt.AltScrollEnabled = true
	vt.scanState = scanCSI
	vt.scanCSIPrivateNum = 42
	vt.ResetScanState()
	if vt.ScrollRegionUsed {
		t.Fatal("expected ScrollRegionUsed=false after reset")
	}
	if vt.AltScrollEnabled {
		t.Fatal("expected AltScrollEnabled=false after reset")
	}
	if vt.scanState != scanNormal {
		t.Fatal("expected scanState=scanNormal after reset")
	}
	if vt.scanCSIPrivateNum != 0 {
		t.Fatal("expected scanCSIPrivateNum=0 after reset")
	}
}
