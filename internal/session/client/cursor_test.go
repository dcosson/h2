package client

import "testing"

// --- CursorLeft / CursorRight ---

func TestCursorLeft_MovesBackOneRune(t *testing.T) {
	o := &Overlay{Input: []byte("abc"), CursorPos: 3}
	o.CursorLeft()
	if o.CursorPos != 2 {
		t.Fatalf("expected 2, got %d", o.CursorPos)
	}
}

func TestCursorLeft_AtStart(t *testing.T) {
	o := &Overlay{Input: []byte("abc"), CursorPos: 0}
	o.CursorLeft()
	if o.CursorPos != 0 {
		t.Fatalf("expected 0, got %d", o.CursorPos)
	}
}

func TestCursorLeft_MultibyteUTF8(t *testing.T) {
	// "aé" is 3 bytes: 'a'(1) + 'é'(2)
	o := &Overlay{Input: []byte("aé"), CursorPos: 3}
	o.CursorLeft()
	if o.CursorPos != 1 {
		t.Fatalf("expected 1, got %d", o.CursorPos)
	}
}

func TestCursorRight_MovesForwardOneRune(t *testing.T) {
	o := &Overlay{Input: []byte("abc"), CursorPos: 0}
	o.CursorRight()
	if o.CursorPos != 1 {
		t.Fatalf("expected 1, got %d", o.CursorPos)
	}
}

func TestCursorRight_AtEnd(t *testing.T) {
	o := &Overlay{Input: []byte("abc"), CursorPos: 3}
	o.CursorRight()
	if o.CursorPos != 3 {
		t.Fatalf("expected 3, got %d", o.CursorPos)
	}
}

func TestCursorRight_MultibyteUTF8(t *testing.T) {
	o := &Overlay{Input: []byte("éb"), CursorPos: 0}
	o.CursorRight()
	if o.CursorPos != 2 {
		t.Fatalf("expected 2, got %d", o.CursorPos)
	}
}

// --- CursorToStart / CursorToEnd ---

func TestCursorToStart(t *testing.T) {
	o := &Overlay{Input: []byte("hello"), CursorPos: 3}
	o.CursorToStart()
	if o.CursorPos != 0 {
		t.Fatalf("expected 0, got %d", o.CursorPos)
	}
}

func TestCursorToEnd(t *testing.T) {
	o := &Overlay{Input: []byte("hello"), CursorPos: 2}
	o.CursorToEnd()
	if o.CursorPos != 5 {
		t.Fatalf("expected 5, got %d", o.CursorPos)
	}
}

// --- CursorForwardWord / CursorBackwardWord ---

func TestCursorForwardWord_SkipsToEndOfWord(t *testing.T) {
	o := &Overlay{Input: []byte("hello world"), CursorPos: 0}
	o.CursorForwardWord()
	if o.CursorPos != 5 {
		t.Fatalf("expected 5, got %d", o.CursorPos)
	}
}

func TestCursorForwardWord_SkipsSpaceThenWord(t *testing.T) {
	o := &Overlay{Input: []byte("hello world"), CursorPos: 5}
	o.CursorForwardWord()
	if o.CursorPos != 11 {
		t.Fatalf("expected 11, got %d", o.CursorPos)
	}
}

func TestCursorForwardWord_AtEnd(t *testing.T) {
	o := &Overlay{Input: []byte("hello"), CursorPos: 5}
	o.CursorForwardWord()
	if o.CursorPos != 5 {
		t.Fatalf("expected 5, got %d", o.CursorPos)
	}
}

func TestCursorForwardWord_MultipleSpaces(t *testing.T) {
	o := &Overlay{Input: []byte("a   b"), CursorPos: 1}
	o.CursorForwardWord()
	if o.CursorPos != 5 {
		t.Fatalf("expected 5, got %d", o.CursorPos)
	}
}

func TestCursorBackwardWord_SkipsToStartOfWord(t *testing.T) {
	o := &Overlay{Input: []byte("hello world"), CursorPos: 11}
	o.CursorBackwardWord()
	if o.CursorPos != 6 {
		t.Fatalf("expected 6, got %d", o.CursorPos)
	}
}

func TestCursorBackwardWord_SkipsSpaceThenWord(t *testing.T) {
	o := &Overlay{Input: []byte("hello world"), CursorPos: 6}
	o.CursorBackwardWord()
	if o.CursorPos != 0 {
		t.Fatalf("expected 0, got %d", o.CursorPos)
	}
}

func TestCursorBackwardWord_AtStart(t *testing.T) {
	o := &Overlay{Input: []byte("hello"), CursorPos: 0}
	o.CursorBackwardWord()
	if o.CursorPos != 0 {
		t.Fatalf("expected 0, got %d", o.CursorPos)
	}
}

// --- KillToEnd / KillToStart ---

func TestKillToEnd(t *testing.T) {
	o := &Overlay{Input: []byte("hello world"), CursorPos: 5}
	o.KillToEnd()
	if string(o.Input) != "hello" {
		t.Fatalf("expected %q, got %q", "hello", string(o.Input))
	}
	if o.CursorPos != 5 {
		t.Fatalf("expected cursor 5, got %d", o.CursorPos)
	}
}

func TestKillToEnd_AtEnd(t *testing.T) {
	o := &Overlay{Input: []byte("hello"), CursorPos: 5}
	o.KillToEnd()
	if string(o.Input) != "hello" {
		t.Fatalf("expected %q, got %q", "hello", string(o.Input))
	}
}

func TestKillToEnd_AtStart(t *testing.T) {
	o := &Overlay{Input: []byte("hello"), CursorPos: 0}
	o.KillToEnd()
	if string(o.Input) != "" {
		t.Fatalf("expected empty, got %q", string(o.Input))
	}
	if o.CursorPos != 0 {
		t.Fatalf("expected cursor 0, got %d", o.CursorPos)
	}
}

func TestKillToStart(t *testing.T) {
	o := &Overlay{Input: []byte("hello world"), CursorPos: 5}
	o.KillToStart()
	if string(o.Input) != " world" {
		t.Fatalf("expected %q, got %q", " world", string(o.Input))
	}
	if o.CursorPos != 0 {
		t.Fatalf("expected cursor 0, got %d", o.CursorPos)
	}
}

func TestKillToStart_AtStart(t *testing.T) {
	o := &Overlay{Input: []byte("hello"), CursorPos: 0}
	o.KillToStart()
	if string(o.Input) != "hello" {
		t.Fatalf("expected %q, got %q", "hello", string(o.Input))
	}
}

func TestKillToStart_AtEnd(t *testing.T) {
	o := &Overlay{Input: []byte("hello"), CursorPos: 5}
	o.KillToStart()
	if string(o.Input) != "" {
		t.Fatalf("expected empty, got %q", string(o.Input))
	}
}

// --- DeleteBackward ---

func TestDeleteBackward_MiddleOfString(t *testing.T) {
	o := &Overlay{Input: []byte("abcd"), CursorPos: 2}
	ok := o.DeleteBackward()
	if !ok {
		t.Fatal("expected true")
	}
	if string(o.Input) != "acd" {
		t.Fatalf("expected %q, got %q", "acd", string(o.Input))
	}
	if o.CursorPos != 1 {
		t.Fatalf("expected cursor 1, got %d", o.CursorPos)
	}
}

func TestDeleteBackward_AtEnd(t *testing.T) {
	o := &Overlay{Input: []byte("abc"), CursorPos: 3}
	o.DeleteBackward()
	if string(o.Input) != "ab" {
		t.Fatalf("expected %q, got %q", "ab", string(o.Input))
	}
	if o.CursorPos != 2 {
		t.Fatalf("expected cursor 2, got %d", o.CursorPos)
	}
}

func TestDeleteBackward_AtStart(t *testing.T) {
	o := &Overlay{Input: []byte("abc"), CursorPos: 0}
	ok := o.DeleteBackward()
	if ok {
		t.Fatal("expected false")
	}
	if string(o.Input) != "abc" {
		t.Fatalf("expected %q, got %q", "abc", string(o.Input))
	}
}

func TestDeleteBackward_MultibyteUTF8(t *testing.T) {
	// "aéb" = 'a'(1) + 'é'(2) + 'b'(1) = 4 bytes
	o := &Overlay{Input: []byte("aéb"), CursorPos: 3} // cursor after 'é'
	o.DeleteBackward()
	if string(o.Input) != "ab" {
		t.Fatalf("expected %q, got %q", "ab", string(o.Input))
	}
	if o.CursorPos != 1 {
		t.Fatalf("expected cursor 1, got %d", o.CursorPos)
	}
}

// --- InsertByte ---

func TestInsertByte_AtStart(t *testing.T) {
	o := &Overlay{Input: []byte("bc"), CursorPos: 0}
	o.InsertByte('a')
	if string(o.Input) != "abc" {
		t.Fatalf("expected %q, got %q", "abc", string(o.Input))
	}
	if o.CursorPos != 1 {
		t.Fatalf("expected cursor 1, got %d", o.CursorPos)
	}
}

func TestInsertByte_AtEnd(t *testing.T) {
	o := &Overlay{Input: []byte("ab"), CursorPos: 2}
	o.InsertByte('c')
	if string(o.Input) != "abc" {
		t.Fatalf("expected %q, got %q", "abc", string(o.Input))
	}
	if o.CursorPos != 3 {
		t.Fatalf("expected cursor 3, got %d", o.CursorPos)
	}
}

func TestInsertByte_Middle(t *testing.T) {
	o := &Overlay{Input: []byte("ac"), CursorPos: 1}
	o.InsertByte('b')
	if string(o.Input) != "abc" {
		t.Fatalf("expected %q, got %q", "abc", string(o.Input))
	}
	if o.CursorPos != 2 {
		t.Fatalf("expected cursor 2, got %d", o.CursorPos)
	}
}

func TestInsertByte_EmptyInput(t *testing.T) {
	o := &Overlay{Input: []byte{}, CursorPos: 0}
	o.InsertByte('a')
	if string(o.Input) != "a" {
		t.Fatalf("expected %q, got %q", "a", string(o.Input))
	}
	if o.CursorPos != 1 {
		t.Fatalf("expected cursor 1, got %d", o.CursorPos)
	}
}

// --- History sets CursorPos ---

func TestHistoryUp_SetsCursorToEnd(t *testing.T) {
	o := &Overlay{
		Input:   []byte{},
		History: []string{"hello"},
		HistIdx: -1,
	}
	o.HistoryUp()
	if o.CursorPos != 5 {
		t.Fatalf("expected cursor 5, got %d", o.CursorPos)
	}
}

func TestHistoryDown_SetsCursorToEnd(t *testing.T) {
	o := &Overlay{
		Input:   []byte("prev"),
		History: []string{"hello", "world"},
		HistIdx: 0,
	}
	o.HistoryDown()
	if string(o.Input) != "world" {
		t.Fatalf("expected %q, got %q", "world", string(o.Input))
	}
	if o.CursorPos != 5 {
		t.Fatalf("expected cursor 5, got %d", o.CursorPos)
	}
}

// --- isWordChar ---

func TestIsWordChar(t *testing.T) {
	tests := []struct {
		r    rune
		want bool
	}{
		{'a', true},
		{'Z', true},
		{'5', true},
		{'_', true},
		{' ', false},
		{'-', false},
		{'.', false},
		{'/', false},
	}
	for _, tt := range tests {
		got := isWordChar(tt.r)
		if got != tt.want {
			t.Errorf("isWordChar(%q) = %v, want %v", tt.r, got, tt.want)
		}
	}
}
