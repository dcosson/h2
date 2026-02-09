package agent

import (
	"context"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"h2/internal/activitylog"
)

// IdleThreshold is how long without activity before an agent is considered idle.
const IdleThreshold = 2 * time.Second

// State represents the derived activity state of an agent.
type State int

const (
	StateActive State = iota // receiving activity signals
	StateIdle                // no activity for IdleThreshold
	StateExited              // child process exited
)

// String returns a human-readable name for the state.
func (s State) String() string {
	switch s {
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

// IdleAuthority tracks which data source is authoritative for idle/active decisions.
// Once a higher-fidelity source fires, it becomes the sole authority.
type IdleAuthority int

const (
	AuthorityOutputTimer IdleAuthority = iota // fallback: child PTY output timing
	AuthorityOtel                             // OTEL events
	AuthorityHooks                            // Claude Code hook events (highest fidelity)
)

// Agent manages collectors, state derivation, and metrics for a session.
type Agent struct {
	agentType AgentType

	// OTEL collector fields (active if AgentType.Collectors().Otel)
	metrics              *OtelMetrics
	listener             net.Listener
	server               *http.Server
	port                 int
	otelNotify           chan struct{}   // buffered(1), signaled on OTEL event
	otelMetricsReceived  atomic.Bool    // true after first /v1/metrics POST

	// Hook collector (nil if not active)
	hooks *HookCollector

	// Activity logger (nil-safe; Nop logger when not set)
	activityLog *activitylog.Logger

	// Layer 2: Derived state
	mu             sync.Mutex
	state          State
	stateChangedAt time.Time
	stateCh        chan struct{} // closed on state change

	idleAuthority IdleAuthority

	// Signals
	outputNotify chan struct{} // buffered(1), signaled by Session on child output
	stopCh       chan struct{}
}

// New creates a new Agent with the given agent type.
func New(agentType AgentType) *Agent {
	return &Agent{
		agentType:      agentType,
		metrics:        &OtelMetrics{},
		otelNotify:     make(chan struct{}, 1),
		state:          StateActive,
		stateChangedAt: time.Now(),
		stateCh:        make(chan struct{}),
		outputNotify:   make(chan struct{}, 1),
		stopCh:         make(chan struct{}),
	}
}

// SetActivityLog sets the activity logger for this agent.
// Must be called before StartCollectors.
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

// StartCollectors starts the collectors enabled by the agent type and
// launches the internal watchState goroutine.
func (a *Agent) StartCollectors() error {
	cfg := a.agentType.Collectors()
	if cfg.Otel {
		if err := a.StartOtelCollector(); err != nil {
			return err
		}
	}
	if cfg.Hooks {
		a.hooks = NewHookCollector(a.activityLog)
	}
	go a.watchState()
	return nil
}

// --- State accessors ---

// State returns the current derived state.
func (a *Agent) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

// StateChanged returns a channel that is closed when the state changes.
func (a *Agent) StateChanged() <-chan struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stateCh
}

// WaitForState blocks until the agent reaches the target state or ctx is cancelled.
func (a *Agent) WaitForState(ctx context.Context, target State) bool {
	for {
		a.mu.Lock()
		if a.state == target {
			a.mu.Unlock()
			return true
		}
		ch := a.stateCh
		a.mu.Unlock()

		select {
		case <-ch:
			continue
		case <-ctx.Done():
			return false
		}
	}
}

// StateDuration returns how long the agent has been in its current state.
func (a *Agent) StateDuration() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return time.Since(a.stateChangedAt)
}

// setState updates the state and notifies waiters. Caller must NOT hold mu.
func (a *Agent) setState(newState State) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.setStateLocked(newState)
}

// setStateLocked updates state while mu is already held.
func (a *Agent) setStateLocked(newState State) {
	if a.state == newState {
		return
	}
	prev := a.state
	a.state = newState
	a.stateChangedAt = time.Now()
	close(a.stateCh)
	a.stateCh = make(chan struct{})
	a.ActivityLog().StateChange(prev.String(), newState.String())
}

// --- Signals from Session ---

// NoteOutput signals that the child process produced output.
// Called by Session from the PTY output callback.
func (a *Agent) NoteOutput() {
	select {
	case a.outputNotify <- struct{}{}:
	default:
	}
}

// SetExited transitions the agent to the Exited state.
// Called by Session when the child process exits.
func (a *Agent) SetExited() {
	a.setState(StateExited)
}

// --- Internal watchState goroutine ---

// watchState is the Agent's internal goroutine that derives state from
// collector signals and child output, using committed authority.
func (a *Agent) watchState() {
	idleTimer := time.NewTimer(IdleThreshold)
	defer idleTimer.Stop()

	for {
		select {
		case <-a.outputNotify:
			a.handleCollectorActivity(AuthorityOutputTimer, idleTimer)

		case <-a.otelNotifyCh():
			a.handleCollectorActivity(AuthorityOtel, idleTimer)

		case event := <-a.hooksEventCh():
			a.handleHookEvent(event, idleTimer)

		case <-idleTimer.C:
			a.mu.Lock()
			if a.state != StateExited {
				a.setStateLocked(StateIdle)
			}
			a.mu.Unlock()

		case <-a.stopCh:
			return
		}
	}
}

// handleCollectorActivity promotes authority if needed and resets idle timer
// if this source is the current authority (or higher).
func (a *Agent) handleCollectorActivity(source IdleAuthority, idleTimer *time.Timer) {
	a.mu.Lock()
	if a.state == StateExited {
		a.mu.Unlock()
		return
	}
	if source > a.idleAuthority {
		a.idleAuthority = source
	}
	if source >= a.idleAuthority {
		a.setStateLocked(StateActive)
		a.mu.Unlock()
		resetTimer(idleTimer, IdleThreshold)
	} else {
		a.mu.Unlock()
	}
}

// handleHookEvent processes a hook event, updating state based on the event type.
func (a *Agent) handleHookEvent(eventName string, idleTimer *time.Timer) {
	a.mu.Lock()
	if a.state == StateExited {
		a.mu.Unlock()
		return
	}

	// Promote to hook authority on first hook event.
	if a.idleAuthority < AuthorityHooks {
		a.idleAuthority = AuthorityHooks
	}

	switch eventName {
	case "SessionStart":
		// Commit authority, no state change.
	case "UserPromptSubmit":
		a.setStateLocked(StateActive)
	case "PreToolUse", "PostToolUse":
		a.setStateLocked(StateActive)
	case "PermissionRequest":
		a.setStateLocked(StateActive)
	case "Stop":
		a.setStateLocked(StateIdle)
	case "SessionEnd":
		a.setStateLocked(StateExited)
	}
	a.mu.Unlock()

	// Reset idle timer for events that indicate activity.
	switch eventName {
	case "UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest":
		resetTimer(idleTimer, IdleThreshold)
	}
}

// otelNotifyCh returns the OTEL notify channel, or nil if OTEL is not active.
// A nil channel blocks forever in select, effectively disabling that case.
func (a *Agent) otelNotifyCh() <-chan struct{} {
	return a.otelNotify
}

// hooksEventCh returns the hook collector's event channel, or nil if hooks
// are not active.
func (a *Agent) hooksEventCh() <-chan string {
	if a.hooks != nil {
		return a.hooks.EventCh()
	}
	return nil
}

// resetTimer safely resets a timer.
func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

// --- Delegators ---

// AgentType returns the agent type.
func (a *Agent) AgentType() AgentType {
	return a.agentType
}

// PrependArgs returns extra args to inject before the user's args.
func (a *Agent) PrependArgs(sessionID string) []string {
	if a.agentType == nil {
		return nil
	}
	return a.agentType.PrependArgs(sessionID)
}

// ChildEnv returns environment variables to inject into the child process.
func (a *Agent) ChildEnv() map[string]string {
	if a.agentType == nil {
		return nil
	}
	return a.agentType.ChildEnv(&CollectorPorts{OtelPort: a.port})
}

// Metrics returns a snapshot of the current OTEL metrics.
func (a *Agent) Metrics() OtelMetricsSnapshot {
	if a.metrics == nil {
		return OtelMetricsSnapshot{}
	}
	return a.metrics.Snapshot()
}

// OtelPort returns the port the OTEL collector is listening on.
func (a *Agent) OtelPort() int {
	return a.port
}

// HookCollector returns the hook collector, or nil if not active.
func (a *Agent) HookCollector() *HookCollector {
	return a.hooks
}

// Stop cleans up agent resources and stops all goroutines.
func (a *Agent) Stop() {
	select {
	case <-a.stopCh:
		// already stopped
	default:
		close(a.stopCh)
	}
	a.StopOtelCollector()
}
