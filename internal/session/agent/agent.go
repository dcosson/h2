package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"h2/internal/activitylog"
	"h2/internal/session/agent/adapter"
	"h2/internal/session/agent/collector"
	"h2/internal/session/agent/monitor"
	"h2/internal/session/agent/shared/outputcollector"
)

// Agent manages state, metrics, and lifecycle for a session's agent process.
// It delegates telemetry and state derivation to an AgentAdapter + AgentMonitor
// pipeline for supported agents (Claude Code, Codex). Generic agents use an
// output collector bridged to the monitor for output-based state detection.
type Agent struct {
	agentType AgentType

	// Adapter pattern: adapter translates agent-specific telemetry into
	// normalized AgentEvents; monitor consumes those events and maintains
	// derived state + metrics.
	adapter      adapter.AgentAdapter
	agentMonitor *monitor.AgentMonitor
	cancel       context.CancelFunc

	// Output collector: always present for NoteOutput signal.
	// For generic agents (no adapter), this is the primary state source.
	outputCollector *outputcollector.Collector

	// Hook collector: kept for backward compat Snapshot() data.
	// Hook events are forwarded to both adapter and hooksCollector.
	hooksCollector *collector.HookCollector

	// Legacy OTEL metrics (populated on legacy path when no adapter).
	metrics *OtelMetrics

	// Activity logger (nil-safe; Nop logger when not set).
	activityLog *activitylog.Logger

	// Raw OTEL payload log files (legacy path).
	otelLogsFile    *os.File
	otelMetricsFile *os.File
	otelFileMu      sync.Mutex

	// Signals
	stopCh chan struct{}
}

// New creates a new Agent with the given agent type.
func New(agentType AgentType) *Agent {
	return &Agent{
		agentType:    agentType,
		agentMonitor: monitor.New(),
		metrics:      &OtelMetrics{},
		stopCh:       make(chan struct{}),
	}
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

// SetOtelLogFiles opens the raw OTEL log files for appending.
// Must be called before PrepareForLaunch.
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

// PrepareForLaunch creates the adapter (if applicable) and returns env vars
// and CLI args to inject into the child process. Must be called after
// SetActivityLog and before Start.
func (a *Agent) PrepareForLaunch(agentName, sessionID string) (adapter.LaunchConfig, error) {
	a.adapter = a.agentType.NewAdapter(a.ActivityLog())

	if a.adapter == nil {
		// Generic agent: no adapter, no special env/args.
		return adapter.LaunchConfig{}, nil
	}

	// Create hook collector for backward compat Snapshot() data.
	a.hooksCollector = collector.NewHookCollector(a.ActivityLog())

	return a.adapter.PrepareForLaunch(agentName, sessionID)
}

// Start begins the adapter+monitor pipeline (for adapted agents) or the
// output collector bridge (for generic agents). Must be called after
// PrepareForLaunch and after the child process starts.
func (a *Agent) Start(ctx context.Context) {
	ctx, a.cancel = context.WithCancel(ctx)

	// Always create output collector for NoteOutput.
	a.outputCollector = outputcollector.New(monitor.IdleThreshold)

	if a.adapter != nil {
		// Adapted agent: adapter → monitor pipeline.
		go a.adapter.Start(ctx, a.agentMonitor.Events())
		go a.agentMonitor.Run(ctx)
	} else {
		// Generic agent: bridge output collector state to monitor.
		go a.agentMonitor.Run(ctx)
		go a.bridgeOutputToMonitor(ctx)
	}
}

// bridgeOutputToMonitor forwards output collector state changes to the
// monitor as AgentEvents. Used for generic agents.
func (a *Agent) bridgeOutputToMonitor(ctx context.Context) {
	for {
		select {
		case su := <-a.outputCollector.StateCh():
			select {
			case a.agentMonitor.Events() <- monitor.AgentEvent{
				Type:      monitor.EventStateChange,
				Timestamp: time.Now(),
				Data:      monitor.StateChangeData{State: su.State, SubState: su.SubState},
			}:
			case <-ctx.Done():
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// HandleHookEvent processes a hook event, forwarding to both the adapter
// (for event emission) and the hook collector (for backward compat Snapshot data).
func (a *Agent) HandleHookEvent(eventName string, payload json.RawMessage) bool {
	// Forward to hook collector for Snapshot() data.
	if a.hooksCollector != nil {
		a.hooksCollector.ProcessEvent(eventName, payload)
	}

	// Forward to adapter for AgentEvent emission.
	if a.adapter != nil {
		return a.adapter.HandleHookEvent(eventName, payload)
	}

	return a.hooksCollector != nil
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

// NoteOutput signals that the child process produced output.
// Called by Session from the PTY output callback.
func (a *Agent) NoteOutput() {
	if a.outputCollector != nil {
		a.outputCollector.NoteOutput()
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

// AgentType returns the agent type.
func (a *Agent) AgentType() AgentType {
	return a.agentType
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
	// Check adapter for OTEL port (type-assert for OtelPort method).
	type otelPorter interface {
		OtelPort() int
	}
	if a.adapter != nil {
		if op, ok := a.adapter.(otelPorter); ok {
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

	// Cancel adapter/monitor context.
	if a.cancel != nil {
		a.cancel()
	}

	// Stop adapter.
	if a.adapter != nil {
		a.adapter.Stop()
	}

	// Stop collectors.
	if a.outputCollector != nil {
		a.outputCollector.Stop()
	}
	if a.hooksCollector != nil {
		a.hooksCollector.Stop()
	}

	// Close raw OTEL log files.
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
