package client

// HistoryUp moves to the previous history entry.
func (c *Client) HistoryUp() {
	if len(c.History) == 0 {
		return
	}
	if c.HistIdx == -1 {
		c.Saved = make([]byte, len(c.Input))
		copy(c.Saved, c.Input)
		c.HistIdx = len(c.History) - 1
	} else if c.HistIdx > 0 {
		c.HistIdx--
	} else {
		return
	}
	c.Input = []byte(c.History[c.HistIdx])
	c.CursorPos = len(c.Input)
}

// HistoryDown moves to the next history entry.
func (c *Client) HistoryDown() {
	if c.HistIdx == -1 {
		return
	}
	if c.HistIdx < len(c.History)-1 {
		c.HistIdx++
		c.Input = []byte(c.History[c.HistIdx])
	} else {
		c.HistIdx = -1
		c.Input = c.Saved
		c.Saved = nil
	}
	c.CursorPos = len(c.Input)
}
