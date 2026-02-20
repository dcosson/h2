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

// String returns the event type name.
func (t AgentEventType) String() string {
	switch t {
	case EventSessionStarted:
		return "session_started"
	case EventTurnStarted:
		return "turn_started"
	case EventTurnCompleted:
		return "turn_completed"
	case EventToolStarted:
		return "tool_started"
	case EventToolCompleted:
		return "tool_completed"
	case EventApprovalRequested:
		return "approval_requested"
	case EventAgentMessage:
		return "agent_message"
	case EventStateChange:
		return "state_change"
	case EventSessionEnded:
		return "session_ended"
	default:
		return "unknown"
	}
}

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
