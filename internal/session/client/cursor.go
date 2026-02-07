package client

import (
	"unicode"
	"unicode/utf8"
)

// CursorLeft moves the cursor left by one rune.
func (c *Client) CursorLeft() {
	if c.CursorPos > 0 {
		_, size := utf8.DecodeLastRune(c.Input[:c.CursorPos])
		c.CursorPos -= size
	}
}

// CursorRight moves the cursor right by one rune.
func (c *Client) CursorRight() {
	if c.CursorPos < len(c.Input) {
		_, size := utf8.DecodeRune(c.Input[c.CursorPos:])
		c.CursorPos += size
	}
}

// CursorToStart moves the cursor to the beginning of the input.
func (c *Client) CursorToStart() {
	c.CursorPos = 0
}

// CursorToEnd moves the cursor to the end of the input.
func (c *Client) CursorToEnd() {
	c.CursorPos = len(c.Input)
}

// CursorForwardWord moves the cursor forward to the end of the next word.
func (c *Client) CursorForwardWord() {
	i := c.CursorPos
	// Skip non-word characters.
	for i < len(c.Input) {
		r, size := utf8.DecodeRune(c.Input[i:])
		if isWordChar(r) {
			break
		}
		i += size
	}
	// Skip word characters.
	for i < len(c.Input) {
		r, size := utf8.DecodeRune(c.Input[i:])
		if !isWordChar(r) {
			break
		}
		i += size
	}
	c.CursorPos = i
}

// CursorBackwardWord moves the cursor backward to the start of the previous word.
func (c *Client) CursorBackwardWord() {
	i := c.CursorPos
	// Skip non-word characters backward.
	for i > 0 {
		r, size := utf8.DecodeLastRune(c.Input[:i])
		if isWordChar(r) {
			break
		}
		i -= size
		_ = r
	}
	// Skip word characters backward.
	for i > 0 {
		r, size := utf8.DecodeLastRune(c.Input[:i])
		if !isWordChar(r) {
			break
		}
		i -= size
		_ = r
	}
	c.CursorPos = i
}

// KillToEnd removes text from the cursor to the end of the input.
func (c *Client) KillToEnd() {
	c.Input = c.Input[:c.CursorPos]
}

// KillToStart removes text from the beginning of the input to the cursor.
func (c *Client) KillToStart() {
	c.Input = append(c.Input[:0], c.Input[c.CursorPos:]...)
	c.CursorPos = 0
}

// DeleteBackward removes the rune before the cursor. Returns true if a
// character was deleted.
func (c *Client) DeleteBackward() bool {
	if c.CursorPos <= 0 {
		return false
	}
	_, size := utf8.DecodeLastRune(c.Input[:c.CursorPos])
	copy(c.Input[c.CursorPos-size:], c.Input[c.CursorPos:])
	c.Input = c.Input[:len(c.Input)-size]
	c.CursorPos -= size
	return true
}

// InsertByte inserts a single byte at the cursor position.
func (c *Client) InsertByte(b byte) {
	c.Input = append(c.Input, 0)
	copy(c.Input[c.CursorPos+1:], c.Input[c.CursorPos:])
	c.Input[c.CursorPos] = b
	c.CursorPos++
}

// isWordChar returns true for characters considered part of a word
// (letters, digits, underscore).
func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
