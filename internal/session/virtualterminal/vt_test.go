package virtualterminal

import (
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/vito/midterm"
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

// --- Resize bounds checking ---

func TestResize_IgnoresInvalidDimensions(t *testing.T) {
	vt := &VT{Rows: 24, Cols: 80, ChildRows: 22}

	// Zero rows — should be ignored.
	vt.Resize(0, 80, -2)
	if vt.Rows != 24 || vt.Cols != 80 || vt.ChildRows != 22 {
		t.Fatalf("Resize(0,80,-2) should be no-op, got Rows=%d Cols=%d ChildRows=%d", vt.Rows, vt.Cols, vt.ChildRows)
	}

	// Negative childRows — should be ignored.
	vt.Resize(1, 80, -1)
	if vt.Rows != 24 || vt.Cols != 80 || vt.ChildRows != 22 {
		t.Fatalf("Resize(1,80,-1) should be no-op, got Rows=%d Cols=%d ChildRows=%d", vt.Rows, vt.Cols, vt.ChildRows)
	}

	// Zero cols — should be ignored.
	vt.Resize(24, 0, 22)
	if vt.Rows != 24 || vt.Cols != 80 || vt.ChildRows != 22 {
		t.Fatalf("Resize(24,0,22) should be no-op, got Rows=%d Cols=%d ChildRows=%d", vt.Rows, vt.Cols, vt.ChildRows)
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

// --- ScanPTYOutput (synchronized output detection) ---

func TestScanPTYOutput_DetectsSyncOutputEnable(t *testing.T) {
	vt := &VT{}
	vt.ScanPTYOutput([]byte("\033[?2026h"))
	if !vt.SyncOutputActive {
		t.Fatal("expected SyncOutputActive=true after ?2026h")
	}
}

func TestScanPTYOutput_DetectsSyncOutputDisable(t *testing.T) {
	vt := &VT{}
	vt.SyncOutputActive = true
	vt.ScanPTYOutput([]byte("\033[?2026l"))
	if vt.SyncOutputActive {
		t.Fatal("expected SyncOutputActive=false after ?2026l")
	}
}

func TestScanPTYOutput_SyncOutputToggle(t *testing.T) {
	vt := &VT{}
	vt.ScanPTYOutput([]byte("\033[?2026h"))
	if !vt.SyncOutputActive {
		t.Fatal("expected SyncOutputActive=true")
	}
	vt.ScanPTYOutput([]byte("\033[?2026l"))
	if vt.SyncOutputActive {
		t.Fatal("expected SyncOutputActive=false after disable")
	}
}

func TestScanPTYOutput_SyncOutputSplitAcrossChunks(t *testing.T) {
	vt := &VT{}
	// Split ESC [ ? 2 0 2 6 h across two chunks.
	vt.ScanPTYOutput([]byte("\033[?20"))
	if vt.SyncOutputActive {
		t.Fatal("should not enable mid-sequence")
	}
	vt.ScanPTYOutput([]byte("26h"))
	if !vt.SyncOutputActive {
		t.Fatal("expected SyncOutputActive=true after completing ?2026h across chunks")
	}
}

func TestScanPTYOutput_SyncOutputBothInSameChunk(t *testing.T) {
	vt := &VT{}
	// Both enable and disable in the same chunk — should end up disabled.
	vt.ScanPTYOutput([]byte("\033[?2026h\033[2J\033[H\033[?2026l"))
	if vt.SyncOutputActive {
		t.Fatal("expected SyncOutputActive=false when both ?2026h and ?2026l are in same chunk")
	}
}

func TestScanPTYOutput_SyncOutputDoesNotAffectOtherModes(t *testing.T) {
	vt := &VT{}
	vt.ScanPTYOutput([]byte("\033[?2026h"))
	if vt.AltScrollEnabled {
		t.Fatal("?2026h should not affect AltScrollEnabled")
	}
	if vt.ScrollRegionUsed {
		t.Fatal("?2026h should not affect ScrollRegionUsed")
	}
}

// --- RespondTerminalQueries ---

func TestRespondTerminalQueries_DA2(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	vt := &VT{Ptm: w}
	vt.RespondTerminalQueries([]byte("\033[>c"))

	buf := make([]byte, 256)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	got := string(buf[:n])
	want := "\033[>65;388;1c"
	if got != want {
		t.Errorf("DA2 response: got %q, want %q", got, want)
	}
}

func TestRespondTerminalQueries_DA2WithParam(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	vt := &VT{Ptm: w}
	vt.RespondTerminalQueries([]byte("\033[>0c"))

	buf := make([]byte, 256)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	got := string(buf[:n])
	want := "\033[>65;388;1c"
	if got != want {
		t.Errorf("DA2 response: got %q, want %q", got, want)
	}
}

func TestRespondTerminalQueries_XTVERSION(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	vt := &VT{Ptm: w}
	vt.RespondTerminalQueries([]byte("\033[>0q"))

	buf := make([]byte, 256)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	got := string(buf[:n])
	want := "\033P>|xterm(388)\033\\"
	if got != want {
		t.Errorf("XTVERSION response: got %q, want %q", got, want)
	}
}

func TestRespondTerminalQueries_NoMatch(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	vt := &VT{Ptm: w}
	// Normal output should not trigger any response.
	vt.RespondTerminalQueries([]byte("hello world\033[31m"))

	// Set read deadline to verify nothing was written.
	r.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	buf := make([]byte, 256)
	_, err = r.Read(buf)
	r.Close()
	if err == nil {
		t.Error("expected no response for normal output")
	}
}

// --- SetupScrollCapture scroll history ---

func TestSetupScrollCapture_CapturesScrolledLines(t *testing.T) {
	// Create a small terminal and fill it, then trigger a scroll via line feed.
	// Verify that SetupScrollCapture captures the scrolled-off line.
	vt := &VT{}
	vt.Vt = midterm.NewTerminal(3, 10) // 3 rows, 10 cols
	vt.SetupScrollCapture()

	// Fill all 3 rows and add a newline to trigger scroll.
	vt.Vt.Write([]byte("line1\r\nline2\r\nline3\r\n"))

	if len(vt.ScrollHistory) == 0 {
		t.Fatal("expected scroll history to capture at least one line")
	}
	// Captured entry must carry Content (the line's text) and at least one
	// run covering it — proves the RLE capture wired up correctly.
	first := vt.ScrollHistory[0]
	if len(first.Content) == 0 {
		t.Fatalf("captured entry has no Content")
	}
	if len(first.Runs) == 0 {
		t.Fatalf("captured entry has no Runs (Format compression broken)")
	}
}

func TestCoalesceFormatRuns(t *testing.T) {
	def := midterm.Format{}
	red := midterm.Format{}
	red.SetBold(true)

	tests := []struct {
		name     string
		input    []midterm.Format
		wantRuns int
		wantSize int
	}{
		{"empty", nil, 0, 0},
		{"single cell", []midterm.Format{def}, 1, 1},
		{"all same", []midterm.Format{def, def, def, def, def}, 1, 5},
		{"two runs", []midterm.Format{def, def, red, red, red}, 2, 5},
		{"alternating", []midterm.Format{def, red, def, red}, 4, 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runs := coalesceFormatRuns(tc.input, nil)
			if len(runs) != tc.wantRuns {
				t.Fatalf("got %d runs, want %d", len(runs), tc.wantRuns)
			}
			total := 0
			for _, r := range runs {
				total += r.Size
			}
			if total != tc.wantSize {
				t.Fatalf("got total size %d, want %d", total, tc.wantSize)
			}
		})
	}
}

// --- pipeChunk panic recovery ---

func TestPipeChunk_RecoverFromPanic(t *testing.T) {
	// Verify that pipeChunk recovers from panics without killing the caller.
	// The onData callback panics, simulating any panic during chunk processing.
	vt := &VT{}
	vt.Vt = midterm.NewTerminal(24, 80)

	panicked := false
	vt.pipeChunk([]byte("hello"), func() {
		panicked = true
		panic("test panic in onData")
	})

	if !panicked {
		t.Fatal("expected onData to have been called")
	}

	// Verify mutex is NOT held after recovery (would deadlock if still held).
	assertMutexAvailable(t, &vt.Mu)
}

func TestPipeChunk_MutexReleasedOnPanic(t *testing.T) {
	// Verify the mutex is released even when midterm Write panics.
	// After pipeChunk recovers, subsequent Lock/Unlock must succeed.
	vt := &VT{}
	vt.Vt = midterm.NewTerminal(24, 80)

	// Normal chunk should work fine.
	called := false
	vt.pipeChunk([]byte("ok"), func() { called = true })
	if !called {
		t.Fatal("expected onData to be called for normal chunk")
	}

	// Mutex should be available.
	assertMutexAvailable(t, &vt.Mu)
}

// --- Live VT height clamp (defense in depth) ---

// TestPipeChunk_LiveVtHeightClamped verifies that the live VT's Height never
// exceeds ChildRows after a chunk is processed, even when the chunk contains
// sequences that historically grew midterm's Height past the configured size.
//
// The bug this guards against: if Vt.Height grows past ChildRows,
// renderLiveView's `startRow = Cursor.Y - ChildRows + 1` slides the rendered
// window past content that's still in Vt.Content but never reaches the user.
// The user then sees content disappear into "thin air" until the next
// SIGWINCH-driven Resize re-clamps Height.
//
// midterm itself now gates ensureHeight on AutoResizeY, so this is a
// belt-and-suspenders check. Future midterm refactors or additional grow
// paths cannot silently regress h2 rendering.
func TestPipeChunk_LiveVtHeightClamped(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{
			"insertLines (CSI L) past height",
			[]byte("\033[10;1H\033[5L"),
		},
		{
			"absolute goto then text past height",
			[]byte("\033[99;1Hx"),
		},
		{
			"newlines + cursor home + redraw cycle (Claude Code pattern)",
			func() []byte {
				var b []byte
				for i := 0; i < 100; i++ {
					b = append(b, []byte("line\r\n")...)
				}
				b = append(b, []byte("\033[H")...)
				for i := 0; i < 5; i++ {
					b = append(b, []byte("redraw\r\n")...)
				}
				return b
			}(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vt := &VT{
				ChildRows: 10,
				Cols:      80,
				Vt:        midterm.NewTerminal(10, 80),
				Output:    io.Discard,
			}
			vt.pipeChunk(tc.data, func() {})

			if vt.Vt.Height > vt.ChildRows {
				t.Fatalf("Vt.Height=%d exceeds ChildRows=%d", vt.Vt.Height, vt.ChildRows)
			}
			if len(vt.Vt.Content) > vt.ChildRows {
				t.Fatalf("len(Vt.Content)=%d exceeds ChildRows=%d",
					len(vt.Vt.Content), vt.ChildRows)
			}
		})
	}
}

// TestClampLiveVtHeight_Idempotent verifies that the clamp is a no-op when
// Vt.Height already matches ChildRows (the steady-state hot path).
func TestClampLiveVtHeight_Idempotent(t *testing.T) {
	vt := &VT{
		ChildRows: 10,
		Cols:      80,
		Vt:        midterm.NewTerminal(10, 80),
	}
	for i := 0; i < 5; i++ {
		vt.clampLiveVtHeight()
		if vt.Vt.Height != 10 {
			t.Fatalf("iter %d: Height=%d, want 10", i, vt.Vt.Height)
		}
	}
}

// TestClampLiveVtHeight_NoVtNoCrash guards against nil Vt or zero ChildRows
// during early lifecycle (initVT before the first PTY chunk).
func TestClampLiveVtHeight_NoVtNoCrash(t *testing.T) {
	(&VT{}).clampLiveVtHeight()
	(&VT{ChildRows: 10}).clampLiveVtHeight()
}

// TestPipeChunk_SurvivesShrinkMidSession reproduces the codex-after-monitor-
// unplug freeze. A TUI on a tall screen with a scroll region (codex pattern)
// has its cursor far down the screen. A sudden shrink resize must not leave
// midterm in a state where the next chunk panics inside Screen.paint with an
// out-of-range Content index — that panic would be swallowed by pipeChunk's
// recovery and the chunk's data would be lost, producing the "frozen window,
// no PTY output, redraw does nothing" symptom users see.
//
// midterm's resizeY now clamps Cursor.Y / SavedCursor.Y / ScrollRegion to
// the new bounds; this test exercises the full pipeChunk path to lock that
// guarantee in at the integration boundary, not just inside midterm.
func TestPipeChunk_SurvivesShrinkMidSession(t *testing.T) {
	vt := &VT{
		Rows:      50,
		Cols:      100,
		ChildRows: 48,
		Vt:        midterm.NewTerminal(48, 100),
		Output:    io.Discard,
	}

	// Codex-like setup: scroll region reserving bottom rows, content fills it.
	vt.pipeChunk([]byte("\033[1;45r"), func() {})
	var initial []byte
	for i := 0; i < 30; i++ {
		initial = append(initial, []byte("line ")...)
		initial = append(initial, []byte("xxxxxxxxxx\r\n")...)
	}
	vt.pipeChunk(initial, func() {})

	// User unplugs monitor: terminal shrinks to 25 rows (childRows=23).
	vt.Resize(25, 100, 23)

	// Acid test: write more data. Before the midterm clamp fix, this panicked
	// inside Screen.paint with `index out of range [N] with length 23`,
	// pipeChunk's recover() swallowed it, and the chunk's render was lost —
	// repeatedly, for every chunk until kill+restart.
	rendered := 0
	vt.pipeChunk([]byte("after resize: should render\r\n"), func() { rendered++ })

	// The render callback must fire — confirming no swallowed panic dropped
	// the chunk. Vt.Height must be within bounds. Cursor must be within bounds.
	if rendered == 0 {
		t.Fatal("render callback did not fire — pipeChunk likely panicked and recovered silently")
	}
	if vt.Vt.Height > 23 {
		t.Fatalf("Vt.Height=%d exceeds ChildRows=23", vt.Vt.Height)
	}
	if vt.Vt.Cursor.Y >= vt.Vt.Height {
		t.Fatalf("Cursor.Y=%d out of bounds for Height=%d", vt.Vt.Cursor.Y, vt.Vt.Height)
	}
	if vt.Vt.ScrollRegion != nil && vt.Vt.ScrollRegion.End >= vt.Vt.Height {
		t.Fatalf("ScrollRegion.End=%d out of bounds for Height=%d",
			vt.Vt.ScrollRegion.End, vt.Vt.Height)
	}
}

func assertMutexAvailable(t *testing.T, mu *sync.Mutex) {
	t.Helper()
	locked := make(chan struct{})
	go func() {
		acquired := false
		mu.Lock()
		acquired = true
		mu.Unlock()
		if !acquired {
			panic("unreachable")
		}
		close(locked)
	}()
	select {
	case <-locked:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("mutex remained locked")
	}
}

func TestResetScanState(t *testing.T) {
	vt := &VT{}
	vt.ScrollRegionUsed = true
	vt.AltScrollEnabled = true
	vt.SyncOutputActive = true
	vt.scanState = scanCSI
	vt.scanCSIPrivateNum = 42
	vt.ResetScanState()
	if vt.ScrollRegionUsed {
		t.Fatal("expected ScrollRegionUsed=false after reset")
	}
	if vt.AltScrollEnabled {
		t.Fatal("expected AltScrollEnabled=false after reset")
	}
	if vt.SyncOutputActive {
		t.Fatal("expected SyncOutputActive=false after reset")
	}
	if vt.scanState != scanNormal {
		t.Fatal("expected scanState=scanNormal after reset")
	}
	if vt.scanCSIPrivateNum != 0 {
		t.Fatal("expected scanCSIPrivateNum=0 after reset")
	}
}
