// Package automation provides event-driven triggers and time-based schedules
// for h2 agent automation. Triggers fire once on event match; schedules fire
// on RRULE-based timing. Both execute actions (shell exec or message injection).
package automation

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"h2/internal/session/agent/monitor"
)

// Action defines what happens when a trigger fires or a schedule ticks.
// Exactly one of Exec or Message must be set.
type Action struct {
	Exec     string // shell command via sh -c
	Message  string // message injected into agent's PTY via message queue
	From     string // sender identity for message (default: h2-trigger or h2-schedule)
	Priority string // message priority: interrupt, normal, idle-first, idle
}

// Validate returns an error if the action is misconfigured.
func (a *Action) Validate() error {
	if a.Exec == "" && a.Message == "" {
		return fmt.Errorf("action must have either exec or message set")
	}
	if a.Exec != "" && a.Message != "" {
		return fmt.Errorf("action must have exactly one of exec or message, not both")
	}
	if a.Priority != "" {
		switch a.Priority {
		case "interrupt", "normal", "idle-first", "idle":
		default:
			return fmt.Errorf("invalid priority %q", a.Priority)
		}
	}
	return nil
}

// Trigger fires when an event matches and the optional condition passes.
// By default triggers are one-shot (MaxFirings=1). Set MaxFirings=-1 for
// unlimited firings, or MaxFirings=N>0 for a fixed count.
type Trigger struct {
	ID   string // unique identifier (8-char hex or user-provided)
	Name string // human-readable label (optional)

	// Event matching.
	Event    string // event type to match: "state_change", "approval_requested", etc.
	State    string // for state_change: match this state (empty = any)
	SubState string // for state_change: match this substate (empty = any)

	// Condition gate (optional).
	Condition string // shell command; trigger fires only if exit code 0

	Action Action

	// Lifecycle control.
	MaxFirings int           // -1 = unlimited, 0 = default (one-shot), N > 0 = fire N times
	ExpiresAt  time.Time     // zero value = no expiry
	Cooldown   time.Duration // zero value = no cooldown; eligible again at exactly Cooldown elapsed (>= not >)

	// Runtime tracking (internal, not user-configured).
	FireCount   int       // number of times this trigger has fired
	LastFiredAt time.Time // timestamp of last firing (for cooldown enforcement)
}

// effectiveMaxFirings returns the actual max firings, treating 0 (unset) as 1 (one-shot).
func (t *Trigger) effectiveMaxFirings() int {
	if t.MaxFirings == 0 {
		return 1 // default: one-shot
	}
	return t.MaxFirings
}

// Clock abstracts time for deterministic testing. Default: realClock (time.Now).
type Clock interface {
	Now() time.Time
}

// realClock is the default Clock using time.Now.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// ResolveExpiresAt parses an ExpiresAt string. Accepts RFC 3339 absolute
// timestamps or relative durations like "+1h". The now parameter is the base
// for relative timestamps (pass clock.Now() for consistency with injectable clock).
func ResolveExpiresAt(raw string, now time.Time) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	if strings.HasPrefix(raw, "+") {
		dur, err := time.ParseDuration(raw[1:])
		if err != nil {
			return time.Time{}, fmt.Errorf("parse relative expires_at %q: %w", raw, err)
		}
		return now.Add(dur), nil
	}
	return time.Parse(time.RFC3339, raw)
}

// ConditionMode controls how a schedule's condition gate interacts with firings.
type ConditionMode int

const (
	// RunIf executes the action only when the condition passes.
	RunIf ConditionMode = iota
	// StopWhen executes the action until the condition passes, then deletes the schedule.
	StopWhen
	// RunOnceWhen skips until the condition passes, fires once, then deletes.
	RunOnceWhen
)

// String returns the condition mode name.
func (m ConditionMode) String() string {
	switch m {
	case RunIf:
		return "run_if"
	case StopWhen:
		return "stop_when"
	case RunOnceWhen:
		return "run_once_when"
	default:
		return "unknown"
	}
}

// ParseConditionMode converts a string to a ConditionMode.
func ParseConditionMode(s string) (ConditionMode, bool) {
	switch s {
	case "run_if", "":
		return RunIf, true
	case "stop_when":
		return StopWhen, true
	case "run_once_when":
		return RunOnceWhen, true
	default:
		return 0, false
	}
}

// Schedule fires at times defined by a start time + RRULE, optionally gated
// by a condition.
type Schedule struct {
	ID   string // unique identifier
	Name string // human-readable label (optional)

	Start string // start time (RFC 3339); defaults to now if empty
	RRule string // RRULE string (RFC 5545)

	Condition     string        // shell command
	ConditionMode ConditionMode // how the condition interacts with firings

	Action Action
}

// MatchesEvent returns true if the trigger's event filter matches the given event.
func (t *Trigger) MatchesEvent(evt monitor.AgentEvent) bool {
	if t.Event != evt.Type.String() {
		return false
	}
	if evt.Type == monitor.EventStateChange {
		var data monitor.StateChangeData
		switch d := evt.Data.(type) {
		case monitor.StateChangeData:
			data = d
		case *monitor.StateChangeData:
			data = *d
		default:
			return false
		}
		if t.State != "" && t.State != data.State.String() {
			return false
		}
		if t.SubState != "" && t.SubState != data.SubState.String() {
			return false
		}
	}
	return true
}

// EvalCondition runs a condition command in a shell and returns true if it
// exits 0. If the condition is empty, returns true. The env map provides
// additional environment variables overlaid on top of the inherited process
// environment.
func EvalCondition(ctx context.Context, condition string, env map[string]string) bool {
	if condition == "" {
		return true
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", condition)
	cmd.Env = buildFullEnv(env)
	return cmd.Run() == nil
}

// DefaultConditionTimeout is the maximum time a condition command can run.
var DefaultConditionTimeout = 10 * time.Second
