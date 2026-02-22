package claude

import (
	"encoding/json"
	"strconv"
	"time"

	"h2/internal/activitylog"
	"h2/internal/session/agent/monitor"
)

// EventHandler coalesces Claude telemetry sources (OTEL logs, hooks,
// and session JSONL lines) into normalized AgentEvents.
type EventHandler struct {
	events      chan<- monitor.AgentEvent
	activityLog *activitylog.Logger
}

// NewEventHandler creates an EventHandler that emits events on the given channel.
func NewEventHandler(events chan<- monitor.AgentEvent, log *activitylog.Logger) *EventHandler {
	if log == nil {
		log = activitylog.Nop()
	}
	return &EventHandler{events: events, activityLog: log}
}

// OnLogs is the callback for /v1/logs payloads from the OTEL server.
func (h *EventHandler) OnLogs(body []byte) {
	var payload otelLogsPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return
	}
	h.processLogs(payload)
}

// OnMetrics is the callback for /v1/metrics payloads from the OTEL server.
// Cumulative metrics are handled by monitor metrics aggregation.
func (h *EventHandler) OnMetrics(body []byte) {}

func (h *EventHandler) processLogs(payload otelLogsPayload) {
	now := time.Now()
	for _, rl := range payload.ResourceLogs {
		for _, sl := range rl.ScopeLogs {
			for _, lr := range sl.LogRecords {
				eventName := getAttr(lr.Attributes, "event.name")
				if eventName == "" {
					continue
				}
				h.processLogRecord(eventName, lr, now)
			}
		}
	}
}

func (h *EventHandler) processLogRecord(eventName string, lr otelLogRecord, ts time.Time) {
	switch eventName {
	case "api_request":
		input := getIntAttr(lr.Attributes, "input_tokens")
		output := getIntAttr(lr.Attributes, "output_tokens")
		cost := getFloatAttr(lr.Attributes, "cost_usd")
		if input > 0 || output > 0 || cost > 0 {
			h.emit(monitor.AgentEvent{
				Type:      monitor.EventTurnCompleted,
				Timestamp: ts,
				Data: monitor.TurnCompletedData{
					InputTokens:  input,
					OutputTokens: output,
					CostUSD:      cost,
				},
			})
		}
	case "tool_result":
		toolName := getAttr(lr.Attributes, "tool_name")
		if toolName != "" {
			h.emit(monitor.AgentEvent{
				Type:      monitor.EventToolCompleted,
				Timestamp: ts,
				Data:      monitor.ToolCompletedData{ToolName: toolName, Success: true},
			})
		}
	}
}

// ProcessHookEvent translates Claude hook events into AgentEvents.
func (h *EventHandler) ProcessHookEvent(eventName string, payload json.RawMessage) bool {
	toolName := extractToolName(payload)
	sessionID := extractSessionID(payload)
	now := time.Now()

	if eventName == "permission_decision" {
		decision := extractDecision(payload)
		reason := extractReason(payload)
		h.activityLog.PermissionDecision(sessionID, toolName, decision, reason)
	} else {
		h.activityLog.HookEvent(sessionID, eventName, toolName)
	}

	switch eventName {
	case "UserPromptSubmit":
		h.emit(monitor.AgentEvent{Type: monitor.EventUserPrompt, Timestamp: now})
		h.emitStateChange(now, monitor.StateActive, monitor.SubStateThinking)

	case "PreToolUse":
		h.emit(monitor.AgentEvent{
			Type:      monitor.EventToolStarted,
			Timestamp: now,
			Data:      monitor.ToolStartedData{ToolName: toolName},
		})
		h.emitStateChange(now, monitor.StateActive, monitor.SubStateToolUse)

	case "PostToolUse":
		h.emit(monitor.AgentEvent{
			Type:      monitor.EventToolCompleted,
			Timestamp: now,
			Data:      monitor.ToolCompletedData{ToolName: toolName, Success: true},
		})
		h.emitStateChange(now, monitor.StateActive, monitor.SubStateThinking)

	case "PermissionRequest":
		h.emit(monitor.AgentEvent{
			Type:      monitor.EventApprovalRequested,
			Timestamp: now,
			Data:      monitor.ApprovalRequestedData{ToolName: toolName},
		})
		h.emitStateChange(now, monitor.StateActive, monitor.SubStateWaitingForPermission)

	case "permission_decision":
		decision := extractDecision(payload)
		if decision == "ask_user" {
			h.emitStateChange(now, monitor.StateActive, monitor.SubStateWaitingForPermission)
		} else {
			h.emitStateChange(now, monitor.StateActive, monitor.SubStateToolUse)
		}

	case "PreCompact":
		h.emitStateChange(now, monitor.StateActive, monitor.SubStateCompacting)

	case "SessionStart":
		h.emitStateChange(now, monitor.StateIdle, monitor.SubStateNone)

	case "Stop", "Interrupt":
		h.emitStateChange(now, monitor.StateIdle, monitor.SubStateNone)

	case "SessionEnd":
		h.emit(monitor.AgentEvent{Type: monitor.EventSessionEnded, Timestamp: now})

	default:
		return false
	}
	return true
}

// OnSessionLogLine parses one Claude session JSONL line.
func (h *EventHandler) OnSessionLogLine(line []byte) {
	if ev, ok := parseSessionLine(line); ok {
		h.emit(ev)
	}
}

func (h *EventHandler) emitStateChange(ts time.Time, state monitor.State, subState monitor.SubState) {
	h.emit(monitor.AgentEvent{
		Type:      monitor.EventStateChange,
		Timestamp: ts,
		Data:      monitor.StateChangeData{State: state, SubState: subState},
	})
}

func (h *EventHandler) emit(ev monitor.AgentEvent) {
	select {
	case h.events <- ev:
	default:
	}
}

// --- hook payload helpers ---

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

// --- session log parsing ---

type sessionLogEntry struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message,omitempty"`
}

type sessionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
}

func parseSessionLine(line []byte) (monitor.AgentEvent, bool) {
	var entry sessionLogEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return monitor.AgentEvent{}, false
	}
	if entry.Type != "assistant" {
		return monitor.AgentEvent{}, false
	}

	var msg sessionMessage
	if len(entry.Message) > 0 {
		if err := json.Unmarshal(entry.Message, &msg); err != nil {
			return monitor.AgentEvent{}, false
		}
	}
	if msg.Content == "" {
		return monitor.AgentEvent{}, false
	}

	return monitor.AgentEvent{
		Type:      monitor.EventAgentMessage,
		Timestamp: time.Now(),
		Data:      monitor.AgentMessageData{Content: msg.Content},
	}, true
}

// --- OTEL JSON types + helpers ---

type otelLogsPayload struct {
	ResourceLogs []otelResourceLogs `json:"resourceLogs"`
}

type otelResourceLogs struct {
	ScopeLogs []otelScopeLogs `json:"scopeLogs"`
}

type otelScopeLogs struct {
	LogRecords []otelLogRecord `json:"logRecords"`
}

type otelLogRecord struct {
	Attributes []otelAttribute `json:"attributes"`
}

type otelAttribute struct {
	Key   string        `json:"key"`
	Value otelAttrValue `json:"value"`
}

type otelAttrValue struct {
	StringValue string          `json:"stringValue,omitempty"`
	IntValue    json.RawMessage `json:"intValue,omitempty"`
}

func getAttr(attrs []otelAttribute, key string) string {
	for _, a := range attrs {
		if a.Key == key {
			return a.Value.StringValue
		}
	}
	return ""
}

func getIntAttr(attrs []otelAttribute, key string) int64 {
	for _, a := range attrs {
		if a.Key != key {
			continue
		}
		if len(a.Value.IntValue) > 0 {
			s := string(a.Value.IntValue)
			if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
				s = s[1 : len(s)-1]
			}
			if v, err := strconv.ParseInt(s, 10, 64); err == nil {
				return v
			}
		}
		if a.Value.StringValue != "" {
			if v, err := strconv.ParseInt(a.Value.StringValue, 10, 64); err == nil {
				return v
			}
		}
	}
	return 0
}

func getFloatAttr(attrs []otelAttribute, key string) float64 {
	for _, a := range attrs {
		if a.Key != key {
			continue
		}
		if a.Value.StringValue != "" {
			if v, err := strconv.ParseFloat(a.Value.StringValue, 64); err == nil {
				return v
			}
		}
	}
	return 0
}
