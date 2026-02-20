package monitor

import "time"

// AgentEvent is the normalized event emitted by adapters.
type AgentEvent struct {
	Type      AgentEventType
	Timestamp time.Time
	Data      any // type-specific payload
}

// AgentEventType identifies the kind of agent event.
type AgentEventType int

const (
	EventSessionStarted    AgentEventType = iota
	EventTurnStarted
	EventTurnCompleted
	EventToolStarted
	EventToolCompleted
	EventApprovalRequested
	EventAgentMessage
	EventStateChange
	EventSessionEnded
)

// SessionStartedData is the payload for EventSessionStarted.
type SessionStartedData struct {
	ThreadID string
	Model    string
}

// TurnCompletedData is the payload for EventTurnCompleted.
type TurnCompletedData struct {
	TurnID       string
	InputTokens  int64
	OutputTokens int64
	CachedTokens int64
	CostUSD      float64
}

// ToolCompletedData is the payload for EventToolCompleted.
type ToolCompletedData struct {
	ToolName   string
	CallID     string
	DurationMs int64
	Success    bool
}

// StateChangeData is the payload for EventStateChange.
type StateChangeData struct {
	State    State
	SubState SubState
}
