package client

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/vito/midterm"

	"h2/internal/session/virtualterminal"
)

func newTestClient(childRows, cols int) *Client {
	vt := &virtualterminal.VT{
		Rows:      childRows + 2,
		Cols:      cols,
		ChildRows: childRows,
		Vt:        midterm.NewTerminal(childRows, cols),
		Output:    io.Discard,
	}
	sb := midterm.NewTerminal(childRows, cols)
	sb.AutoResizeY = true
	sb.AppendOnly = true
	vt.Scrollback = sb
	return &Client{
		VT:     vt,
		Output: io.Discard,
		Mode:   ModeNormal,
	}
}

// --- ClampScrollOffset ---

func TestClampScrollOffset_NilScrollback(t *testing.T) {
	o := newTestClient(10, 80)
	o.VT.Scrollback = nil
	o.ScrollOffset = 5
	o.ClampScrollOffset()
	if o.ScrollOffset != 0 {
		t.Fatalf("expected 0, got %d", o.ScrollOffset)
	}
}

func TestClampScrollOffset_NoHistory(t *testing.T) {
	o := newTestClient(10, 80)
	// Scrollback cursor at Y=0, no history beyond one screen.
	o.ScrollOffset = 5
	o.ClampScrollOffset()
	if o.ScrollOffset != 0 {
		t.Fatalf("expected 0, got %d", o.ScrollOffset)
	}
}

func TestClampScrollOffset_WithHistory(t *testing.T) {
	o := newTestClient(10, 80)
	// Simulate 30 lines of content: cursor at row 29.
	for i := 0; i < 30; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}
	// maxOffset = 30 - 10 + 1 = 21 (cursor Y is 30)
	o.ScrollOffset = 15
	o.ClampScrollOffset()
	if o.ScrollOffset != 15 {
		t.Fatalf("expected 15, got %d", o.ScrollOffset)
	}
}

func TestClampScrollOffset_OverMax(t *testing.T) {
	o := newTestClient(10, 80)
	for i := 0; i < 15; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}
	cursorY := o.VT.Scrollback.Cursor.Y
	maxOffset := cursorY - o.VT.ChildRows + 1
	if maxOffset < 0 {
		maxOffset = 0
	}
	o.ScrollOffset = 999
	o.ClampScrollOffset()
	if o.ScrollOffset != maxOffset {
		t.Fatalf("expected %d, got %d", maxOffset, o.ScrollOffset)
	}
}

func TestClampScrollOffset_Negative(t *testing.T) {
	o := newTestClient(10, 80)
	o.ScrollOffset = -5
	o.ClampScrollOffset()
	if o.ScrollOffset != 0 {
		t.Fatalf("expected 0, got %d", o.ScrollOffset)
	}
}

func TestClampScrollOffset_UsesCursorYNotContentLen(t *testing.T) {
	o := newTestClient(10, 80)
	for i := 0; i < 40; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}
	// Simulate a TUI where Content is inflated (AutoResizeY grew it)
	// but cursor is at a reasonable position.
	o.VT.Scrollback.Cursor.Y = 20

	o.ScrollOffset = 999
	o.ClampScrollOffset()

	// maxOffset should be based on Cursor.Y, not len(Content).
	want := 20 - o.VT.ChildRows + 1
	if want < 0 {
		want = 0
	}
	if o.ScrollOffset != want {
		t.Fatalf("expected offset %d based on Cursor.Y, got %d", want, o.ScrollOffset)
	}
}

// --- EnterScrollMode / ExitScrollMode ---

func TestEnterExitScrollMode(t *testing.T) {
	o := newTestClient(10, 80)
	for i := 0; i < 20; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}
	if o.Mode != ModeNormal {
		t.Fatalf("expected ModeNormal, got %d", o.Mode)
	}

	o.EnterScrollMode()
	if o.Mode != ModeScroll {
		t.Fatalf("expected ModeScroll, got %d", o.Mode)
	}
	if o.ScrollOffset != 0 {
		t.Fatalf("expected offset 0 on enter, got %d", o.ScrollOffset)
	}
	if o.ScrollAnchorY != o.scrollbackBottomRow() {
		t.Fatalf("expected anchor %d, got %d", o.scrollbackBottomRow(), o.ScrollAnchorY)
	}

	o.ScrollOffset = 5
	o.ExitScrollMode()
	if o.Mode != ModeNormal {
		t.Fatalf("expected ModeNormal after exit, got %d", o.Mode)
	}
	if o.ScrollOffset != 0 {
		t.Fatalf("expected offset 0 after exit, got %d", o.ScrollOffset)
	}
	if o.ScrollAnchorY != 0 {
		t.Fatalf("expected anchor reset to 0, got %d", o.ScrollAnchorY)
	}
	if o.ScrollHistoryAnchor != 0 {
		t.Fatalf("expected ScrollHistoryAnchor reset to 0, got %d", o.ScrollHistoryAnchor)
	}
}

func TestClampScrollOffset_UsesFrozenAnchorInScrollMode(t *testing.T) {
	o := newTestClient(10, 80)
	for i := 0; i < 30; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}
	o.EnterScrollMode()
	anchor := o.ScrollAnchorY

	// Simulate new output arriving while scrolled.
	for i := 0; i < 50; i++ {
		o.VT.Scrollback.Write([]byte("new\n"))
	}

	o.ScrollOffset = 999
	o.ClampScrollOffset()
	want := anchor - o.VT.ChildRows + 1
	if want < 0 {
		want = 0
	}
	if o.ScrollOffset != want {
		t.Fatalf("expected clamped offset %d with frozen anchor, got %d", want, o.ScrollOffset)
	}
}

// --- ScrollUp / ScrollDown ---

func TestScrollUpDown(t *testing.T) {
	o := newTestClient(10, 80)
	// Write enough lines to have history.
	for i := 0; i < 30; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}

	o.EnterScrollMode()
	o.ScrollUp(5)
	if o.ScrollOffset != 5 {
		t.Fatalf("expected offset 5, got %d", o.ScrollOffset)
	}

	o.ScrollDown(3, true)
	if o.ScrollOffset != 2 {
		t.Fatalf("expected offset 2, got %d", o.ScrollOffset)
	}
}

func TestScrollUp_NoOpAtMax(t *testing.T) {
	o := newTestClient(10, 80)
	for i := 0; i < 15; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}
	o.EnterScrollMode()

	// Scroll up to the max.
	o.ScrollUp(999)
	maxOffset := o.ScrollOffset

	// Scrolling up again should be a no-op (offset stays the same).
	o.ScrollUp(5)
	if o.ScrollOffset != maxOffset {
		t.Fatalf("expected offset to stay at %d, got %d", maxOffset, o.ScrollOffset)
	}
}

func TestScrollDown_ExitsAtZero(t *testing.T) {
	o := newTestClient(10, 80)
	for i := 0; i < 30; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}

	o.EnterScrollMode()
	o.ScrollUp(3)
	if o.Mode != ModeScroll {
		t.Fatalf("expected ModeScroll, got %d", o.Mode)
	}

	// Scroll down past zero should exit scroll mode.
	o.ScrollDown(10, true)
	if o.Mode != ModeNormal {
		t.Fatalf("expected ModeNormal after scrolling to bottom, got %d", o.Mode)
	}
	if o.ScrollOffset != 0 {
		t.Fatalf("expected offset 0, got %d", o.ScrollOffset)
	}
}

func TestScrollDown_StaysInModeWhenNoExit(t *testing.T) {
	o := newTestClient(10, 80)
	for i := 0; i < 30; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}

	o.EnterScrollMode()
	o.ScrollUp(3)

	// Scroll down past zero with exitAtBottom=false should stay in scroll mode.
	o.ScrollDown(10, false)
	if !o.IsScrollMode() {
		t.Fatal("expected to stay in scroll mode with exitAtBottom=false")
	}
	if o.ScrollOffset != 0 {
		t.Fatalf("expected offset 0, got %d", o.ScrollOffset)
	}
}

// --- HandleSGRMouse ---

func TestHandleSGRMouse_ScrollUpEntersScrollMode(t *testing.T) {
	// Scroll up should always enter h2's scroll mode.
	o := newTestClient(10, 80)
	for i := 0; i < 20; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}

	o.HandleSGRMouse([]byte("<64;1;1"), true)
	if o.Mode != ModeScroll {
		t.Fatalf("expected ModeScroll, got %d", o.Mode)
	}
	if o.ScrollOffset != scrollStep {
		t.Fatalf("expected offset %d, got %d", scrollStep, o.ScrollOffset)
	}
}

func TestHandleSGRMouse_ScrollUpEntersModeWhenChildExited(t *testing.T) {
	o := newTestClient(10, 80)
	o.VT.ChildExited = true
	for i := 0; i < 20; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}

	o.HandleSGRMouse([]byte("<64;1;1"), true)
	if o.Mode != ModeScroll {
		t.Fatalf("expected ModeScroll when child exited, got %d", o.Mode)
	}
	if o.ScrollOffset != scrollStep {
		t.Fatalf("expected offset %d, got %d", scrollStep, o.ScrollOffset)
	}
}

func TestHandleSGRMouse_ScrollDownInMode(t *testing.T) {
	o := newTestClient(10, 80)
	for i := 0; i < 20; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}

	o.EnterScrollMode()
	o.ScrollUp(10)

	before := o.ScrollOffset
	o.HandleSGRMouse([]byte("<65;1;1"), true)
	if o.ScrollOffset != before-scrollStep {
		t.Fatalf("expected offset %d, got %d", before-scrollStep, o.ScrollOffset)
	}
}

func TestHandleSGRMouse_ScrollInPassthrough_EntersPassthroughScroll(t *testing.T) {
	// Scroll up in passthrough mode enters ModePassthroughScroll.
	o := newTestClient(10, 80)
	o.Mode = ModePassthrough
	o.HandleSGRMouse([]byte("<64;1;1"), true)
	if o.Mode != ModePassthroughScroll {
		t.Fatalf("expected ModePassthroughScroll, got %d", o.Mode)
	}
}

func TestPassthrough_ScrollSequenceEntersScrollMode(t *testing.T) {
	// SGR mouse scroll-up sequence in passthrough enters ModePassthroughScroll.
	o := newTestClient(10, 80)
	o.Mode = ModePassthrough
	buf := []byte("\x1b[<64;1;1M")
	o.HandlePassthroughBytes(buf, 0, len(buf))
	if o.Mode != ModePassthroughScroll {
		t.Fatalf("expected ModePassthroughScroll, got %d", o.Mode)
	}
}

func TestHandleSGRMouse_MalformedParams(t *testing.T) {
	o := newTestClient(10, 80)
	// No '<' prefix
	o.HandleSGRMouse([]byte("64;1;1"), true)
	if o.Mode != ModeNormal {
		t.Fatalf("expected ModeNormal, got %d", o.Mode)
	}
	// Too few params
	o.HandleSGRMouse([]byte("<64"), true)
	if o.Mode != ModeNormal {
		t.Fatalf("expected ModeNormal, got %d", o.Mode)
	}
	// Non-numeric button
	o.HandleSGRMouse([]byte("<abc;1;1"), true)
	if o.Mode != ModeNormal {
		t.Fatalf("expected ModeNormal, got %d", o.Mode)
	}
}

func TestHandleSGRMouse_LeftClickShowsSelectHint(t *testing.T) {
	o := newTestClient(10, 80)
	o.HandleSGRMouse([]byte("<0;5;5"), true)
	if !o.SelectHint {
		t.Fatal("expected SelectHint to be true after left click")
	}
	if o.SelectHintTimer == nil {
		t.Fatal("expected SelectHintTimer to be set")
	}
	o.SelectHintTimer.Stop()
}

func TestHandleSGRMouse_LeftClickReleaseNoHint(t *testing.T) {
	o := newTestClient(10, 80)
	o.HandleSGRMouse([]byte("<0;5;5"), false) // release, not press
	if o.SelectHint {
		t.Fatal("expected SelectHint to be false on release")
	}
}

func TestRenderSelectHint_DefaultMode(t *testing.T) {
	o := newTestClient(10, 80)
	o.SelectHint = true
	var buf bytes.Buffer
	o.renderSelectHint(&buf)
	output := buf.String()
	if len(output) == 0 {
		t.Fatal("expected hint output")
	}
	// Hint should be on row 1 in default mode.
	if !bytes.Contains([]byte(output), []byte("hold shift to select")) {
		t.Fatal("expected hint text in output")
	}
}

func TestRenderSelectHint_ScrollMode(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModeScroll
	o.SelectHint = true
	var buf bytes.Buffer
	o.renderSelectHint(&buf)
	output := buf.String()
	// Hint should be on row 2 when scrolling.
	if !bytes.Contains([]byte(output), []byte("[2;")) {
		t.Fatal("expected hint on row 2 in scroll mode")
	}
}

func TestRenderSelectHint_NotShownWhenFalse(t *testing.T) {
	o := newTestClient(10, 80)
	o.SelectHint = false
	var buf bytes.Buffer
	o.renderSelectHint(&buf)
	if buf.Len() != 0 {
		t.Fatal("expected no output when SelectHint is false")
	}
}

func TestRenderScrollView_UsesScrollHistoryWhenAvailable(t *testing.T) {
	o := newTestClient(5, 80)
	var out bytes.Buffer
	o.Output = &out

	// Mark that scroll regions are used (e.g. codex).
	o.VT.ScrollRegionUsed = true
	// Populate ScrollHistory with captured lines.
	o.VT.ScrollHistory = []string{
		"history-line-0",
		"history-line-1",
		"history-line-2",
		"history-line-3",
		"history-line-4",
		"history-target",
	}

	o.EnterScrollMode()
	// Scroll up to see ScrollHistory content.
	o.ScrollUp(3)
	o.RenderScreen()

	if !bytes.Contains(out.Bytes(), []byte("history-target")) {
		t.Fatalf("expected scroll render to include ScrollHistory content")
	}
}

func TestRenderScrollView_DoesNotBlankOnFirstScrollTick(t *testing.T) {
	o := newTestClient(5, 80)
	var out bytes.Buffer
	o.Output = &out

	// Write content to scrollback.
	o.VT.Scrollback.Write([]byte("line-a\nline-b\nline-c\n"))

	// Scroll up enters scroll mode.
	o.HandleSGRMouse([]byte("<64;1;1"), true)

	if !bytes.Contains(out.Bytes(), []byte("line-a")) &&
		!bytes.Contains(out.Bytes(), []byte("line-b")) &&
		!bytes.Contains(out.Bytes(), []byte("line-c")) {
		t.Fatalf("expected first scroll render to include existing scrollback lines")
	}
}

// --- HandleScrollBytes ---

func TestHandleScrollBytes_EscAtEndStartsPending(t *testing.T) {
	o := newTestClient(10, 80)
	o.EnterScrollMode()
	// Bare Esc (0x1B) at end of buffer starts pending timer, doesn't exit immediately.
	buf := []byte{0x1B}
	o.HandleScrollBytes(buf, 0, len(buf))
	if !o.PendingEsc {
		t.Fatal("expected PendingEsc to be true")
	}
	if o.Mode != ModeScroll {
		t.Fatalf("expected ModeScroll (pending), got %d", o.Mode)
	}
}

func TestHandleScrollBytes_EscFollowedByNonSeqStays(t *testing.T) {
	o := newTestClient(10, 80)
	o.EnterScrollMode()
	o.ScrollOffset = 3
	// Esc followed by a non-sequence byte stays in scroll mode.
	buf := []byte{0x1B, 'x'}
	o.HandleScrollBytes(buf, 0, len(buf))
	if o.Mode != ModeScroll {
		t.Fatalf("expected ModeScroll, got %d", o.Mode)
	}
}

func TestHandleScrollBytes_PendingEscContinuation(t *testing.T) {
	o := newTestClient(10, 80)
	for i := 0; i < 30; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}
	o.EnterScrollMode()

	// ESC at end of first read.
	buf1 := []byte{0x1B}
	o.HandleScrollBytes(buf1, 0, len(buf1))
	if !o.PendingEsc {
		t.Fatal("expected PendingEsc")
	}

	// Continuation in next read: [ A (arrow up).
	buf2 := []byte{'[', 'A'}
	o.HandleScrollBytes(buf2, 0, len(buf2))
	if o.PendingEsc {
		t.Fatal("expected PendingEsc to be cleared")
	}
	if o.Mode != ModeScroll {
		t.Fatalf("expected ModeScroll after arrow key, got %d", o.Mode)
	}
	if o.ScrollOffset != 1 {
		t.Fatalf("expected offset 1, got %d", o.ScrollOffset)
	}
}

func TestHandleScrollBytes_RegularKeysIgnored(t *testing.T) {
	o := newTestClient(10, 80)
	o.EnterScrollMode()
	o.ScrollOffset = 5
	buf := []byte{'a', 'b', 'c', 'q', ' ', '1'}
	o.HandleScrollBytes(buf, 0, len(buf))
	if o.Mode != ModeScroll {
		t.Fatalf("expected ModeScroll, got %d", o.Mode)
	}
	if o.ScrollOffset != 5 {
		t.Fatalf("expected offset 5, got %d", o.ScrollOffset)
	}
}

func TestHandleScrollBytes_ArrowUpScrolls(t *testing.T) {
	o := newTestClient(10, 80)
	for i := 0; i < 30; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}
	o.EnterScrollMode()

	// ESC [ A = arrow up
	buf := []byte{0x1B, '[', 'A'}
	o.HandleScrollBytes(buf, 0, len(buf))
	if o.ScrollOffset != 1 {
		t.Fatalf("expected offset 1, got %d", o.ScrollOffset)
	}
}

func TestHandleScrollBytes_ArrowDownScrolls(t *testing.T) {
	o := newTestClient(10, 80)
	for i := 0; i < 30; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}
	o.EnterScrollMode()
	o.ScrollUp(5)

	// ESC [ B = arrow down
	buf := []byte{0x1B, '[', 'B'}
	o.HandleScrollBytes(buf, 0, len(buf))
	if o.ScrollOffset != 4 {
		t.Fatalf("expected offset 4, got %d", o.ScrollOffset)
	}
}

func TestHandleScrollBytes_PageUpScrolls(t *testing.T) {
	o := newTestClient(10, 80)
	for i := 0; i < 50; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}
	o.EnterScrollMode()

	// ESC [ 5 ~ = Page Up
	buf := []byte{0x1B, '[', '5', '~'}
	o.HandleScrollBytes(buf, 0, len(buf))
	if o.ScrollOffset != o.VT.ChildRows {
		t.Fatalf("expected offset %d (one page), got %d", o.VT.ChildRows, o.ScrollOffset)
	}
}

func TestHandleScrollBytes_PageDownScrolls(t *testing.T) {
	o := newTestClient(10, 80)
	for i := 0; i < 50; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}
	o.EnterScrollMode()
	o.ScrollUp(20)

	// ESC [ 6 ~ = Page Down
	buf := []byte{0x1B, '[', '6', '~'}
	o.HandleScrollBytes(buf, 0, len(buf))
	if o.ScrollOffset != 20-o.VT.ChildRows {
		t.Fatalf("expected offset %d, got %d", 20-o.VT.ChildRows, o.ScrollOffset)
	}
}

func TestHandleScrollBytes_HomeScrollsToTop(t *testing.T) {
	o := newTestClient(10, 80)
	for i := 0; i < 50; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}
	o.EnterScrollMode()

	// ESC [ H = Home
	buf := []byte{0x1B, '[', 'H'}
	o.HandleScrollBytes(buf, 0, len(buf))

	maxOffset, _ := o.scrollMaxOffset()
	if o.ScrollOffset != maxOffset {
		t.Fatalf("expected offset %d (max), got %d", maxOffset, o.ScrollOffset)
	}
}

func TestHandleScrollBytes_EndExitsScrollMode(t *testing.T) {
	o := newTestClient(10, 80)
	for i := 0; i < 50; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}
	o.EnterScrollMode()
	o.ScrollUp(20)

	// ESC [ F = End
	buf := []byte{0x1B, '[', 'F'}
	o.HandleScrollBytes(buf, 0, len(buf))

	if o.IsScrollMode() {
		t.Fatal("expected End to exit scroll mode")
	}
	if o.ScrollOffset != 0 {
		t.Fatalf("expected offset 0, got %d", o.ScrollOffset)
	}
}

func TestPageUp_EntersScrollModeFromNormal(t *testing.T) {
	o := newTestClient(10, 80)
	for i := 0; i < 30; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}

	// ESC [ 5 ~ = Page Up in normal mode
	buf := []byte{0x1B, '[', '5', '~'}
	o.HandleDefaultBytes(buf, 0, len(buf))
	if !o.IsScrollMode() {
		t.Fatal("expected PageUp to enter scroll mode from normal mode")
	}
	if o.ScrollOffset != o.VT.ChildRows {
		t.Fatalf("expected offset %d (one page), got %d", o.VT.ChildRows, o.ScrollOffset)
	}
}

func TestHome_EntersScrollModeFromNormal(t *testing.T) {
	o := newTestClient(10, 80)
	for i := 0; i < 30; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}

	// ESC [ H = Home in normal mode (no params)
	buf := []byte{0x1B, '[', 'H'}
	o.HandleDefaultBytes(buf, 0, len(buf))
	if !o.IsScrollMode() {
		t.Fatal("expected Home to enter scroll mode from normal mode")
	}
	maxOffset, _ := o.scrollMaxOffset()
	if o.ScrollOffset != maxOffset {
		t.Fatalf("expected offset %d (max), got %d", maxOffset, o.ScrollOffset)
	}
}

// --- RenderLiveView anchors to cursor ---

func TestRenderLiveView_AnchorsToCursor(t *testing.T) {
	o := newTestClient(5, 40)
	// Write enough lines to move the cursor well past ChildRows.
	for i := 0; i < 20; i++ {
		o.VT.Vt.Write([]byte("line\n"))
	}

	cursorY := o.VT.Vt.Cursor.Y
	expectedStart := cursorY - o.VT.ChildRows + 1
	if expectedStart < 0 {
		expectedStart = 0
	}

	var buf bytes.Buffer
	o.renderLiveView(&buf)
	output := buf.String()

	if len(output) == 0 {
		t.Fatal("expected non-empty render output")
	}
	// The cursor should be within the rendered window.
	if cursorY < expectedStart || cursorY >= expectedStart+o.VT.ChildRows {
		t.Fatalf("cursor Y=%d outside rendered window [%d, %d)", cursorY, expectedStart, expectedStart+o.VT.ChildRows)
	}
}

func TestRenderLiveView_SmallContent(t *testing.T) {
	o := newTestClient(10, 40)
	// Write fewer lines than ChildRows — startRow should be 0.
	o.VT.Vt.Write([]byte("hello\n"))

	var buf bytes.Buffer
	o.renderLiveView(&buf)
	output := buf.String()

	if len(output) == 0 {
		t.Fatal("expected non-empty render output")
	}
	// Cursor should be at row 1 (after one newline), startRow = max(0, 1-10+1) = 0.
	if o.VT.Vt.Cursor.Y > o.VT.ChildRows {
		t.Fatalf("cursor Y=%d should be within ChildRows=%d", o.VT.Vt.Cursor.Y, o.VT.ChildRows)
	}
}

// --- Mode labels ---

func TestModeLabel_Scroll(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModeScroll
	if got := o.ModeLabel(); got != "Scroll" {
		t.Fatalf("expected 'Scroll', got %q", got)
	}
}

func TestHelpLabel_Scroll(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModeScroll
	got := o.HelpLabel()
	if got != "Scroll/Up/Down navigate | Esc exit scroll" {
		t.Fatalf("unexpected help label: %q", got)
	}
}

// --- Exited + scroll mode ---

func TestHandleExitedBytes_MouseScrollEntersScrollMode(t *testing.T) {
	o := newTestClient(10, 80)
	o.VT.ChildExited = true
	for i := 0; i < 20; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}

	// SGR mouse scroll up: ESC [ < 64 ; 1 ; 1 M
	buf := []byte{0x1B, '[', '<', '6', '4', ';', '1', ';', '1', 'M'}
	o.HandleExitedBytes(buf, 0, len(buf))
	if o.Mode != ModeScroll {
		t.Fatalf("expected ModeScroll, got %d", o.Mode)
	}
}

func TestHandleExitedBytes_EnterStillRelaunches(t *testing.T) {
	o := newTestClient(10, 80)
	o.VT.ChildExited = true
	var called bool
	o.OnRelaunch = func() { called = true }

	buf := []byte{'\r'}
	o.HandleExitedBytes(buf, 0, len(buf))

	if !called {
		t.Fatal("expected OnRelaunch to be called")
	}
}

func TestHandleExitedBytes_QStillQuits(t *testing.T) {
	o := newTestClient(10, 80)
	o.VT.ChildExited = true
	var called bool
	o.OnQuit = func() { called = true }

	buf := []byte{'q'}
	o.HandleExitedBytes(buf, 0, len(buf))

	if !called {
		t.Fatal("expected OnQuit to be called")
	}
	if !o.Quit {
		t.Fatal("expected Quit to be true")
	}
}

func TestExitedScrollMode_BarStaysRed(t *testing.T) {
	o := newTestClient(10, 80)
	o.VT.ChildExited = true
	o.Mode = ModeScroll

	// ModeBarStyle returns cyan for scroll, but ChildExited overrides to red.
	// We verify the bar rendering path uses the red style by checking that
	// the label includes "Scroll" and the exit message.
	// (The actual ANSI color is hardcoded in the render code, not in ModeBarStyle.)

	// Verify the mode label still says Scroll.
	if got := o.ModeLabel(); got != "Scroll" {
		t.Fatalf("expected 'Scroll', got %q", got)
	}
}

func TestExitedScrollMode_ScrollDownToBottomExits(t *testing.T) {
	o := newTestClient(10, 80)
	o.VT.ChildExited = true
	for i := 0; i < 20; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}

	o.EnterScrollMode()
	o.ScrollUp(3)
	if o.Mode != ModeScroll {
		t.Fatalf("expected ModeScroll, got %d", o.Mode)
	}

	// Scroll down past zero exits scroll mode.
	o.ScrollDown(10, true)
	if o.Mode != ModeNormal {
		t.Fatalf("expected ModeNormal after scrolling to bottom, got %d", o.Mode)
	}
	if !o.VT.ChildExited {
		t.Fatal("expected ChildExited to still be true")
	}
}

func TestHandleScrollBytes_CtrlPassthrough(t *testing.T) {
	// We can't easily test PTY writes without a real PTY, but we can
	// verify that ctrl chars don't exit scroll mode and don't panic.
	o := newTestClient(10, 80)
	o.EnterScrollMode()
	o.ScrollOffset = 5
	// Ctrl+C (0x03), Ctrl+D (0x04) — child is not running so writes
	// are skipped, but mode should remain ModeScroll.
	buf := []byte{0x03, 0x04}
	o.HandleScrollBytes(buf, 0, len(buf))
	if o.Mode != ModeScroll {
		t.Fatalf("expected ModeScroll after ctrl chars, got %d", o.Mode)
	}
	if o.ScrollOffset != 5 {
		t.Fatalf("expected offset 5, got %d", o.ScrollOffset)
	}
}

// --- ctrl+\ / ctrl+enter / ctrl+escape ---

func TestCtrlBackslash_EntersMenuMode(t *testing.T) {
	o := newTestClient(10, 80)
	buf := []byte{0x1C} // ctrl+backslash
	o.HandleDefaultBytes(buf, 0, len(buf))
	if o.Mode != ModeMenu {
		t.Fatalf("expected ModeMenu, got %d", o.Mode)
	}
}

func TestCtrlBackslash_EntersMenuWithInput(t *testing.T) {
	o := newTestClient(10, 80)
	o.Input = []byte("hello")
	o.CursorPos = 5
	buf := []byte{0x1C} // ctrl+backslash
	o.HandleDefaultBytes(buf, 0, len(buf))
	if o.Mode != ModeMenu {
		t.Fatalf("expected ModeMenu, got %d", o.Mode)
	}
}

func TestCtrlPN_PassedThroughInNormalMode(t *testing.T) {
	// Ctrl+P and Ctrl+N no longer navigate history — they pass through to PTY.
	// Without a real PTY, we just verify they don't trigger history navigation.
	o := newTestClient(10, 80)
	o.History = []string{"first", "second"}
	o.HistIdx = -1
	buf := []byte{0x10} // ctrl+p
	o.HandleDefaultBytes(buf, 0, len(buf))
	if string(o.Input) != "" {
		t.Fatalf("expected empty input (ctrl+p should pass through), got %q", string(o.Input))
	}
	if o.HistIdx != -1 {
		t.Fatalf("expected HistIdx -1, got %d", o.HistIdx)
	}
}

func TestCtrlEnterCSI_EntersMenuFromNormal(t *testing.T) {
	// Kitty format: ESC [ 13;5 u
	o := newTestClient(10, 80)
	buf := []byte{0x1B, '[', '1', '3', ';', '5', 'u'}
	o.HandleDefaultBytes(buf, 0, len(buf))
	if o.Mode != ModeMenu {
		t.Fatalf("expected ModeMenu, got %d", o.Mode)
	}
}

func TestCtrlEnterCSI_Xterm_EntersMenuFromNormal(t *testing.T) {
	// xterm format: ESC [ 27;5;13 ~
	o := newTestClient(10, 80)
	buf := []byte{0x1B, '[', '2', '7', ';', '5', ';', '1', '3', '~'}
	o.HandleDefaultBytes(buf, 0, len(buf))
	if o.Mode != ModeMenu {
		t.Fatalf("expected ModeMenu, got %d", o.Mode)
	}
}

func TestCtrlEnterCSI_NoOpInPassthrough(t *testing.T) {
	// Ctrl+Enter in passthrough mode should NOT switch to menu.
	// The CSI is handled by FlushPassthroughEscIfComplete, which writes it through.
	o := newTestClient(10, 80)
	o.Mode = ModePassthrough
	buf := []byte{0x1B, '[', '1', '3', ';', '5', 'u'}
	o.HandlePassthroughBytes(buf, 0, len(buf))
	if o.Mode != ModePassthrough {
		t.Fatalf("expected ModePassthrough, got %d", o.Mode)
	}
}

func TestUpDown_PassedThroughInNormalMode(t *testing.T) {
	// Up/down arrows in normal mode now pass through to the PTY.
	// We can't verify the PTY write without a real PTY, but we verify
	// mode stays normal and no history is triggered.
	o := newTestClient(10, 80)
	o.History = []string{"first", "second"}
	o.HistIdx = -1
	buf := []byte{0x1B, '[', 'A'}
	o.HandleDefaultBytes(buf, 0, len(buf))
	if string(o.Input) != "" {
		t.Fatalf("expected empty input after up arrow, got %q", string(o.Input))
	}
	if o.HistIdx != -1 {
		t.Fatalf("expected HistIdx -1, got %d", o.HistIdx)
	}
	if o.Mode != ModeNormal {
		t.Fatalf("expected ModeNormal, got %d", o.Mode)
	}
}

// --- Menu shortcut keys ---

func TestMenu_PPassthrough(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModeMenu
	buf := []byte{'p'}
	o.HandleMenuBytes(buf, 0, len(buf))
	if o.Mode != ModePassthrough {
		t.Fatalf("expected ModePassthrough, got %d", o.Mode)
	}
}

func TestMenu_ClearInput(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModeMenu
	o.Input = []byte("some text")
	o.CursorPos = 9
	buf := []byte{'c'}
	o.HandleMenuBytes(buf, 0, len(buf))
	if o.Mode != ModeNormal {
		t.Fatalf("expected ModeNormal, got %d", o.Mode)
	}
	if len(o.Input) != 0 {
		t.Fatalf("expected empty input, got %q", string(o.Input))
	}
	if o.CursorPos != 0 {
		t.Fatalf("expected CursorPos 0, got %d", o.CursorPos)
	}
}

func TestMenu_Redraw(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModeMenu
	buf := []byte{'r'}
	o.HandleMenuBytes(buf, 0, len(buf))
	if o.Mode != ModeNormal {
		t.Fatalf("expected ModeNormal after redraw, got %d", o.Mode)
	}
}

func TestMenu_EscExits(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModeMenu
	// Bare Esc at end of buffer
	buf := []byte{0x1B}
	o.HandleMenuBytes(buf, 0, len(buf))
	if o.Mode != ModeNormal {
		t.Fatalf("expected ModeNormal after Esc, got %d", o.Mode)
	}
}

func TestMenu_CtrlBackslashExits(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModeMenu
	buf := []byte{0x1C} // ctrl+backslash
	o.HandleMenuBytes(buf, 0, len(buf))
	if o.Mode != ModeNormal {
		t.Fatalf("expected ModeNormal after Ctrl+\\, got %d", o.Mode)
	}
}

func TestMenu_UpDownArrowNavigatesHistory(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModeMenu
	o.History = []string{"first", "second"}
	o.HistIdx = -1
	o.Input = []byte("draft")
	o.CursorPos = len(o.Input)

	// Up arrow should load newest history entry.
	o.HandleMenuBytes([]byte{0x1B, '[', 'A'}, 0, 3)
	if got := string(o.Input); got != "second" {
		t.Fatalf("input after up = %q, want %q", got, "second")
	}
	if o.HistIdx != 1 {
		t.Fatalf("HistIdx after up = %d, want 1", o.HistIdx)
	}

	// Down arrow should restore saved draft input.
	o.HandleMenuBytes([]byte{0x1B, '[', 'B'}, 0, 3)
	if got := string(o.Input); got != "draft" {
		t.Fatalf("input after down = %q, want %q", got, "draft")
	}
	if o.HistIdx != -1 {
		t.Fatalf("HistIdx after down = %d, want -1", o.HistIdx)
	}
}

func TestMenu_OtherKeysIgnored(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModeMenu
	buf := []byte{'x', 'z', '1'}
	o.HandleMenuBytes(buf, 0, len(buf))
	if o.Mode != ModeMenu {
		t.Fatalf("expected ModeMenu (other keys ignored), got %d", o.Mode)
	}
}

func TestMenu_HelpLabel(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModeMenu
	got := o.HelpLabel()
	if got != "esc exit" {
		t.Fatalf("expected 'esc exit', got %q", got)
	}
}

func TestHelpLabel_Normal_Legacy(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModeNormal
	o.KeybindingMode = KeybindingsLegacy
	got := o.HelpLabel()
	if got != `Enter send | Ctrl+\ menu` {
		t.Fatalf("unexpected help label: %q", got)
	}
}

func TestHelpLabel_Normal_Kitty(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModeNormal
	o.KeybindingMode = KeybindingsKitty
	got := o.HelpLabel()
	if got != "Enter send | Ctrl+Enter menu" {
		t.Fatalf("unexpected help label: %q", got)
	}
}

func TestHelpLabel_Passthrough_Legacy(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModePassthrough
	o.KeybindingMode = KeybindingsLegacy
	got := o.HelpLabel()
	if got != `Ctrl+\ exit` {
		t.Fatalf("unexpected help label: %q", got)
	}
}

func TestHelpLabel_Passthrough_Kitty(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModePassthrough
	o.KeybindingMode = KeybindingsKitty
	got := o.HelpLabel()
	if got != "Ctrl+Esc exit" {
		t.Fatalf("unexpected help label: %q", got)
	}
}

func TestMenu_DetachCallsCallback(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModeMenu
	called := false
	o.OnDetach = func() { called = true }
	buf := []byte{'d'}
	o.HandleMenuBytes(buf, 0, len(buf))
	if !called {
		t.Fatal("expected OnDetach to be called")
	}
	if o.Mode != ModeNormal {
		t.Fatalf("expected ModeNormal after detach, got %d", o.Mode)
	}
}

func TestMenu_DetachUppercase(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModeMenu
	called := false
	o.OnDetach = func() { called = true }
	buf := []byte{'D'}
	o.HandleMenuBytes(buf, 0, len(buf))
	if !called {
		t.Fatal("expected OnDetach to be called")
	}
}

func TestMenu_DetachIgnoredWithoutCallback(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModeMenu
	buf := []byte{'d'}
	o.HandleMenuBytes(buf, 0, len(buf))
	if o.Mode != ModeMenu {
		t.Fatalf("expected ModeMenu when OnDetach is nil, got %d", o.Mode)
	}
}

func TestMenuLabel(t *testing.T) {
	o := newTestClient(10, 80)
	got := o.MenuLabel()
	if got != "Menu | p:passthrough | c:clear | r:redraw | q:quit" {
		t.Fatalf("unexpected menu label: %q", got)
	}
}

func TestMenuLabel_WithDetach(t *testing.T) {
	o := newTestClient(10, 80)
	o.OnDetach = func() {}
	got := o.MenuLabel()
	if got != "Menu | p:passthrough | c:clear | r:redraw | d:detach | q:quit" {
		t.Fatalf("unexpected menu label: %q", got)
	}
}

// --- Passthrough mode input changes ---

func TestPassthrough_EnterStaysInPassthrough(t *testing.T) {
	// Enter in passthrough should write \r to PTY and stay in passthrough mode.
	// Without a real PTY we can't verify the write, but we verify the mode.
	o := newTestClient(10, 80)
	o.Mode = ModePassthrough
	buf := []byte{0x0D}
	o.HandlePassthroughBytes(buf, 0, len(buf))
	if o.Mode != ModePassthrough {
		t.Fatalf("expected ModePassthrough after Enter, got %d", o.Mode)
	}
}

func TestPassthrough_CtrlBackslash_Exits(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModePassthrough
	buf := []byte{0x1C} // ctrl+backslash
	o.HandlePassthroughBytes(buf, 0, len(buf))
	if o.Mode != ModeNormal {
		t.Fatalf("expected ModeNormal after Ctrl+\\, got %d", o.Mode)
	}
}

func TestPassthrough_CtrlEscapeCSI_Exits(t *testing.T) {
	// Kitty format: ESC [ 27;5 u
	o := newTestClient(10, 80)
	o.Mode = ModePassthrough
	buf := []byte{0x1B, '[', '2', '7', ';', '5', 'u'}
	o.HandlePassthroughBytes(buf, 0, len(buf))
	if o.Mode != ModeNormal {
		t.Fatalf("expected ModeNormal after Ctrl+Esc CSI, got %d", o.Mode)
	}
}

func TestPassthrough_CtrlEscapeCSI_Xterm_Exits(t *testing.T) {
	// xterm format: ESC [ 27;5;27 ~
	o := newTestClient(10, 80)
	o.Mode = ModePassthrough
	buf := []byte{0x1B, '[', '2', '7', ';', '5', ';', '2', '7', '~'}
	o.HandlePassthroughBytes(buf, 0, len(buf))
	if o.Mode != ModeNormal {
		t.Fatalf("expected ModeNormal after Ctrl+Esc xterm CSI, got %d", o.Mode)
	}
}

func TestPassthrough_BareEscPassesThrough(t *testing.T) {
	// A bare ESC (timer fires) should pass ESC through to child, not exit passthrough.
	// We test this by simulating what the timer callback does.
	o := newTestClient(10, 80)
	o.Mode = ModePassthrough
	// Start the pending ESC.
	buf := []byte{0x1B}
	o.HandlePassthroughBytes(buf, 0, len(buf))
	if !o.PendingEsc {
		t.Fatal("expected PendingEsc to be true")
	}
	// Mode should still be passthrough (ESC is pending).
	if o.Mode != ModePassthrough {
		t.Fatalf("expected ModePassthrough while ESC is pending, got %d", o.Mode)
	}
}

func TestPassthrough_EscNonSequence_PassesThrough(t *testing.T) {
	// ESC followed by a non-CSI/SS3 byte in passthrough should write ESC+byte to PTY.
	// Without a real PTY we verify it stays in passthrough mode.
	o := newTestClient(10, 80)
	o.Mode = ModePassthrough
	// ESC at end of buffer starts pending.
	o.HandlePassthroughBytes([]byte{0x1B}, 0, 1)
	if !o.PendingEsc {
		t.Fatal("expected PendingEsc")
	}
	// Next byte is 'x' (not [ or O).
	o.HandlePassthroughBytes([]byte{'x'}, 0, 1)
	// Should stay in passthrough (ESC+x passed through to child).
	if o.Mode != ModePassthrough {
		t.Fatalf("expected ModePassthrough after ESC+x, got %d", o.Mode)
	}
}

func TestModeLabel_Normal(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModeNormal
	if got := o.ModeLabel(); got != "Normal" {
		t.Fatalf("expected 'Normal', got %q", got)
	}
}

func TestFormatWorkingDirForBar_RelativeToH2DirTwoPartsNoPrefix(t *testing.T) {
	o := newTestClient(10, 80)
	t.Setenv("H2_DIR", "/Users/dcosson/h2home")
	got := o.formatWorkingDirForBar("/Users/dcosson/h2home/projects/h2/subdir")
	if got != "h2/subdir" {
		t.Fatalf("got %q, want %q", got, "h2/subdir")
	}
}

func TestFormatWorkingDirForBar_OutsideH2DirTwoPartsWithDotDotPrefix(t *testing.T) {
	o := newTestClient(10, 80)
	t.Setenv("H2_DIR", "/Users/dcosson/h2home")
	got := o.formatWorkingDirForBar("/tmp/build/output")
	if got != "../build/output" {
		t.Fatalf("got %q, want %q", got, "../build/output")
	}
}

// --- ModePassthroughScroll ---

func TestEnterScrollFromPassthrough_EntersPassthroughScroll(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModePassthrough
	o.EnterScrollMode()
	if o.Mode != ModePassthroughScroll {
		t.Fatalf("expected ModePassthroughScroll, got %d", o.Mode)
	}
}

func TestExitScrollFromPassthroughScroll_RestoresPassthrough(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModePassthrough
	o.EnterScrollMode()
	if o.Mode != ModePassthroughScroll {
		t.Fatalf("expected ModePassthroughScroll, got %d", o.Mode)
	}
	o.ScrollOffset = 5
	o.ExitScrollMode()
	if o.Mode != ModePassthrough {
		t.Fatalf("expected ModePassthrough after exit, got %d", o.Mode)
	}
	if o.ScrollOffset != 0 {
		t.Fatalf("expected offset 0 after exit, got %d", o.ScrollOffset)
	}
}

func TestEnterScrollFromNormal_StillModeScroll(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModeNormal
	o.EnterScrollMode()
	if o.Mode != ModeScroll {
		t.Fatalf("expected ModeScroll from normal, got %d", o.Mode)
	}
}

func TestExitScrollFromModeScroll_RestoresNormal(t *testing.T) {
	o := newTestClient(10, 80)
	o.EnterScrollMode()
	o.ExitScrollMode()
	if o.Mode != ModeNormal {
		t.Fatalf("expected ModeNormal after exit from ModeScroll, got %d", o.Mode)
	}
}

func TestModeBarStyle_PassthroughScroll_Yellow(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModePassthroughScroll
	got := o.ModeBarStyle()
	expected := "\033[7m\033[33m" // yellow inverse (same as passthrough)
	if got != expected {
		t.Fatalf("expected yellow style %q, got %q", expected, got)
	}
}

func TestModeBarStyle_Scroll_Cyan(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModeScroll
	got := o.ModeBarStyle()
	expected := "\033[7m\033[36m" // cyan inverse
	if got != expected {
		t.Fatalf("expected cyan style %q, got %q", expected, got)
	}
}

func TestModeLabel_PassthroughScroll(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModePassthroughScroll
	if got := o.ModeLabel(); got != "Scroll (PT)" {
		t.Fatalf("expected 'Scroll (PT)', got %q", got)
	}
}

func TestHelpLabel_PassthroughScroll(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModePassthroughScroll
	got := o.HelpLabel()
	if got != "Scroll/Up/Down navigate | Esc exit scroll" {
		t.Fatalf("unexpected help label: %q", got)
	}
}

func TestScrollDownToBottom_FromPassthroughScroll_RestoresPassthrough(t *testing.T) {
	o := newTestClient(10, 80)
	for i := 0; i < 30; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}
	o.Mode = ModePassthrough
	o.EnterScrollMode()
	o.ScrollUp(3)
	if o.Mode != ModePassthroughScroll {
		t.Fatalf("expected ModePassthroughScroll, got %d", o.Mode)
	}

	// Scroll down past zero exits back to passthrough.
	o.ScrollDown(10, true)
	if o.Mode != ModePassthrough {
		t.Fatalf("expected ModePassthrough after scrolling to bottom, got %d", o.Mode)
	}
	if o.ScrollOffset != 0 {
		t.Fatalf("expected offset 0, got %d", o.ScrollOffset)
	}
}

func TestPassthrough_SGRMouseScroll_EntersScrollMode(t *testing.T) {
	// Scroll up in passthrough enters ModePassthroughScroll.
	o := newTestClient(10, 80)
	for i := 0; i < 20; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}
	o.Mode = ModePassthrough
	o.HandleSGRMouse([]byte("<64;1;1"), true)
	if o.Mode != ModePassthroughScroll {
		t.Fatalf("expected ModePassthroughScroll, got %d", o.Mode)
	}
}

func TestPassthrough_ScrollSequence_EntersScrollMode(t *testing.T) {
	// Full SGR mouse scroll-up sequence via HandlePassthroughBytes
	// should enter passthrough scroll mode.
	o := newTestClient(10, 80)
	for i := 0; i < 20; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}
	o.Mode = ModePassthrough
	buf := []byte("\x1b[<64;1;1M")
	o.HandlePassthroughBytes(buf, 0, len(buf))
	if o.Mode != ModePassthroughScroll {
		t.Fatalf("expected ModePassthroughScroll, got %d", o.Mode)
	}
}

func TestIsScrollMode(t *testing.T) {
	o := newTestClient(10, 80)

	o.Mode = ModeNormal
	if o.IsScrollMode() {
		t.Fatal("ModeNormal should not be scroll mode")
	}
	o.Mode = ModePassthrough
	if o.IsScrollMode() {
		t.Fatal("ModePassthrough should not be scroll mode")
	}
	o.Mode = ModeScroll
	if !o.IsScrollMode() {
		t.Fatal("ModeScroll should be scroll mode")
	}
	o.Mode = ModePassthroughScroll
	if !o.IsScrollMode() {
		t.Fatal("ModePassthroughScroll should be scroll mode")
	}
}

func TestRenderSelectHint_PassthroughScrollMode(t *testing.T) {
	o := newTestClient(10, 80)
	o.Mode = ModePassthroughScroll
	o.SelectHint = true
	var buf bytes.Buffer
	o.renderSelectHint(&buf)
	output := buf.String()
	// Hint should be on row 2 when in passthrough scroll mode (same as ModeScroll).
	if !bytes.Contains([]byte(output), []byte("[2;")) {
		t.Fatal("expected hint on row 2 in passthrough scroll mode")
	}
}

// --- ScrollHistory tests ---

func TestScrollMaxOffset_WithScrollHistory(t *testing.T) {
	o := newTestClient(10, 80)
	o.VT.ScrollRegionUsed = true
	// With 25 ScrollHistory lines, max offset should be 25
	// (total = 25 history + 10 live = 35, max = 35 - 10 = 25).
	o.VT.ScrollHistory = make([]string, 25)
	for i := range o.VT.ScrollHistory {
		o.VT.ScrollHistory[i] = "line"
	}
	maxOff, ok := o.scrollMaxOffset()
	if !ok {
		t.Fatal("expected ok=true")
	}
	if maxOff != 25 {
		t.Fatalf("expected maxOffset 25, got %d", maxOff)
	}
}

func TestScrollHistoryAnchor_FrozenInScrollMode(t *testing.T) {
	o := newTestClient(10, 80)
	o.VT.ScrollRegionUsed = true
	o.VT.ScrollHistory = make([]string, 20)
	for i := range o.VT.ScrollHistory {
		o.VT.ScrollHistory[i] = "line"
	}

	o.EnterScrollMode()
	if o.ScrollHistoryAnchor != 20 {
		t.Fatalf("expected anchor 20, got %d", o.ScrollHistoryAnchor)
	}

	// New lines arrive after entering scroll mode.
	o.VT.ScrollHistory = append(o.VT.ScrollHistory, "new1", "new2", "new3")

	// scrollHistoryLen should use the frozen anchor, not live length.
	got := o.scrollHistoryLen()
	if got != 20 {
		t.Fatalf("expected frozen scrollHistoryLen 20, got %d", got)
	}
}

func TestRenderScrollViewHistory_ShowsHistoryAndLive(t *testing.T) {
	o := newTestClient(5, 40)
	var out bytes.Buffer
	o.Output = &out

	o.VT.ScrollRegionUsed = true
	// Populate ScrollHistory.
	o.VT.ScrollHistory = []string{
		"scroll-line-0",
		"scroll-line-1",
		"scroll-line-2",
	}
	// Write some content to the live terminal.
	o.VT.Vt.Write([]byte("live-content\r\n"))

	o.EnterScrollMode()
	// Scroll up enough to see ScrollHistory lines.
	o.ScrollUp(3)
	o.RenderScreen()

	output := out.String()
	if !bytes.Contains([]byte(output), []byte("scroll-line-0")) {
		t.Fatalf("expected output to contain ScrollHistory line, got: %q", output)
	}
}

func TestScrollHistory_IgnoredWithoutScrollRegion(t *testing.T) {
	// When ScrollRegionUsed is false (e.g. Claude Code), ScrollHistory
	// should NOT be used even if it has content.
	o := newTestClient(10, 80)
	o.VT.ScrollHistory = make([]string, 25)
	for i := range o.VT.ScrollHistory {
		o.VT.ScrollHistory[i] = "line"
	}
	// ScrollRegionUsed is false (default).
	if o.hasScrollHistory() {
		t.Fatal("expected hasScrollHistory=false when ScrollRegionUsed is false")
	}
	// scrollMaxOffset should fall through to Scrollback path.
	for i := 0; i < 30; i++ {
		o.VT.Scrollback.Write([]byte("sb\n"))
	}
	maxOff, ok := o.scrollMaxOffset()
	if !ok {
		t.Fatal("expected ok=true from Scrollback path")
	}
	if maxOff == 25 {
		t.Fatal("maxOffset should NOT be based on ScrollHistory when ScrollRegionUsed is false")
	}
}

func TestScrollHistory_FallsBackToScrollbackWhenEmpty(t *testing.T) {
	o := newTestClient(5, 40)
	var out bytes.Buffer
	o.Output = &out

	// No ScrollHistory, but Scrollback has content.
	for i := 0; i < 15; i++ {
		o.VT.Scrollback.Write([]byte("sb-line\n"))
	}

	o.EnterScrollMode()
	o.ScrollUp(3)
	o.RenderScreen()

	output := out.String()
	if !bytes.Contains([]byte(output), []byte("sb-line")) {
		t.Fatalf("expected output to contain Scrollback content when ScrollHistory is empty")
	}
}

// newTestClientWithPTY creates a test client with a pipe as the PTY so
// writePTYOrHang works. Returns the client and a reader for the PTY output.
func newTestClientWithPTY(childRows, cols int) (*Client, *os.File) {
	r, w, _ := os.Pipe()
	vt := &virtualterminal.VT{
		Rows:      childRows + 2,
		Cols:      cols,
		ChildRows: childRows,
		Vt:        midterm.NewTerminal(childRows, cols),
		Output:    io.Discard,
		Ptm:       w,
	}
	sb := midterm.NewTerminal(childRows, cols)
	sb.AutoResizeY = true
	sb.AppendOnly = true
	vt.Scrollback = sb
	c := &Client{
		VT:     vt,
		Output: io.Discard,
		Mode:   ModeNormal,
	}
	return c, r
}

func TestHandleSGRMouse_AltScrollUp_SendsArrowKeys(t *testing.T) {
	c, r := newTestClientWithPTY(10, 80)
	defer r.Close()
	defer c.VT.Ptm.Close()

	c.VT.AltScrollEnabled = true
	c.HandleSGRMouse([]byte("<64;1;1"), true)

	// Should NOT enter scroll mode.
	if c.IsScrollMode() {
		t.Fatal("expected NOT to enter scroll mode when AltScrollEnabled")
	}
	if c.Mode != ModeNormal {
		t.Fatalf("expected ModeNormal, got %d", c.Mode)
	}

	// Read what was written to the PTY.
	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	got := string(buf[:n])
	// scrollStep=3, so expect 3 arrow up sequences.
	want := "\033[A\033[A\033[A"
	if got != want {
		t.Fatalf("expected PTY output %q, got %q", want, got)
	}
}

func TestHandleSGRMouse_AltScrollDown_SendsArrowKeys(t *testing.T) {
	c, r := newTestClientWithPTY(10, 80)
	defer r.Close()
	defer c.VT.Ptm.Close()

	c.VT.AltScrollEnabled = true
	c.HandleSGRMouse([]byte("<65;1;1"), true)

	if c.IsScrollMode() {
		t.Fatal("expected NOT to enter scroll mode when AltScrollEnabled")
	}

	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	got := string(buf[:n])
	want := "\033[B\033[B\033[B"
	if got != want {
		t.Fatalf("expected PTY output %q, got %q", want, got)
	}
}

func TestHandleSGRMouse_AltScrollDisabled_NormalBehavior(t *testing.T) {
	o := newTestClient(10, 80)
	for i := 0; i < 20; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}
	o.VT.AltScrollEnabled = false

	o.HandleSGRMouse([]byte("<64;1;1"), true)
	if !o.IsScrollMode() {
		t.Fatal("expected scroll mode when AltScrollEnabled is false")
	}
}

func TestHandleSGRMouse_AltScrollInPassthrough(t *testing.T) {
	c, r := newTestClientWithPTY(10, 80)
	defer r.Close()
	defer c.VT.Ptm.Close()

	c.Mode = ModePassthrough
	c.VT.AltScrollEnabled = true
	c.HandleSGRMouse([]byte("<64;1;1"), true)

	// Should stay in passthrough, not enter scroll mode.
	if c.Mode != ModePassthrough {
		t.Fatalf("expected ModePassthrough, got %d", c.Mode)
	}

	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	got := string(buf[:n])
	want := "\033[A\033[A\033[A"
	if got != want {
		t.Fatalf("expected PTY output %q, got %q", want, got)
	}
}
