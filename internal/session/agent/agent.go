package agent

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"h2/internal/activitylog"
	"h2/internal/session/agent/collector"
)

// Re-export State type, constants, and IdleThreshold from collector package
// so existing callers (agent.StateActive, agent.IdleThreshold, etc.)
// continue to work with zero changes.
type State = collector.State

const (
	StateInitialized = collector.StateInitialized
	StateActive      = collector.StateActive
	StateIdle        = collector.StateIdle
	StateExited      = collector.StateExited

	IdleThreshold = collector.IdleThreshold
)

// Agent manages collectors, state derivation, and metrics for a session.
type Agent struct {
	agentType AgentType

	// OTEL collector fields (active if AgentType.Collectors().Otel)
	metrics             *OtelMetrics
	listener            net.Listener
	server              *http.Server
	port                int
	otelMetricsReceived atomic.Bool // true after first /v1/metrics POST

	// Collectors
	outputCollector  *collector.OutputCollector
	otelCollector    *collector.OtelCollector
	hooksCollector   *collector.HookCollector
	primaryCollector collector.StateCollector

	// Activity logger (nil-safe; Nop logger when not set)
	activityLog *activitylog.Logger

	// Raw OTEL payload log files
	otelLogsFile    *os.File
	otelMetricsFile *os.File
	otelFileMu      sync.Mutex

	// Layer 2: Derived state
	mu             sync.Mutex
	state          State
	stateChangedAt time.Time
	stateCh        chan struct{} // closed on state change

	// Signals
	stopCh chan struct{}
}

// New creates a new Agent with the given agent type.
func New(agentType AgentType) *Agent {
	return &Agent{
		agentType:      agentType,
		metrics:        &OtelMetrics{},
		state:          StateInitialized,
		stateChangedAt: time.Now(),
		stateCh:        make(chan struct{}),
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

// SetOtelLogFiles opens the raw OTEL log files for appending.
// Must be called before StartCollectors.
func (a *Agent) SetOtelLogFiles(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create otel log dir: %w", err)
	}
	logsFile, err := os.OpenFile(filepath.Join(dir, "otel-logs.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open otel-logs.jsonl: %w", err)
	}
	metricsFile, err := os.OpenFile(filepath.Join(dir, "otel-metrics.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		logsFile.Close()
		return fmt.Errorf("open otel-metrics.jsonl: %w", err)
	}
	a.otelLogsFile = logsFile
	a.otelMetricsFile = metricsFile
	return nil
}

// StartCollectors starts the collectors enabled by the agent type and
// launches the internal watchState goroutine.
func (a *Agent) StartCollectors() error {
	cfg := a.agentType.Collectors()
	a.outputCollector = collector.NewOutputCollector()
	var primary collector.StateCollector = a.outputCollector

	if cfg.Otel {
		if err := a.StartOtelCollector(); err != nil {
			return err
		}
		a.otelCollector = collector.NewOtelCollector()
		primary = a.otelCollector
	}
	if cfg.Hooks {
		a.hooksCollector = collector.NewHookCollector(a.activityLog)
		primary = a.hooksCollector
	}
	a.primaryCollector = primary
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
	if a.outputCollector != nil {
		a.outputCollector.NoteOutput()
	}
}

// SetExited transitions the agent to the Exited state.
// Called by Session when the child process exits.
func (a *Agent) SetExited() {
	a.setState(StateExited)
}

// --- Internal watchState goroutine ---

// watchState forwards state from the primary collector to the Agent.
func (a *Agent) watchState() {
	for {
		select {
		case newState := <-a.primaryCollector.StateCh():
			a.mu.Lock()
			if a.state != StateExited {
				a.setStateLocked(newState)
			}
			a.mu.Unlock()
		case <-a.stopCh:
			return
		}
	}
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
func (a *Agent) HookCollector() *collector.HookCollector {
	return a.hooksCollector
}

// Stop cleans up agent resources and stops all goroutines.
func (a *Agent) Stop() {
	select {
	case <-a.stopCh:
		// already stopped
	default:
		close(a.stopCh)
	}
	if a.outputCollector != nil {
		a.outputCollector.Stop()
	}
	if a.otelCollector != nil {
		a.otelCollector.Stop()
	}
	if a.hooksCollector != nil {
		a.hooksCollector.Stop()
	}
	a.StopOtelCollector()

	// Log session summary before closing files.
	if a.metrics != nil {
		snap := a.metrics.Snapshot()
		a.ActivityLog().SessionSummary(snap.InputTokens, snap.OutputTokens, snap.TotalCostUSD, snap.APIRequestCount, snap.ToolResultCount, snap.LinesAdded, snap.LinesRemoved, snap.ToolCounts)
	}

	a.otelFileMu.Lock()
	if a.otelLogsFile != nil {
		a.otelLogsFile.Close()
		a.otelLogsFile = nil
	}
	if a.otelMetricsFile != nil {
		a.otelMetricsFile.Close()
		a.otelMetricsFile = nil
	}
	a.otelFileMu.Unlock()
}
