package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"h2/internal/activitylog"
	"h2/internal/session/agent/monitor"
	"h2/internal/session/agent/shared/debugenv"
)

// EventHandler coalesces Claude telemetry sources (OTEL logs, hooks,
// and session JSONL lines) into normalized AgentEvents.
type EventHandler struct {
	events            chan<- monitor.AgentEvent
	activityLog       *activitylog.Logger
	expectedSessionID string
	debugPath         string
	debugMu           sync.Mutex
	debugFile         *os.File
}

// NewEventHandler creates an EventHandler that emits events on the given channel.
func NewEventHandler(events chan<- monitor.AgentEvent, log *activitylog.Logger) *EventHandler {
	if log == nil {
		log = activitylog.Nop()
	}
	return &EventHandler{events: events, activityLog: log}
}

// SetExpectedSessionID sets the parent session ID for hook event filtering.
// Hook events with a different non-empty session_id are ignored for state/event
// emission, but still written to activity logs.
func (h *EventHandler) SetExpectedSessionID(sessionID string) {
	h.expectedSessionID = sessionID
}

// ConfigureDebug sets the OTEL debug log path and eagerly initializes the file.
func (h *EventHandler) ConfigureDebug(path string) {
	h.debugMu.Lock()
	defer h.debugMu.Unlock()
	if !debugenv.OtelDebugLoggingEnabled() {
		h.debugPath = ""
		return
	}
	h.debugPath = path
	h.ensureDebugFile()
	if h.debugFile != nil {
		_, _ = h.debugFile.WriteString(time.Now().Format(time.RFC3339Nano) + " " + fmt.Sprintf("startup parser=claude_otel path=%s pid=%d", path, os.Getpid()) + "\n")
	}
}

// OnLogs is the callback for /v1/logs payloads from the OTEL server.
func (h *EventHandler) OnLogs(body []byte) {
	h.debugf("received /v1/logs payload bytes=%d", len(body))
	var payload otelLogsPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		h.debugf("invalid json logs: %v body=%q", err, truncate(body, 600))
		return
	}
	h.processLogs(payload)
}

// OnMetrics is the callback for /v1/metrics payloads from the OTEL server.
// Cumulative metrics are handled by monitor metrics aggregation.
func (h *EventHandler) OnMetrics(body []byte) {
	h.debugf("received /v1/metrics payload bytes=%d", len(body))
}

func (h *EventHandler) processLogs(payload otelLogsPayload) {
	now := time.Now()
	recordCount := 0
	emittedCount := 0
	for _, rl := range payload.ResourceLogs {
		for _, sl := range rl.ScopeLogs {
			for _, lr := range sl.LogRecords {
				recordCount++
				eventName := getAttr(lr.Attributes, "event.name")
				if eventName == "" {
					h.debugf("log_record missing event.name")
					continue
				}
				if h.processLogRecord(eventName, lr, now) {
					emittedCount++
				}
			}
		}
	}
	h.debugf("processed log_records=%d emitted=%d", recordCount, emittedCount)
}

func (h *EventHandler) processLogRecord(eventName string, lr otelLogRecord, ts time.Time) bool {
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
			h.debugf("event=api_request input=%d output=%d cost=%f", input, output, cost)
			return true
		}
	case "tool_result":
		toolName := getAttr(lr.Attributes, "tool_name")
		if toolName != "" {
			h.emit(monitor.AgentEvent{
				Type:      monitor.EventToolCompleted,
				Timestamp: ts,
				Data:      monitor.ToolCompletedData{ToolName: toolName, Success: true},
			})
			h.debugf("event=tool_result tool=%q", toolName)
			return true
		}
	}
	h.debugf("event=%q ignored", eventName)
	return false
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
	if h.shouldIgnoreHookSession(sessionID) {
		return isKnownHookEvent(eventName)
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

	case "PostToolUseFailure":
		h.emit(monitor.AgentEvent{
			Type:      monitor.EventToolCompleted,
			Timestamp: now,
			Data:      monitor.ToolCompletedData{ToolName: toolName, Success: false},
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
		switch decision {
		case "ask_user":
			h.emitStateChange(now, monitor.StateActive, monitor.SubStateWaitingForPermission)
		case "allow":
			h.emitStateChange(now, monitor.StateActive, monitor.SubStateToolUse)
		default:
			// deny (and any unknown value) means we are no longer executing the tool.
			h.emitStateChange(now, monitor.StateActive, monitor.SubStateThinking)
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

func (h *EventHandler) shouldIgnoreHookSession(sessionID string) bool {
	if h.expectedSessionID == "" || sessionID == "" {
		return false
	}
	return sessionID != h.expectedSessionID
}

func isKnownHookEvent(eventName string) bool {
	switch eventName {
	case "UserPromptSubmit",
		"PreToolUse",
		"PostToolUse",
		"PostToolUseFailure",
		"PermissionRequest",
		"permission_decision",
		"PreCompact",
		"SessionStart",
		"Stop",
		"Interrupt",
		"SessionEnd":
		return true
	default:
		return false
	}
}

// HandleInterrupt emits the normalized local interrupt transition.
func (h *EventHandler) HandleInterrupt() bool {
	h.emitStateChange(time.Now(), monitor.StateIdle, monitor.SubStateNone)
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

func (h *EventHandler) debugf(format string, args ...any) {
	if h.debugPath == "" {
		return
	}
	if !debugenv.OtelDebugLoggingEnabled() {
		return
	}

	h.debugMu.Lock()
	defer h.debugMu.Unlock()

	h.ensureDebugFile()
	if h.debugFile == nil {
		return
	}

	msg := fmt.Sprintf(format, args...)
	_, _ = h.debugFile.WriteString(time.Now().Format(time.RFC3339Nano) + " " + msg + "\n")
}

func (h *EventHandler) ensureDebugFile() {
	if h.debugFile != nil || h.debugPath == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(h.debugPath), 0o755)
	f, err := os.OpenFile(h.debugPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		h.debugFile = f
	}
}

func truncate(body []byte, n int) string {
	s := string(body)
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
