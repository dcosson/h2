package collector

import (
	"time"

	"h2/internal/session/agent/monitor"
)

// StateCollector emits state transitions from a single signal source.
// Each implementation has its own goroutine and idle detection logic.
type StateCollector interface {
	StateCh() <-chan monitor.StateUpdate // receives state updates
	Stop()                              // stops internal goroutine
}

// resetTimer safely resets a timer, draining the channel if needed.
func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}
