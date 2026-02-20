package collector

import (
	"time"

	"h2/internal/session/agent/monitor"
)

// OtelCollector derives state from OTEL log events.
// It goes active on each NoteEvent signal and idle after monitor.IdleThreshold
// with no further events.
type OtelCollector struct {
	notifyCh chan struct{}
	stateCh  chan monitor.StateUpdate
	stopCh   chan struct{}
}

// NewOtelCollector creates and starts an OtelCollector.
func NewOtelCollector() *OtelCollector {
	c := &OtelCollector{
		notifyCh: make(chan struct{}, 1),
		stateCh:  make(chan monitor.StateUpdate, 1),
		stopCh:   make(chan struct{}),
	}
	go c.run()
	return c
}

// NoteEvent signals that an OTEL event was received.
func (c *OtelCollector) NoteEvent() {
	select {
	case c.notifyCh <- struct{}{}:
	default:
	}
}

// StateCh returns the channel that receives state updates.
func (c *OtelCollector) StateCh() <-chan monitor.StateUpdate {
	return c.stateCh
}

// Stop stops the internal goroutine.
func (c *OtelCollector) Stop() {
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
}

func (c *OtelCollector) run() {
	idleTimer := time.NewTimer(monitor.IdleThreshold)
	defer idleTimer.Stop()

	for {
		select {
		case <-c.notifyCh:
			c.send(monitor.StateActive)
			resetTimer(idleTimer, monitor.IdleThreshold)
		case <-idleTimer.C:
			c.send(monitor.StateIdle)
		case <-c.stopCh:
			return
		}
	}
}

func (c *OtelCollector) send(s monitor.State) {
	su := monitor.StateUpdate{State: s, SubState: monitor.SubStateNone}
	select {
	case <-c.stateCh:
	default:
	}
	c.stateCh <- su
}
