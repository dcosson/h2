package claude

import (
	"encoding/json"
	"time"

	"h2/internal/activitylog"
	"h2/internal/session/agent/monitor"
)

// HookHandler translates Claude Code hook events into AgentEvents.
// It maps lifecycle events (PreToolUse, PostToolUse, SessionStart, etc.)
// to state changes, tool events, and other normalized events.
type HookHandler struct {
	events      chan<- monitor.AgentEvent
	activityLog *activitylog.Logger
}

// NewHookHandler creates a HookHandler that emits events on the given channel.
func NewHookHandler(events chan<- monitor.AgentEvent, log *activitylog.Logger) *HookHandler {
	if log == nil {
		log = activitylog.Nop()
	}
	return &HookHandler{
		events:      events,
		activityLog: log,
	}
}

// ProcessEvent translates a Claude Code hook event into AgentEvent(s)
// and emits them on the events channel.
func (h *HookHandler) ProcessEvent(eventName string, payload json.RawMessage) {
	toolName := extractToolName(payload)
	sessionID := extractSessionID(payload)
	now := time.Now()

	// Log the hook event.
	if eventName == "permission_decision" {
		decision := extractDecision(payload)
		reason := extractReason(payload)
		h.activityLog.PermissionDecision(sessionID, toolName, decision, reason)
	} else {
		h.activityLog.HookEvent(sessionID, eventName, toolName)
	}

	// Translate to AgentEvents.
	switch eventName {
	case "UserPromptSubmit":
		h.emit(monitor.AgentEvent{
			Type:      monitor.EventTurnStarted,
			Timestamp: now,
		})
		h.emitStateChange(now, monitor.StateActive, monitor.SubStateThinking)

	case "PreToolUse":
		h.emit(monitor.AgentEvent{
			Type:      monitor.EventToolStarted,
			Timestamp: now,
			Data: monitor.ToolCompletedData{
				ToolName: toolName,
			},
		})
		h.emitStateChange(now, monitor.StateActive, monitor.SubStateToolUse)

	case "PostToolUse":
		h.emit(monitor.AgentEvent{
			Type:      monitor.EventToolCompleted,
			Timestamp: now,
			Data: monitor.ToolCompletedData{
				ToolName: toolName,
				Success:  true,
			},
		})
		h.emitStateChange(now, monitor.StateActive, monitor.SubStateThinking)

	case "PermissionRequest":
		h.emit(monitor.AgentEvent{
			Type:      monitor.EventApprovalRequested,
			Timestamp: now,
		})
		h.emitStateChange(now, monitor.StateActive, monitor.SubStateWaitingForPermission)

	case "permission_decision":
		decision := extractDecision(payload)
		if decision == "ask_user" {
			h.emitStateChange(now, monitor.StateActive, monitor.SubStateWaitingForPermission)
		} else {
			// Permission granted — tool is about to execute.
			h.emitStateChange(now, monitor.StateActive, monitor.SubStateToolUse)
		}

	case "PreCompact":
		h.emitStateChange(now, monitor.StateActive, monitor.SubStateCompacting)

	case "SessionStart":
		h.emitStateChange(now, monitor.StateIdle, monitor.SubStateNone)

	case "Stop", "Interrupt":
		h.emitStateChange(now, monitor.StateIdle, monitor.SubStateNone)

	case "SessionEnd":
		h.emit(monitor.AgentEvent{
			Type:      monitor.EventSessionEnded,
			Timestamp: now,
		})
	}
}

// NoteInterrupt signals that a Ctrl+C was sent. Treated as an idle transition.
func (h *HookHandler) NoteInterrupt() {
	h.emitStateChange(time.Now(), monitor.StateIdle, monitor.SubStateNone)
}

func (h *HookHandler) emitStateChange(ts time.Time, state monitor.State, subState monitor.SubState) {
	h.emit(monitor.AgentEvent{
		Type:      monitor.EventStateChange,
		Timestamp: ts,
		Data: monitor.StateChangeData{
			State:    state,
			SubState: subState,
		},
	})
}

func (h *HookHandler) emit(ev monitor.AgentEvent) {
	select {
	case h.events <- ev:
	default:
	}
}

// --- Payload extraction helpers ---

type hookPayload struct {
	ToolName  string `json:"tool_name"`
	SessionID string `json:"session_id"`
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`
}

func extractToolName(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var p hookPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	return p.ToolName
}

func extractSessionID(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var p hookPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	return p.SessionID
}

func extractDecision(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var p hookPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	return p.Decision
}

func extractReason(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var p hookPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	return p.Reason
}
