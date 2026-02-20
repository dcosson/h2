package monitor

import (
	"context"
	"sync"
	"time"
)

// AgentMonitor consumes AgentEvents from an adapter and maintains the
// agent's derived state, accumulated metrics, and other data that h2
// core queries. It does not own the adapter directly (to avoid circular
// imports); the caller connects the adapter's event output to the
// monitor's Events() channel.
type AgentMonitor struct {
	events     chan AgentEvent
	writeEvent func(AgentEvent) error // optional persistence callback

	mu             sync.RWMutex
	state          State
	subState       SubState
	stateChangedAt time.Time
	stateCh        chan struct{} // closed on state change

	threadID string
	model    string

	// Accumulated metrics from events.
	inputTokens  int64
	outputTokens int64
	cachedTokens int64
	totalCostUSD float64
	turnCount    int64
	toolCounts   map[string]int64
}

// Option configures an AgentMonitor.
type Option func(*AgentMonitor)

// WithEventWriter sets a callback that is invoked for every event
// processed by the monitor. Typically used to write events to an
// EventStore for persistence.
func WithEventWriter(fn func(AgentEvent) error) Option {
	return func(m *AgentMonitor) {
		m.writeEvent = fn
	}
}

// New creates an AgentMonitor.
func New(opts ...Option) *AgentMonitor {
	m := &AgentMonitor{
		events:         make(chan AgentEvent, 256),
		state:          StateInitialized,
		stateChangedAt: time.Now(),
		stateCh:        make(chan struct{}),
		toolCounts:     make(map[string]int64),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Events returns the channel that the adapter should send events to.
// The caller connects: go adapter.Start(ctx, monitor.Events())
func (m *AgentMonitor) Events() chan<- AgentEvent {
	return m.events
}

// Run processes events from the events channel until ctx is cancelled.
// Each event updates the monitor's state and metrics, and is optionally
// persisted via the event writer callback.
func (m *AgentMonitor) Run(ctx context.Context) error {
	for {
		select {
		case ev := <-m.events:
			m.processEvent(ev)
		case <-ctx.Done():
			return nil
		}
	}
}

// processEvent handles a single AgentEvent, updating state and metrics.
func (m *AgentMonitor) processEvent(ev AgentEvent) {
	// Persist the event if a writer is configured.
	if m.writeEvent != nil {
		m.writeEvent(ev) //nolint:errcheck // best-effort persistence
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	switch ev.Type {
	case EventSessionStarted:
		if data, ok := ev.Data.(SessionStartedData); ok {
			m.threadID = data.ThreadID
			m.model = data.Model
		}

	case EventTurnStarted:
		m.turnCount++

	case EventTurnCompleted:
		if data, ok := ev.Data.(TurnCompletedData); ok {
			m.inputTokens += data.InputTokens
			m.outputTokens += data.OutputTokens
			m.cachedTokens += data.CachedTokens
			m.totalCostUSD += data.CostUSD
		}

	case EventToolCompleted:
		if data, ok := ev.Data.(ToolCompletedData); ok {
			if data.ToolName != "" {
				m.toolCounts[data.ToolName]++
			}
		}

	case EventStateChange:
		if data, ok := ev.Data.(StateChangeData); ok {
			// Exited is sticky — don't allow state changes once exited.
			if m.state != StateExited {
				m.setStateLocked(data.State, data.SubState)
			}
		}

	case EventSessionEnded:
		m.setStateLocked(StateExited, SubStateNone)
	}
}

// setStateLocked updates state under the lock. Notifies waiters when
// the top-level State changes.
func (m *AgentMonitor) setStateLocked(newState State, newSubState SubState) {
	if m.state != newState {
		m.stateChangedAt = time.Now()
		close(m.stateCh)
		m.stateCh = make(chan struct{})
	}
	m.state = newState
	m.subState = newSubState
}

// --- Getters (thread-safe) ---

// State returns the current state and sub-state.
func (m *AgentMonitor) State() (State, SubState) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state, m.subState
}

// StateChanged returns a channel that is closed when the state changes.
func (m *AgentMonitor) StateChanged() <-chan struct{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stateCh
}

// WaitForState blocks until the monitor reaches the target state or
// ctx is cancelled.
func (m *AgentMonitor) WaitForState(ctx context.Context, target State) bool {
	for {
		st, _ := m.State()
		if st == target {
			return true
		}
		m.mu.RLock()
		ch := m.stateCh
		m.mu.RUnlock()

		select {
		case <-ch:
			continue
		case <-ctx.Done():
			return false
		}
	}
}

// StateDuration returns how long the monitor has been in its current state.
func (m *AgentMonitor) StateDuration() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return time.Since(m.stateChangedAt)
}

// ThreadID returns the session thread ID (set by EventSessionStarted).
func (m *AgentMonitor) ThreadID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.threadID
}

// Model returns the model name (set by EventSessionStarted).
func (m *AgentMonitor) Model() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.model
}

// Metrics returns a snapshot of accumulated metrics.
func (m *AgentMonitor) Metrics() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	toolCounts := make(map[string]int64, len(m.toolCounts))
	for k, v := range m.toolCounts {
		toolCounts[k] = v
	}

	return MetricsSnapshot{
		InputTokens:  m.inputTokens,
		OutputTokens: m.outputTokens,
		CachedTokens: m.cachedTokens,
		TotalCostUSD: m.totalCostUSD,
		TurnCount:    m.turnCount,
		ToolCounts:   toolCounts,
	}
}

// SetEventWriter sets the callback invoked for every event. Must be called
// before Run. Typically used to wire an EventStore for persistence.
func (m *AgentMonitor) SetEventWriter(fn func(AgentEvent) error) {
	m.writeEvent = fn
}

// SetExited transitions to the Exited state. Called externally when
// the child process exits.
func (m *AgentMonitor) SetExited() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setStateLocked(StateExited, SubStateNone)
}

// MetricsSnapshot is a point-in-time copy of accumulated metrics.
type MetricsSnapshot struct {
	InputTokens  int64
	OutputTokens int64
	CachedTokens int64
	TotalCostUSD float64
	TurnCount    int64
	ToolCounts   map[string]int64
}
