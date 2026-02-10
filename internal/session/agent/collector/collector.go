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

// SubState represents what the agent is doing within the Active state.
type SubState int

const (
	SubStateNone                 SubState = iota // no sub-state (non-Active, or unknown)
	SubStateThinking                             // waiting for model response
	SubStateToolUse                              // executing a tool
	SubStateWaitingForPermission                 // blocked on user permission approval
)

// String returns a human-readable name for the sub-state.
func (ss SubState) String() string {
	switch ss {
	case SubStateNone:
		return ""
	case SubStateThinking:
		return "thinking"
	case SubStateToolUse:
		return "tool_use"
	case SubStateWaitingForPermission:
		return "waiting_for_permission"
	default:
		return ""
	}
}

// StateUpdate is a (State, SubState) pair emitted by collectors.
type StateUpdate struct {
	State    State
	SubState SubState
}

// FormatStateLabel returns a display label like "Active (thinking)" or "Idle".
// The subState string comes from SubState.String() (or the SubState field in
// AgentInfo). If subState is empty, just the capitalized state is returned.
// An optional toolName is appended to the "tool use" sub-state, e.g.
// "Active (tool use: Bash)".
func FormatStateLabel(state, subState string, toolName ...string) string {
	var label string
	switch state {
	case "active":
		label = "Active"
	case "idle":
		label = "Idle"
	case "exited":
		label = "Exited"
	case "initialized":
		label = "Initialized"
	default:
		label = state
	}
	if subState == "" {
		return label
	}
	var pretty string
	switch subState {
	case "thinking":
		pretty = "thinking"
	case "tool_use":
		pretty = "tool use"
		if len(toolName) > 0 && toolName[0] != "" {
			pretty += ": " + toolName[0]
		}
	case "waiting_for_permission":
		pretty = "permission"
	default:
		pretty = subState
	}
	return label + " (" + pretty + ")"
}

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
