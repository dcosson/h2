// Package outputcollector provides a reusable PTY idle detector. It monitors
// output notifications and emits active/idle state transitions based on a
// configurable silence threshold. Both the Claude Code and Codex adapters
// embed this as a fallback state source.
package outputcollector

import (
	"time"

	"h2/internal/session/agent/monitor"
)

// Collector derives state from child PTY output.
// It goes active on each NoteOutput signal and idle after the configured
// threshold with no further output.
type Collector struct {
	idleThreshold time.Duration
	notifyCh      chan struct{}
	stateCh       chan monitor.StateUpdate
	stopCh        chan struct{}
}

// New creates and starts a Collector with the given idle threshold.
func New(idleThreshold time.Duration) *Collector {
	c := &Collector{
		idleThreshold: idleThreshold,
		notifyCh:      make(chan struct{}, 1),
		stateCh:       make(chan monitor.StateUpdate, 1),
		stopCh:        make(chan struct{}),
	}
	go c.run()
	return c
}

// NoteOutput signals that the child process produced output.
func (c *Collector) NoteOutput() {
	select {
	case c.notifyCh <- struct{}{}:
	default:
	}
}

// StateCh returns the channel that receives state updates.
func (c *Collector) StateCh() <-chan monitor.StateUpdate {
	return c.stateCh
}

// Stop stops the internal goroutine.
func (c *Collector) Stop() {
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
}

func (c *Collector) run() {
	idleTimer := time.NewTimer(c.idleThreshold)
	defer idleTimer.Stop()

	for {
		select {
		case <-c.notifyCh:
			c.send(monitor.StateActive)
			resetTimer(idleTimer, c.idleThreshold)
		case <-idleTimer.C:
			c.send(monitor.StateIdle)
		case <-c.stopCh:
			return
		}
	}
}

func (c *Collector) send(s monitor.State) {
	su := monitor.StateUpdate{State: s, SubState: monitor.SubStateNone}
	select {
	case <-c.stateCh:
	default:
	}
	c.stateCh <- su
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
