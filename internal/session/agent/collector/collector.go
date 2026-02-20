package collector

import (
	"time"

	"h2/internal/session/agent/monitor"
)

// Re-export types from monitor for use within the collector package.
// Callers outside the collector package should import monitor directly.
type State = monitor.State

const (
	StateInitialized = monitor.StateInitialized
	StateActive      = monitor.StateActive
	StateIdle        = monitor.StateIdle
	StateExited      = monitor.StateExited
)

type SubState = monitor.SubState

const (
	SubStateNone                 = monitor.SubStateNone
	SubStateThinking             = monitor.SubStateThinking
	SubStateToolUse              = monitor.SubStateToolUse
	SubStateWaitingForPermission = monitor.SubStateWaitingForPermission
	SubStateCompacting           = monitor.SubStateCompacting
)

type StateUpdate = monitor.StateUpdate

// StateCollector emits state transitions from a single signal source.
// Each implementation has its own goroutine and idle detection logic.
type StateCollector interface {
	StateCh() <-chan StateUpdate // receives state updates
	Stop()                      // stops internal goroutine
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
