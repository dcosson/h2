package overlay

import (
	"bytes"
	"io"
	"testing"

	"github.com/vito/midterm"

	"h2/internal/virtualterminal"
)

func newTestOverlay(childRows, cols int) *Overlay {
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
	return &Overlay{
		VT:   vt,
		Mode: ModeDefault,
	}
}

// --- ClampScrollOffset ---

func TestClampScrollOffset_NilScrollback(t *testing.T) {
	o := newTestOverlay(10, 80)
	o.VT.Scrollback = nil
	o.ScrollOffset = 5
	o.ClampScrollOffset()
	if o.ScrollOffset != 0 {
		t.Fatalf("expected 0, got %d", o.ScrollOffset)
	}
}

func TestClampScrollOffset_NoHistory(t *testing.T) {
	o := newTestOverlay(10, 80)
	// Scrollback cursor at Y=0, no history beyond one screen.
	o.ScrollOffset = 5
	o.ClampScrollOffset()
	if o.ScrollOffset != 0 {
		t.Fatalf("expected 0, got %d", o.ScrollOffset)
	}
}

func TestClampScrollOffset_WithHistory(t *testing.T) {
	o := newTestOverlay(10, 80)
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
	o := newTestOverlay(10, 80)
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
	o := newTestOverlay(10, 80)
	o.ScrollOffset = -5
	o.ClampScrollOffset()
	if o.ScrollOffset != 0 {
		t.Fatalf("expected 0, got %d", o.ScrollOffset)
	}
}

// --- EnterScrollMode / ExitScrollMode ---

func TestEnterExitScrollMode(t *testing.T) {
	o := newTestOverlay(10, 80)
	if o.Mode != ModeDefault {
		t.Fatalf("expected ModeDefault, got %d", o.Mode)
	}

	o.EnterScrollMode()
	if o.Mode != ModeScroll {
		t.Fatalf("expected ModeScroll, got %d", o.Mode)
	}
	if o.ScrollOffset != 0 {
		t.Fatalf("expected offset 0 on enter, got %d", o.ScrollOffset)
	}

	o.ScrollOffset = 5
	o.ExitScrollMode()
	if o.Mode != ModeDefault {
		t.Fatalf("expected ModeDefault after exit, got %d", o.Mode)
	}
	if o.ScrollOffset != 0 {
		t.Fatalf("expected offset 0 after exit, got %d", o.ScrollOffset)
	}
}

// --- ScrollUp / ScrollDown ---

func TestScrollUpDown(t *testing.T) {
	o := newTestOverlay(10, 80)
	// Write enough lines to have history.
	for i := 0; i < 30; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}

	o.EnterScrollMode()
	o.ScrollUp(5)
	if o.ScrollOffset != 5 {
		t.Fatalf("expected offset 5, got %d", o.ScrollOffset)
	}

	o.ScrollDown(3)
	if o.ScrollOffset != 2 {
		t.Fatalf("expected offset 2, got %d", o.ScrollOffset)
	}
}

func TestScrollDown_ExitsAtZero(t *testing.T) {
	o := newTestOverlay(10, 80)
	for i := 0; i < 30; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}

	o.EnterScrollMode()
	o.ScrollUp(3)
	if o.Mode != ModeScroll {
		t.Fatalf("expected ModeScroll, got %d", o.Mode)
	}

	// Scroll down past zero should exit scroll mode.
	o.ScrollDown(10)
	if o.Mode != ModeDefault {
		t.Fatalf("expected ModeDefault after scrolling to bottom, got %d", o.Mode)
	}
	if o.ScrollOffset != 0 {
		t.Fatalf("expected offset 0, got %d", o.ScrollOffset)
	}
}

// --- HandleSGRMouse ---

func TestHandleSGRMouse_ScrollUpEntersMode(t *testing.T) {
	o := newTestOverlay(10, 80)
	for i := 0; i < 20; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}

	// SGR mouse scroll up: button 64. Params = "<64;1;1"
	o.HandleSGRMouse([]byte("<64;1;1"))
	if o.Mode != ModeScroll {
		t.Fatalf("expected ModeScroll, got %d", o.Mode)
	}
	if o.ScrollOffset != scrollStep {
		t.Fatalf("expected offset %d, got %d", scrollStep, o.ScrollOffset)
	}
}

func TestHandleSGRMouse_ScrollDownInMode(t *testing.T) {
	o := newTestOverlay(10, 80)
	for i := 0; i < 20; i++ {
		o.VT.Scrollback.Write([]byte("line\n"))
	}

	o.EnterScrollMode()
	o.ScrollUp(10)

	before := o.ScrollOffset
	o.HandleSGRMouse([]byte("<65;1;1"))
	if o.ScrollOffset != before-scrollStep {
		t.Fatalf("expected offset %d, got %d", before-scrollStep, o.ScrollOffset)
	}
}

func TestHandleSGRMouse_IgnoredInPassthrough(t *testing.T) {
	o := newTestOverlay(10, 80)
	o.Mode = ModePassthrough
	o.HandleSGRMouse([]byte("<64;1;1"))
	if o.Mode != ModePassthrough {
		t.Fatalf("expected mode to stay ModePassthrough, got %d", o.Mode)
	}
}

func TestHandleSGRMouse_MalformedParams(t *testing.T) {
	o := newTestOverlay(10, 80)
	// No '<' prefix
	o.HandleSGRMouse([]byte("64;1;1"))
	if o.Mode != ModeDefault {
		t.Fatalf("expected ModeDefault, got %d", o.Mode)
	}
	// Too few params
	o.HandleSGRMouse([]byte("<64"))
	if o.Mode != ModeDefault {
		t.Fatalf("expected ModeDefault, got %d", o.Mode)
	}
	// Non-numeric button
	o.HandleSGRMouse([]byte("<abc;1;1"))
	if o.Mode != ModeDefault {
		t.Fatalf("expected ModeDefault, got %d", o.Mode)
	}
}

// --- HandleScrollBytes ---

func TestHandleScrollBytes_EscAtEndStartsPending(t *testing.T) {
	o := newTestOverlay(10, 80)
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

func TestHandleScrollBytes_EscFollowedByNonSeqExits(t *testing.T) {
	o := newTestOverlay(10, 80)
	o.EnterScrollMode()
	// Esc followed by a non-sequence byte exits scroll mode.
	buf := []byte{0x1B, 'x'}
	o.HandleScrollBytes(buf, 0, len(buf))
	if o.Mode != ModeDefault {
		t.Fatalf("expected ModeDefault, got %d", o.Mode)
	}
}

func TestHandleScrollBytes_PendingEscContinuation(t *testing.T) {
	o := newTestOverlay(10, 80)
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

func TestHandleScrollBytes_QExits(t *testing.T) {
	o := newTestOverlay(10, 80)
	o.EnterScrollMode()
	buf := []byte{'q'}
	o.HandleScrollBytes(buf, 0, len(buf))
	if o.Mode != ModeDefault {
		t.Fatalf("expected ModeDefault after q, got %d", o.Mode)
	}
}

func TestHandleScrollBytes_OtherKeysIgnored(t *testing.T) {
	o := newTestOverlay(10, 80)
	o.EnterScrollMode()
	o.ScrollOffset = 5
	buf := []byte{'a', 'b', 'c', ' ', '1'}
	o.HandleScrollBytes(buf, 0, len(buf))
	if o.Mode != ModeScroll {
		t.Fatalf("expected ModeScroll, got %d", o.Mode)
	}
	if o.ScrollOffset != 5 {
		t.Fatalf("expected offset 5, got %d", o.ScrollOffset)
	}
}

func TestHandleScrollBytes_ArrowUpScrolls(t *testing.T) {
	o := newTestOverlay(10, 80)
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
	o := newTestOverlay(10, 80)
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

// --- RenderLiveView renders from row 0 ---

func TestRenderLiveView_RendersVisibleRows(t *testing.T) {
	o := newTestOverlay(5, 40)
	// Write more lines than ChildRows. The primary terminal scrolls
	// internally, keeping visible content in rows 0 through ChildRows-1.
	for i := 0; i < 20; i++ {
		o.VT.Vt.Write([]byte("line\n"))
	}

	var buf bytes.Buffer
	o.renderLiveView(&buf)
	output := buf.String()

	// The live view should contain rendered content from the visible area.
	if len(output) == 0 {
		t.Fatal("expected non-empty render output")
	}
}

// --- Menu integration ---

func TestMenuItems_ContainsScroll(t *testing.T) {
	found := false
	for _, item := range MenuItems {
		if item == "Scroll" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected MenuItems to contain 'Scroll'")
	}
}

func TestMenuSelect_Scroll(t *testing.T) {
	o := newTestOverlay(10, 80)
	o.MenuIdx = 2
	o.MenuSelect()
	if o.Mode != ModeScroll {
		t.Fatalf("expected ModeScroll after selecting Scroll menu item, got %d", o.Mode)
	}
}

// --- Mode labels ---

func TestModeLabel_Scroll(t *testing.T) {
	o := newTestOverlay(10, 80)
	o.Mode = ModeScroll
	if got := o.ModeLabel(); got != "Scroll" {
		t.Fatalf("expected 'Scroll', got %q", got)
	}
}

func TestHelpLabel_Scroll(t *testing.T) {
	o := newTestOverlay(10, 80)
	o.Mode = ModeScroll
	got := o.HelpLabel()
	if got != "Scroll/Up/Down navigate | Esc exit" {
		t.Fatalf("unexpected help label: %q", got)
	}
}
