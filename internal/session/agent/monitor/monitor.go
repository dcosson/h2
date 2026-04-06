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

	sessionID          string
	onSessionStarted   func(SessionStartedData)
	onUsageLimit       func(UsageLimitData)
	onAuthError        func(AuthErrorData)
	onAuthErrorCleared func()
	model              string

	// Accumulated metrics from events.
	inputTokens     int64
	outputTokens    int64
	cachedTokens    int64
	totalCostUSD    float64
	turnCount       int64
	userPromptCount int64
	toolCounts      map[string]int64

	lastToolName        string
	lastActivityAt      time.Time
	toolUseCount        int64
	blockedOnPermission bool
	blockedToolName     string

	usageLimitResetsAt *time.Time
	usageLimitMessage  string
	authErrorMessage   string

	// subscribers receive a copy of every event processed by the monitor.
	// Protected by subscribersMu (separate from mu to avoid contention).
	subscribersMu sync.Mutex
	subscribers   []chan<- AgentEvent
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

	// Fan out to subscribers (non-blocking; drops if subscriber is slow).
	m.subscribersMu.Lock()
	for _, ch := range m.subscribers {
		select {
		case ch <- ev:
		default:
		}
	}
	m.subscribersMu.Unlock()

	// Capture callbacks+data under lock, invoke after unlock to avoid
	// blocking event processing or risking deadlock if callback calls
	// monitor getters.
	var sessionStartedCb func(SessionStartedData)
	var sessionStartedData SessionStartedData
	var usageLimitCb func(UsageLimitData)
	var usageLimitData UsageLimitData
	var authErrorCb func(AuthErrorData)
	var authErrorData AuthErrorData
	var authErrorClearCb func()

	m.mu.Lock()
	if !ev.Timestamp.IsZero() {
		m.lastActivityAt = ev.Timestamp
	} else {
		m.lastActivityAt = time.Now()
	}

	switch ev.Type {
	case EventSessionStarted:
		if data, ok := ev.Data.(SessionStartedData); ok {
			m.sessionID = data.SessionID
			m.model = data.Model
			sessionStartedCb = m.onSessionStarted
			sessionStartedData = data
		}

	case EventUserPrompt:
		m.userPromptCount++

	case EventTurnCompleted:
		m.turnCount++
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
				m.lastToolName = data.ToolName
			}
		}
		m.blockedOnPermission = false
		m.blockedToolName = ""

	case EventToolStarted:
		if data, ok := ev.Data.(ToolStartedData); ok {
			if data.ToolName != "" {
				m.lastToolName = data.ToolName
			}
		}
		m.toolUseCount++
		m.blockedOnPermission = false
		m.blockedToolName = ""

	case EventApprovalRequested:
		if data, ok := ev.Data.(ApprovalRequestedData); ok {
			m.blockedToolName = data.ToolName
			if data.ToolName != "" {
				m.lastToolName = data.ToolName
			}
		}

	case EventStateChange:
		if data, ok := ev.Data.(StateChangeData); ok {
			// Exited is sticky — don't allow state changes once exited.
			if m.state != StateExited {
				m.setStateLocked(data.State, data.SubState)
			}
			if data.SubState == SubStateBlockedOnPermission {
				m.blockedOnPermission = true
			} else if data.SubState != SubStateBlockedOnPermission {
				m.blockedOnPermission = false
				m.blockedToolName = ""
			}
			// Clear usage limit info when leaving usage_limit state.
			if data.SubState != SubStateUsageLimit {
				m.usageLimitResetsAt = nil
				m.usageLimitMessage = ""
			}
			// Clear auth error info when leaving auth_error state.
			if data.SubState != SubStateAuthError && m.authErrorMessage != "" {
				m.authErrorMessage = ""
				authErrorClearCb = m.onAuthErrorCleared
			}
		}

	case EventUsageLimitInfo:
		if data, ok := ev.Data.(UsageLimitData); ok {
			m.usageLimitResetsAt = &data.ResetsAt
			m.usageLimitMessage = data.Message
			usageLimitCb = m.onUsageLimit
			usageLimitData = data
		}

	case EventAuthErrorInfo:
		if data, ok := ev.Data.(AuthErrorData); ok {
			m.authErrorMessage = data.Message
			authErrorCb = m.onAuthError
			authErrorData = data
		}

	case EventSessionEnded:
		m.setStateLocked(StateExited, SubStateNone)
	}

	m.mu.Unlock()

	// Invoke callbacks outside the lock so they can do I/O (e.g. persist
	// RuntimeConfig, write ratelimit.json) without blocking event processing.
	if sessionStartedCb != nil {
		sessionStartedCb(sessionStartedData)
	}
	if usageLimitCb != nil {
		usageLimitCb(usageLimitData)
	}
	if authErrorCb != nil {
		authErrorCb(authErrorData)
	}
	if authErrorClearCb != nil {
		authErrorClearCb()
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

// SessionID returns the harness session ID (set by EventSessionStarted).
func (m *AgentMonitor) SessionID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessionID
}

// SetOnSessionStarted sets a callback invoked when EventSessionStarted is
// processed. The daemon uses this to persist the harness session ID to the
// RuntimeConfig file. Must be called before Run.
func (m *AgentMonitor) SetOnSessionStarted(fn func(SessionStartedData)) {
	m.onSessionStarted = fn
}

// SetOnUsageLimit sets a callback invoked when EventUsageLimitInfo is
// processed. The daemon uses this to persist rate limit info to disk.
// Must be called before Run.
func (m *AgentMonitor) SetOnUsageLimit(fn func(UsageLimitData)) {
	m.onUsageLimit = fn
}

// SetOnAuthError sets a callback invoked when EventAuthErrorInfo is
// processed. The daemon uses this to persist auth error info to disk.
// Must be called before Run.
func (m *AgentMonitor) SetOnAuthError(fn func(AuthErrorData)) {
	m.onAuthError = fn
}

// SetOnAuthErrorCleared sets a callback invoked when the agent transitions
// out of auth_error state (e.g. after a successful /login). The daemon
// uses this to remove the autherror.json file. Must be called before Run.
func (m *AgentMonitor) SetOnAuthErrorCleared(fn func()) {
	m.onAuthErrorCleared = fn
}

// Model returns the model name (set by EventSessionStarted).
func (m *AgentMonitor) Model() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.model
}

// MetricsSnapshot returns a snapshot of accumulated metrics.
func (m *AgentMonitor) MetricsSnapshot() AgentMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()

	toolCounts := make(map[string]int64, len(m.toolCounts))
	for k, v := range m.toolCounts {
		toolCounts[k] = v
	}

	return AgentMetrics{
		InputTokens:     m.inputTokens,
		OutputTokens:    m.outputTokens,
		TotalTokens:     m.inputTokens + m.outputTokens,
		CachedTokens:    m.cachedTokens,
		TotalCostUSD:    m.totalCostUSD,
		TurnCount:       m.turnCount,
		UserPromptCount: m.userPromptCount,
		ToolCounts:      toolCounts,
		EventsReceived:  m.inputTokens > 0 || m.outputTokens > 0 || m.turnCount > 0 || m.userPromptCount > 0 || len(toolCounts) > 0,
	}
}

// SetEventWriter sets the callback invoked for every event. Must be called
// before Run. Typically used to wire an EventStore for persistence.
func (m *AgentMonitor) SetEventWriter(fn func(AgentEvent) error) {
	m.writeEvent = fn
}

// Subscribe returns a channel that receives a copy of every event processed
// by the monitor. The channel is buffered to avoid blocking event processing.
// Must be called before Run. The caller is responsible for draining the channel.
func (m *AgentMonitor) Subscribe() <-chan AgentEvent {
	ch := make(chan AgentEvent, 256)
	m.subscribersMu.Lock()
	m.subscribers = append(m.subscribers, ch)
	m.subscribersMu.Unlock()
	return ch
}

// Inject sends an event into the monitor's processing pipeline from
// outside the harness. Used by the session layer to emit events like
// session_rotated and session_restarted that originate above the harness.
// Non-blocking: drops the event if the channel is full.
func (m *AgentMonitor) Inject(ev AgentEvent) {
	select {
	case m.events <- ev:
	default:
	}
}

// SetExited transitions to the Exited state. Called externally when
// the child process exits.
func (m *AgentMonitor) SetExited() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setStateLocked(StateExited, SubStateNone)
}

// ResetForRelaunch resets the monitor state back to Initialized so a
// relaunched child process can drive state transitions normally.
func (m *AgentMonitor) ResetForRelaunch() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setStateLocked(StateInitialized, SubStateNone)
	m.blockedOnPermission = false
	m.blockedToolName = ""
}

// AgentMetrics is a point-in-time copy of accumulated metrics.
type AgentMetrics struct {
	InputTokens     int64
	OutputTokens    int64
	TotalTokens     int64
	CachedTokens    int64
	TotalCostUSD    float64
	TurnCount       int64
	UserPromptCount int64
	ToolCounts      map[string]int64
	EventsReceived  bool
}

// ActivitySnapshot contains monitor-derived activity state commonly used in status surfaces.
type ActivitySnapshot struct {
	LastToolName        string
	LastActivityAt      time.Time
	ToolUseCount        int64
	BlockedOnPermission bool
	BlockedToolName     string
}

// UsageLimitResetsAt returns the time at which the usage limit resets, if known.
func (m *AgentMonitor) UsageLimitResetsAt() *time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.usageLimitResetsAt
}

// UsageLimitMessage returns the raw rate limit message from the harness.
func (m *AgentMonitor) UsageLimitMessage() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.usageLimitMessage
}

// AuthErrorMessage returns the auth error message from the harness.
func (m *AgentMonitor) AuthErrorMessage() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.authErrorMessage
}

// Activity returns a snapshot of activity fields derived from normalized events.
func (m *AgentMonitor) Activity() ActivitySnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return ActivitySnapshot{
		LastToolName:        m.lastToolName,
		LastActivityAt:      m.lastActivityAt,
		ToolUseCount:        m.toolUseCount,
		BlockedOnPermission: m.blockedOnPermission,
		BlockedToolName:     m.blockedToolName,
	}
}
