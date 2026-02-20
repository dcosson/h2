package collector

import (
	"time"

	"h2/internal/session/agent/monitor"
)

// OutputCollector derives state from child PTY output.
// It goes active on each NoteOutput signal and idle after monitor.IdleThreshold
// with no further output.
type OutputCollector struct {
	notifyCh chan struct{}
	stateCh  chan StateUpdate
	stopCh   chan struct{}
}

// NewOutputCollector creates and starts an OutputCollector.
func NewOutputCollector() *OutputCollector {
	c := &OutputCollector{
		notifyCh: make(chan struct{}, 1),
		stateCh:  make(chan StateUpdate, 1),
		stopCh:   make(chan struct{}),
	}
	go c.run()
	return c
}

// NoteOutput signals that the child process produced output.
func (c *OutputCollector) NoteOutput() {
	select {
	case c.notifyCh <- struct{}{}:
	default:
	}
}

// StateCh returns the channel that receives state updates.
func (c *OutputCollector) StateCh() <-chan StateUpdate {
	return c.stateCh
}

// Stop stops the internal goroutine.
func (c *OutputCollector) Stop() {
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
}

func (c *OutputCollector) run() {
	idleTimer := time.NewTimer(monitor.IdleThreshold)
	defer idleTimer.Stop()

	for {
		select {
		case <-c.notifyCh:
			c.send(StateActive)
			resetTimer(idleTimer, monitor.IdleThreshold)
		case <-idleTimer.C:
			c.send(StateIdle)
		case <-c.stopCh:
			return
		}
	}
}

func (c *OutputCollector) send(s State) {
	su := StateUpdate{State: s, SubState: SubStateNone}
	select {
	case <-c.stateCh:
	default:
	}
	c.stateCh <- su
}
