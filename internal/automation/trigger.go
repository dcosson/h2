package automation

import (
	"context"
	"log/slog"
	"sync"

	"h2/internal/session/agent/monitor"
)

// TriggerEngine subscribes to the agent's event stream and fires registered
// triggers when events match. Triggers are one-shot: consumed on attempt
// regardless of action success.
type TriggerEngine struct {
	mu            sync.Mutex
	triggers      map[string]*Trigger
	runner        *ActionRunner
	logger        *slog.Logger
	stateProvider StateProvider
}

// NewTriggerEngine creates a TriggerEngine that dispatches actions via the given runner.
// The optional stateProvider injects H2_AGENT_STATE/H2_AGENT_SUBSTATE into the env.
func NewTriggerEngine(runner *ActionRunner, logger *slog.Logger, stateProvider ...StateProvider) *TriggerEngine {
	if logger == nil {
		logger = slog.Default()
	}
	te := &TriggerEngine{
		triggers: make(map[string]*Trigger),
		runner:   runner,
		logger:   logger,
	}
	if len(stateProvider) > 0 {
		te.stateProvider = stateProvider[0]
	}
	return te
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

// Add registers a trigger. Returns false if the ID already exists.
func (te *TriggerEngine) Add(t *Trigger) bool {
	te.mu.Lock()
	defer te.mu.Unlock()
	if _, exists := te.triggers[t.ID]; exists {
		return false
	}
	te.triggers[t.ID] = t
	return true
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

// List returns a copy of all registered triggers.
func (te *TriggerEngine) List() []*Trigger {
	te.mu.Lock()
	defer te.mu.Unlock()
	result := make([]*Trigger, 0, len(te.triggers))
	for _, t := range te.triggers {
		result = append(result, t)
	}
	return result
}

// processEvent checks all registered triggers against the event. Matching
// triggers whose conditions pass are fired and removed (one-shot).
func (te *TriggerEngine) processEvent(ctx context.Context, evt monitor.AgentEvent) {
	te.mu.Lock()
	// Snapshot matching triggers under lock to avoid holding lock during
	// condition evaluation and action dispatch.
	var matched []*Trigger
	for _, t := range te.triggers {
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
// action and removes the trigger. If the condition fails, the trigger stays.
func (te *TriggerEngine) evalAndFire(ctx context.Context, t *Trigger, evt monitor.AgentEvent) {
	env := te.buildTriggerEnv(t, evt)

	condCtx, cancel := context.WithTimeout(ctx, DefaultConditionTimeout)
	defer cancel()

	if !EvalCondition(condCtx, t.Condition, env) {
		te.logger.Debug("trigger condition failed, keeping",
			"trigger_id", t.ID, "trigger_name", t.Name)
		return
	}

	// Consume on attempt: remove before running action.
	te.mu.Lock()
	_, existed := te.triggers[t.ID]
	delete(te.triggers, t.ID)
	te.mu.Unlock()

	if !existed {
		// Another goroutine already consumed this trigger (race between
		// concurrent events). Skip silently.
		return
	}

	te.logger.Info("trigger fired",
		"trigger_id", t.ID, "trigger_name", t.Name,
		"event", evt.Type.String())

	if err := te.runner.Run(t.Action, env); err != nil {
		te.logger.Warn("trigger action failed",
			"trigger_id", t.ID, "error", err)
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
