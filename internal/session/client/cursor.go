package client

import (
	"unicode"
	"unicode/utf8"
)

// CursorLeft moves the cursor left by one rune.
func (o *Overlay) CursorLeft() {
	if o.CursorPos > 0 {
		_, size := utf8.DecodeLastRune(o.Input[:o.CursorPos])
		o.CursorPos -= size
	}
}

// CursorRight moves the cursor right by one rune.
func (o *Overlay) CursorRight() {
	if o.CursorPos < len(o.Input) {
		_, size := utf8.DecodeRune(o.Input[o.CursorPos:])
		o.CursorPos += size
	}
}

// CursorToStart moves the cursor to the beginning of the input.
func (o *Overlay) CursorToStart() {
	o.CursorPos = 0
}

// CursorToEnd moves the cursor to the end of the input.
func (o *Overlay) CursorToEnd() {
	o.CursorPos = len(o.Input)
}

// CursorForwardWord moves the cursor forward to the end of the next word.
func (o *Overlay) CursorForwardWord() {
	i := o.CursorPos
	// Skip non-word characters.
	for i < len(o.Input) {
		r, size := utf8.DecodeRune(o.Input[i:])
		if isWordChar(r) {
			break
		}
		i += size
	}
	// Skip word characters.
	for i < len(o.Input) {
		r, size := utf8.DecodeRune(o.Input[i:])
		if !isWordChar(r) {
			break
		}
		i += size
	}
	o.CursorPos = i
}

// CursorBackwardWord moves the cursor backward to the start of the previous word.
func (o *Overlay) CursorBackwardWord() {
	i := o.CursorPos
	// Skip non-word characters backward.
	for i > 0 {
		r, size := utf8.DecodeLastRune(o.Input[:i])
		if isWordChar(r) {
			break
		}
		i -= size
		_ = r
	}
	// Skip word characters backward.
	for i > 0 {
		r, size := utf8.DecodeLastRune(o.Input[:i])
		if !isWordChar(r) {
			break
		}
		i -= size
		_ = r
	}
	o.CursorPos = i
}

// KillToEnd removes text from the cursor to the end of the input.
func (o *Overlay) KillToEnd() {
	o.Input = o.Input[:o.CursorPos]
}

// KillToStart removes text from the beginning of the input to the cursor.
func (o *Overlay) KillToStart() {
	o.Input = append(o.Input[:0], o.Input[o.CursorPos:]...)
	o.CursorPos = 0
}

// DeleteBackward removes the rune before the cursor. Returns true if a
// character was deleted.
func (o *Overlay) DeleteBackward() bool {
	if o.CursorPos <= 0 {
		return false
	}
	_, size := utf8.DecodeLastRune(o.Input[:o.CursorPos])
	copy(o.Input[o.CursorPos-size:], o.Input[o.CursorPos:])
	o.Input = o.Input[:len(o.Input)-size]
	o.CursorPos -= size
	return true
}

// InsertByte inserts a single byte at the cursor position.
func (o *Overlay) InsertByte(b byte) {
	o.Input = append(o.Input, 0)
	copy(o.Input[o.CursorPos+1:], o.Input[o.CursorPos:])
	o.Input[o.CursorPos] = b
	o.CursorPos++
}

// isWordChar returns true for characters considered part of a word
// (letters, digits, underscore).
func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
