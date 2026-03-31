package automation

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/google/uuid"

	"h2/internal/session/agent/monitor"
)

// TriggerEngine subscribes to the agent's event stream and fires registered
// triggers when events match. By default triggers are one-shot (consumed after
// first firing). Repeating triggers use MaxFirings, ExpiresAt, and Cooldown
// to control lifecycle.
type TriggerEngine struct {
	mu            sync.Mutex
	triggers      map[string]*Trigger
	runner        *ActionRunner
	clock         Clock
	stateProvider StateProvider
}

// NewTriggerEngine creates a TriggerEngine that dispatches actions via the given runner.
// The optional stateProvider injects H2_AGENT_STATE/H2_AGENT_SUBSTATE into the env.
func NewTriggerEngine(runner *ActionRunner, stateProvider ...StateProvider) *TriggerEngine {
	te := &TriggerEngine{
		triggers: make(map[string]*Trigger),
		runner:   runner,
		clock:    realClock{},
	}
	if len(stateProvider) > 0 {
		te.stateProvider = stateProvider[0]
	}
	return te
}

// SetClock overrides the time source for testing.
func (te *TriggerEngine) SetClock(c Clock) {
	te.clock = c
}

// Run processes events from the channel until ctx is cancelled.
// This should be started as a goroutine.
func (te *TriggerEngine) Run(ctx context.Context, events <-chan monitor.AgentEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}
			te.processEvent(ctx, evt)
		}
	}
}

// Add registers a trigger. Auto-generates an ID if empty.
// Returns false if the ID already exists.
func (te *TriggerEngine) Add(t *Trigger) bool {
	te.mu.Lock()
	defer te.mu.Unlock()
	if t.ID == "" {
		t.ID = uuid.New().String()[:8]
	}
	if _, exists := te.triggers[t.ID]; exists {
		return false
	}
	te.triggers[t.ID] = t
	return true
}

// Clear removes all registered triggers.
func (te *TriggerEngine) Clear() {
	te.mu.Lock()
	defer te.mu.Unlock()
	te.triggers = make(map[string]*Trigger)
}

// Remove deletes a trigger by ID. Returns true if it existed.
func (te *TriggerEngine) Remove(id string) bool {
	te.mu.Lock()
	defer te.mu.Unlock()
	if _, exists := te.triggers[id]; !exists {
		return false
	}
	delete(te.triggers, id)
	return true
}

// List returns a snapshot copy of all registered triggers. Returns value copies
// (not pointers to live structs) so callers can safely read FireCount/LastFiredAt
// without holding the engine's lock.
func (te *TriggerEngine) List() []Trigger {
	te.mu.Lock()
	defer te.mu.Unlock()
	result := make([]Trigger, 0, len(te.triggers))
	for _, t := range te.triggers {
		result = append(result, *t)
	}
	return result
}

// processEvent checks all registered triggers against the event. Expired
// triggers are reaped opportunistically. Matching triggers are evaluated
// and fired according to their lifecycle settings.
func (te *TriggerEngine) processEvent(ctx context.Context, evt monitor.AgentEvent) {
	now := te.clock.Now()
	te.mu.Lock()
	var matched []*Trigger
	for id, t := range te.triggers {
		// Reap expired triggers opportunistically.
		if !t.ExpiresAt.IsZero() && now.After(t.ExpiresAt) {
			delete(te.triggers, id)
			continue
		}
		if t.MatchesEvent(evt) {
			matched = append(matched, t)
		}
	}
	te.mu.Unlock()

	for _, t := range matched {
		te.evalAndFire(ctx, t, evt)
	}
}

// evalAndFire evaluates the trigger's condition and, if it passes, fires the
// action. Lifecycle control (cooldown, expiry, fire count) determines whether
// the trigger is kept or removed after firing.
func (te *TriggerEngine) evalAndFire(ctx context.Context, t *Trigger, evt monitor.AgentEvent) {
	now := te.clock.Now()

	// Pre-check: acquire lock to read mutable fields (LastFiredAt) and check
	// expiry/cooldown atomically. This prevents a data race where concurrent
	// evalAndFire calls both read LastFiredAt before either writes it.
	// Use pointer identity (cur == t) not just ID presence, because a trigger
	// could be removed and a new one added with the same ID between unlocks.
	te.mu.Lock()
	cur := te.triggers[t.ID]
	if cur != t {
		te.mu.Unlock()
		return // trigger was replaced, consumed, or reaped
	}

	// Check expiry under lock.
	if !t.ExpiresAt.IsZero() && now.After(t.ExpiresAt) {
		delete(te.triggers, t.ID)
		te.mu.Unlock()
		return
	}

	// Check cooldown under lock (reads LastFiredAt which is written under lock).
	if t.Cooldown > 0 && !t.LastFiredAt.IsZero() {
		if now.Sub(t.LastFiredAt) < t.Cooldown {
			te.mu.Unlock()
			return
		}
	}
	te.mu.Unlock()

	// Condition evaluation happens outside the lock (may be slow/blocking).
	env := te.buildTriggerEnv(t, evt)

	condCtx, cancel := context.WithTimeout(ctx, DefaultConditionTimeout)
	defer cancel()
	condEnv := te.runner.MergeEnv(env)
	if !EvalCondition(condCtx, t.Condition, condEnv, te.runner.WorkDir()) {
		return
	}

	// Re-acquire lock to update tracking and determine removal.
	// Must re-check pointer identity since trigger may have been replaced/reaped
	// while condition was evaluating.
	te.mu.Lock()
	cur = te.triggers[t.ID]
	if cur != t {
		te.mu.Unlock()
		return // replaced/reaped during condition evaluation
	}

	t.FireCount++
	t.LastFiredAt = now

	maxFirings := t.effectiveMaxFirings()
	exhausted := maxFirings > 0 && t.FireCount >= maxFirings
	if exhausted {
		delete(te.triggers, t.ID)
	}
	te.mu.Unlock()

	// Build header with current fire count for message delivery.
	action := t.Action
	action.Header = t.TriggerHeader(evt.Type.String())

	if err := te.runner.Run(action, env); err != nil {
		fmt.Fprintf(os.Stderr, "automation: trigger action failed id=%s error=%v\n", t.ID, err)
	}
}

// buildTriggerEnv constructs the extra environment variables for a trigger's
// condition and action execution.
func (te *TriggerEngine) buildTriggerEnv(t *Trigger, evt monitor.AgentEvent) map[string]string {
	env := make(map[string]string)

	// Event vars.
	env["H2_EVENT_TYPE"] = evt.Type.String()
	if evt.Type == monitor.EventStateChange {
		state, sub := extractStateChange(evt)
		env["H2_EVENT_STATE"] = state
		env["H2_EVENT_SUBSTATE"] = sub
		// For state_change events, agent state IS the event state.
		env["H2_AGENT_STATE"] = state
		env["H2_AGENT_SUBSTATE"] = sub
	} else if te.stateProvider != nil {
		// For non-state-change events, query current state.
		state, sub := te.stateProvider()
		env["H2_AGENT_STATE"] = state
		env["H2_AGENT_SUBSTATE"] = sub
	}

	// Rotate-specific vars.
	if evt.Type == monitor.EventSessionRotated {
		if data, ok := evt.Data.(monitor.SessionRotatedData); ok {
			env["H2_OLD_PROFILE"] = data.OldProfile
			env["H2_NEW_PROFILE"] = data.NewProfile
		}
	}

	// Identity var.
	env["H2_TRIGGER_ID"] = t.ID

	return env
}

// extractStateChange pulls state/substate strings from an event, handling
// both value and pointer Data types for compatibility.
func extractStateChange(evt monitor.AgentEvent) (string, string) {
	switch data := evt.Data.(type) {
	case monitor.StateChangeData:
		return data.State.String(), data.SubState.String()
	case *monitor.StateChangeData:
		return data.State.String(), data.SubState.String()
	default:
		return "", ""
	}
}
