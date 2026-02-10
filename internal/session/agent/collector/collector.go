package collector

import "time"

// IdleThreshold is how long without activity before an agent is considered idle.
const IdleThreshold = 2 * time.Second

// State represents the derived activity state of an agent.
type State int

const (
	StateInitialized State = iota // just created, no events yet
	StateActive                   // receiving activity signals
	StateIdle                     // no activity for IdleThreshold
	StateExited                   // child process exited
)

// String returns a human-readable name for the state.
func (s State) String() string {
	switch s {
	case StateInitialized:
		return "initialized"
	case StateActive:
		return "active"
	case StateIdle:
		return "idle"
	case StateExited:
		return "exited"
	default:
		return "unknown"
	}
}

// StateCollector emits state transitions from a single signal source.
// Each implementation has its own goroutine and idle detection logic.
type StateCollector interface {
	StateCh() <-chan State // receives state changes
	Stop()                // stops internal goroutine
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
