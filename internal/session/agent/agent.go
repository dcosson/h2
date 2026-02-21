package agent

import (
	"context"
	"encoding/json"
	"time"

	"h2/internal/activitylog"
	"h2/internal/session/agent/collector"
	"h2/internal/session/agent/harness"
	"h2/internal/session/agent/monitor"
)

// Agent manages state, metrics, and lifecycle for a session's agent process.
// It delegates all agent-type-specific behavior to a Harness, and uses an
// AgentMonitor for derived state + metrics.
type Agent struct {
	harness harness.Harness

	agentMonitor *monitor.AgentMonitor
	cancel       context.CancelFunc

	// Hook collector: kept for backward compat Snapshot() data.
	// Hook events are forwarded to both harness and hooksCollector.
	hooksCollector *collector.HookCollector

	// Activity logger (nil-safe; Nop logger when not set).
	activityLog *activitylog.Logger

	// Signals
	stopCh chan struct{}
}

// New creates a new Agent with the given harness.
func New(h harness.Harness) *Agent {
	return &Agent{
		harness:      h,
		agentMonitor: monitor.New(),
		stopCh:       make(chan struct{}),
	}
}

// SetHarness replaces the agent's harness. Used by setupAgent() to upgrade
// from a minimal harness (created in session.New) to a fully-configured one
// with proper logger and config dir.
func (a *Agent) SetHarness(h harness.Harness) {
	a.harness = h
}

// SetActivityLog sets the activity logger for this agent.
// Must be called before PrepareForLaunch.
func (a *Agent) SetActivityLog(l *activitylog.Logger) {
	a.activityLog = l
}

// ActivityLog returns the activity logger (never nil — returns Nop if unset).
func (a *Agent) ActivityLog() *activitylog.Logger {
	if a.activityLog != nil {
		return a.activityLog
	}
	return activitylog.Nop()
}

// PrepareForLaunch prepares the harness and returns env vars and CLI args
// to inject into the child process. Must be called after SetActivityLog
// and before Start.
func (a *Agent) PrepareForLaunch(agentName, sessionID string, dryRun bool) (harness.LaunchConfig, error) {
	if a.harness == nil {
		return harness.LaunchConfig{}, nil
	}

	// Create hook collector for agents that use hooks (Claude Code).
	if !dryRun && a.harness.Name() == "claude_code" {
		a.hooksCollector = collector.NewHookCollector(a.ActivityLog())
	}

	return a.harness.PrepareForLaunch(agentName, sessionID, dryRun)
}

// Start begins the harness+monitor pipeline. Must be called after
// PrepareForLaunch and after the child process starts.
func (a *Agent) Start(ctx context.Context) {
	ctx, a.cancel = context.WithCancel(ctx)

	if a.harness != nil {
		go a.harness.Start(ctx, a.agentMonitor.Events())
	}
	go a.agentMonitor.Run(ctx)
}

// HandleHookEvent processes a hook event, forwarding to both the harness
// (for event emission) and the hook collector (for backward compat Snapshot data).
func (a *Agent) HandleHookEvent(eventName string, payload json.RawMessage) bool {
	// Forward to hook collector for Snapshot() data.
	if a.hooksCollector != nil {
		a.hooksCollector.ProcessEvent(eventName, payload)
	}

	// Forward to harness for AgentEvent emission.
	if a.harness != nil {
		return a.harness.HandleHookEvent(eventName, payload)
	}

	return a.hooksCollector != nil
}

// SetEventWriter sets the callback for persisting events. Must be called
// before Start. Typically used by session to wire an EventStore.
func (a *Agent) SetEventWriter(fn func(monitor.AgentEvent) error) {
	a.agentMonitor.SetEventWriter(fn)
}

// --- State accessors (delegate to monitor) ---

// State returns the current derived state and sub-state.
func (a *Agent) State() (monitor.State, monitor.SubState) {
	return a.agentMonitor.State()
}

// StateChanged returns a channel that is closed when the state changes.
func (a *Agent) StateChanged() <-chan struct{} {
	return a.agentMonitor.StateChanged()
}

// WaitForState blocks until the agent reaches the target state or ctx is cancelled.
func (a *Agent) WaitForState(ctx context.Context, target monitor.State) bool {
	return a.agentMonitor.WaitForState(ctx, target)
}

// StateDuration returns how long the agent has been in its current state.
func (a *Agent) StateDuration() time.Duration {
	return a.agentMonitor.StateDuration()
}

// SetExited transitions the agent to the Exited state.
// Called by Session when the child process exits.
func (a *Agent) SetExited() {
	a.agentMonitor.SetExited()
}

// --- Signals from Session ---

// HandleOutput signals that the child process produced output.
// Delegates to the harness (e.g. generic harness feeds output collector).
func (a *Agent) HandleOutput() {
	if a.harness != nil {
		a.harness.HandleOutput()
	}
}

// NoteInterrupt signals that a Ctrl+C was sent to the child process.
// Always safe to call — no-op if the hook collector is not active.
func (a *Agent) NoteInterrupt() {
	if a.hooksCollector != nil {
		a.hooksCollector.NoteInterrupt()
	}
}

// --- Delegators ---

// Harness returns the agent's harness.
func (a *Agent) Harness() harness.Harness {
	return a.harness
}

// Metrics returns a snapshot of the current OTEL metrics.
// Metrics are accumulated by the AgentMonitor from AgentEvents emitted by
// the harness.
func (a *Agent) Metrics() OtelMetricsSnapshot {
	ms := a.agentMonitor.Metrics()
	hasData := ms.InputTokens > 0 || ms.OutputTokens > 0 || ms.TurnCount > 0
	return OtelMetricsSnapshot{
		InputTokens:    ms.InputTokens,
		OutputTokens:   ms.OutputTokens,
		TotalTokens:    ms.InputTokens + ms.OutputTokens,
		TotalCostUSD:   ms.TotalCostUSD,
		ToolCounts:     ms.ToolCounts,
		EventsReceived: hasData,
	}
}

// OtelPort returns the port the OTEL collector is listening on.
func (a *Agent) OtelPort() int {
	type otelPorter interface {
		OtelPort() int
	}
	if a.harness != nil {
		if op, ok := a.harness.(otelPorter); ok {
			return op.OtelPort()
		}
	}
	return 0
}

// HookCollector returns the hook collector, or nil if not active.
func (a *Agent) HookCollector() *collector.HookCollector {
	return a.hooksCollector
}

// Stop cleans up agent resources and stops all goroutines.
func (a *Agent) Stop() {
	select {
	case <-a.stopCh:
		// already stopped
		return
	default:
		close(a.stopCh)
	}

	// Cancel harness/monitor context.
	if a.cancel != nil {
		a.cancel()
	}

	// Stop harness.
	if a.harness != nil {
		a.harness.Stop()
	}

	// Stop collectors.
	if a.hooksCollector != nil {
		a.hooksCollector.Stop()
	}
}
