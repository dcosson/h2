package overlay

// HistoryUp moves to the previous history entry.
func (o *Overlay) HistoryUp() {
	if len(o.History) == 0 {
		return
	}
	if o.HistIdx == -1 {
		o.Saved = make([]byte, len(o.Input))
		copy(o.Saved, o.Input)
		o.HistIdx = len(o.History) - 1
	} else if o.HistIdx > 0 {
		o.HistIdx--
	} else {
		return
	}
	o.Input = []byte(o.History[o.HistIdx])
}

// HistoryDown moves to the next history entry.
func (o *Overlay) HistoryDown() {
	if o.HistIdx == -1 {
		return
	}
	if o.HistIdx < len(o.History)-1 {
		o.HistIdx++
		o.Input = []byte(o.History[o.HistIdx])
	} else {
		o.HistIdx = -1
		o.Input = o.Saved
		o.Saved = nil
	}
}
