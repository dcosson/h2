package monitor

import (
	"encoding/json"
	"time"
)

// AgentEvent is the normalized event emitted by adapters.
type AgentEvent struct {
	Type      AgentEventType
	Timestamp time.Time
	Data      any // type-specific payload
}

// AgentEventType identifies the kind of agent event.
type AgentEventType int

const (
	EventSessionStarted AgentEventType = iota
	EventUserPrompt
	EventTurnCompleted
	EventToolStarted
	EventToolCompleted
	EventApprovalRequested
	EventAgentMessage
	EventStateChange
	EventSessionEnded
	EventUsageLimitInfo
	EventPermissionDecision
	EventSessionRotated
	EventSessionRestarted
	EventAuthErrorInfo
	EventServerErrorInfo
)

// String returns the event type name.
func (t AgentEventType) String() string {
	switch t {
	case EventSessionStarted:
		return "session_started"
	case EventUserPrompt:
		return "user_prompt"
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
	case EventUsageLimitInfo:
		return "usage_limit_info"
	case EventPermissionDecision:
		return "permission_decision"
	case EventSessionRotated:
		return "session_rotated"
	case EventSessionRestarted:
		return "session_restarted"
	case EventAuthErrorInfo:
		return "auth_error_info"
	case EventServerErrorInfo:
		return "server_error_info"
	default:
		return "unknown"
	}
}

// SessionStartedData is the payload for EventSessionStarted.
type SessionStartedData struct {
	SessionID string
	Model     string
}

// UnmarshalJSON accepts both "SessionID" and legacy "ThreadID" for backward
// compatibility with eventstore JSONL written before the rename.
func (d *SessionStartedData) UnmarshalJSON(data []byte) error {
	var raw struct {
		SessionID string `json:"SessionID"`
		ThreadID  string `json:"ThreadID"`
		Model     string `json:"Model"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	d.SessionID = raw.SessionID
	if d.SessionID == "" {
		d.SessionID = raw.ThreadID
	}
	d.Model = raw.Model
	return nil
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

// ToolStartedData is the payload for EventToolStarted.
type ToolStartedData struct {
	ToolName string
	CallID   string
}

// ApprovalRequestedData is the payload for EventApprovalRequested.
type ApprovalRequestedData struct {
	ToolName string
	CallID   string
}

// AgentMessageData is the payload for EventAgentMessage.
type AgentMessageData struct {
	Content string
}

// SessionEndedData is the payload for EventSessionEnded.
type SessionEndedData struct {
	Reason string
}

// StateChangeData is the payload for EventStateChange.
type StateChangeData struct {
	State    State
	SubState SubState
}

// UsageLimitData is the payload for EventUsageLimitInfo.
type UsageLimitData struct {
	ResetsAt time.Time // absolute time when the usage limit resets
	Message  string    // raw message from the harness (e.g. "resets 12pm (America/Los_Angeles)")
}

// PermissionDecisionData is the payload for EventPermissionDecision.
type PermissionDecisionData struct {
	ToolName    string `json:"tool_name"`
	Decision    string `json:"decision"`     // allow, deny, ask_user
	Reason      string `json:"reason"`       // human-readable reason from the engine
	ProcessedBy string `json:"processed_by"` // dcg, ai_reviewer, forced, none
	Role        string `json:"role"`         // role name from session metadata
}

// SessionRotatedData is the payload for EventSessionRotated.
type SessionRotatedData struct {
	OldProfile string `json:"old_profile"`
	NewProfile string `json:"new_profile"`
}

// SessionRestartedData is the payload for EventSessionRestarted.
type SessionRestartedData struct{}

// AuthErrorData is the payload for EventAuthErrorInfo.
type AuthErrorData struct {
	Message string // raw error message from the harness
}

// ServerErrorData is the payload for EventServerErrorInfo.
type ServerErrorData struct {
	StatusCode string // HTTP status code (e.g. "500", "502")
	Message    string // raw error message from the harness
}
