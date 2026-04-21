package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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
	h.debugf("metrics payload body=%q", truncate(body, 600))
}

func (h *EventHandler) processLogs(payload otelLogsPayload) {
	now := time.Now()
	recordCount := 0
	emittedCount := 0
	for ri, rl := range payload.ResourceLogs {
		for si, sl := range rl.ScopeLogs {
			for li, lr := range sl.LogRecords {
				recordCount++
				eventName := getAttr(lr.Attributes, "event.name")
				h.debugf("log_record resource=%d scope=%d index=%d event.name=%q attrs={%s}", ri, si, li, eventName, formatAttrs(lr.Attributes))
				if eventName == "" {
					h.debugf("log_record action=ignored reason=missing_event_name")
					continue
				}
				processed, reason := h.processLogRecord(eventName, lr, now)
				if processed {
					emittedCount++
					h.debugf("log_record action=processed event.name=%q reason=%s", eventName, reason)
				} else {
					h.debugf("log_record action=ignored event.name=%q reason=%s", eventName, reason)
				}
			}
		}
	}
	h.debugf("processed log_records=%d emitted=%d", recordCount, emittedCount)
}

func (h *EventHandler) processLogRecord(eventName string, lr otelLogRecord, ts time.Time) (bool, string) {
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
			return true, "turn_completed_emitted"
		}
		return false, "no_usage_values"
	case "api_error":
		statusCode := getAttr(lr.Attributes, "status_code")
		errMsg := getAttr(lr.Attributes, "error")
		if statusCode == "429" {
			h.emitStateChange(ts, monitor.StateIdle, monitor.SubStateUsageLimit)
			return true, fmt.Sprintf("usage_limit status=%s error=%q", statusCode, errMsg)
		}
		if statusCode == "401" {
			h.emitStateChange(ts, monitor.StateIdle, monitor.SubStateAuthError)
			return true, fmt.Sprintf("auth_error status=%s error=%q", statusCode, errMsg)
		}
		if len(statusCode) > 0 && statusCode[0] == '5' {
			h.emitStateChange(ts, monitor.StateIdle, monitor.SubStateServerError)
			h.emit(monitor.AgentEvent{
				Type:      monitor.EventServerErrorInfo,
				Timestamp: ts,
				Data:      monitor.ServerErrorData{StatusCode: statusCode, Message: errMsg},
			})
			return true, fmt.Sprintf("server_error status=%s error=%q", statusCode, errMsg)
		}
		return false, fmt.Sprintf("api_error status=%s", statusCode)

	case "tool_result":
		toolName := getAttr(lr.Attributes, "tool_name")
		if toolName != "" {
			h.emit(monitor.AgentEvent{
				Type:      monitor.EventToolCompleted,
				Timestamp: ts,
				Data:      monitor.ToolCompletedData{ToolName: toolName, Success: true},
			})
			return true, "tool_completed_emitted"
		}
		return false, "missing_tool_name"
	}
	return false, "unsupported_event_name"
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
		h.emitStateChange(now, monitor.StateActive, monitor.SubStatePermissionReview)

	case "permission_decision":
		decision := extractDecision(payload)
		reason := extractReason(payload)
		processedBy := extractField(payload, "processed_by")
		role := extractField(payload, "role")
		h.emit(monitor.AgentEvent{
			Type:      monitor.EventPermissionDecision,
			Timestamp: now,
			Data: monitor.PermissionDecisionData{
				ToolName:    toolName,
				Decision:    decision,
				Reason:      reason,
				ProcessedBy: processedBy,
				Role:        role,
			},
		})
		switch decision {
		case "ask_user":
			h.emitStateChange(now, monitor.StateActive, monitor.SubStateBlockedOnPermission)
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
	if events, ok := parseSessionLine(line); ok {
		for _, ev := range events {
			h.emit(ev)
		}
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
	ToolName    string `json:"tool_name"`
	SessionID   string `json:"session_id"`
	Decision    string `json:"decision"`
	Reason      string `json:"reason"`
	ProcessedBy string `json:"processed_by"`
	Role        string `json:"role"`
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

func extractField(payload json.RawMessage, field string) string {
	if len(payload) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(payload, &m); err != nil {
		return ""
	}
	raw, ok := m[field]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// --- session log parsing ---

type sessionLogEntry struct {
	Type              string          `json:"type"`
	Message           json.RawMessage `json:"message,omitempty"`
	Error             string          `json:"error,omitempty"`
	IsApiErrorMessage bool            `json:"isApiErrorMessage,omitempty"`
}

// sessionMessage handles Claude's message format where content can be
// either a plain string or an array of content blocks.
type sessionMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content,omitempty"`
}

// contentBlock represents one element in the content array.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// extractContent returns the text content from a sessionMessage.
// Handles both string content and array-of-blocks content.
func (m *sessionMessage) extractContent() string {
	if len(m.Content) == 0 {
		return ""
	}
	// Try plain string first.
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return s
	}
	// Try array of content blocks.
	var blocks []contentBlock
	if err := json.Unmarshal(m.Content, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// isAuthErrorMessage returns true if the message content indicates an
// authentication error (expired OAuth token, invalid credentials).
func isAuthErrorMessage(content string) bool {
	return strings.Contains(content, "authentication_error") ||
		strings.Contains(content, "OAuth token has expired")
}

// isServerErrorMessage returns true if the message content indicates an
// API server error (5xx). Matches patterns like "Internal server error",
// "API Error: 500", or the Anthropic error type "api_error".
func isServerErrorMessage(content string) bool {
	lower := strings.ToLower(content)
	return strings.Contains(lower, "internal server error") ||
		strings.Contains(lower, "\"type\":\"api_error\"") ||
		strings.Contains(lower, "api error: 5")
}

// resetsPattern matches Claude Code's synthetic rate limit message format:
//
//	"resets 12pm (America/Los_Angeles)"
//	"resets 5:30am (UTC)"
var resetsPattern = regexp.MustCompile(`resets\s+(\d{1,2}(?::\d{2})?\s*(?:am|pm))\s+\(([^)]+)\)`)

// parseSessionLine parses one Claude session JSONL line into zero or more
// AgentEvents. It returns up to two events: an agent message and/or a
// usage limit info event.
func parseSessionLine(line []byte) ([]monitor.AgentEvent, bool) {
	var entry sessionLogEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return nil, false
	}
	if entry.Type != "assistant" {
		return nil, false
	}

	var msg sessionMessage
	if len(entry.Message) > 0 {
		if err := json.Unmarshal(entry.Message, &msg); err != nil {
			return nil, false
		}
	}
	content := msg.extractContent()
	if content == "" {
		return nil, false
	}

	now := time.Now()
	var events []monitor.AgentEvent

	// Check for rate limit synthetic messages.
	if entry.Error == "rate_limit" || entry.IsApiErrorMessage {
		if resetsAt, ok := parseResetsAt(content, now); ok {
			events = append(events, monitor.AgentEvent{
				Type:      monitor.EventUsageLimitInfo,
				Timestamp: now,
				Data: monitor.UsageLimitData{
					ResetsAt: resetsAt,
					Message:  content,
				},
			})
		}
	}

	// Check for auth error messages (expired OAuth token, 401).
	isAuth := entry.IsApiErrorMessage && isAuthErrorMessage(content)
	if isAuth {
		events = append(events, monitor.AgentEvent{
			Type:      monitor.EventAuthErrorInfo,
			Timestamp: now,
			Data:      monitor.AuthErrorData{Message: content},
		})
	}

	// Check for server error messages (5xx). An API error message that isn't
	// a rate limit or auth error is treated as a server error.
	isRateLimit := entry.Error == "rate_limit" || (entry.IsApiErrorMessage && resetsPattern.MatchString(content))
	if entry.IsApiErrorMessage && !isAuth && !isRateLimit && isServerErrorMessage(content) {
		events = append(events, monitor.AgentEvent{
			Type:      monitor.EventServerErrorInfo,
			Timestamp: now,
			Data:      monitor.ServerErrorData{Message: content},
		})
	}

	// Always emit the agent message.
	events = append(events, monitor.AgentEvent{
		Type:      monitor.EventAgentMessage,
		Timestamp: now,
		Data:      monitor.AgentMessageData{Content: content},
	})

	return events, true
}

// parseResetsAt extracts an absolute reset time from a message like
// "You've hit your limit · resets 12pm (America/Los_Angeles)".
// The reference time is used to resolve the date (next occurrence of the
// given hour in the given timezone).
func parseResetsAt(message string, reference time.Time) (time.Time, bool) {
	m := resetsPattern.FindStringSubmatch(message)
	if m == nil {
		return time.Time{}, false
	}
	timeStr := strings.TrimSpace(m[1])
	tzName := m[2]

	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return time.Time{}, false
	}

	// Parse the time-of-day. Accept "12pm", "5am", "5:30pm".
	var hour, min int
	var isPM bool
	if strings.Contains(timeStr, ":") {
		// "5:30pm" format
		parts := strings.SplitN(strings.TrimRight(timeStr, "apmAPM"), ":", 2)
		hour = atoiSafe(parts[0])
		min = atoiSafe(parts[1])
		isPM = strings.HasSuffix(strings.ToLower(timeStr), "pm")
	} else {
		// "12pm" format
		numStr := strings.TrimRight(strings.ToLower(timeStr), "apm")
		hour = atoiSafe(numStr)
		isPM = strings.HasSuffix(strings.ToLower(timeStr), "pm")
	}

	// Convert 12-hour to 24-hour.
	if isPM && hour != 12 {
		hour += 12
	} else if !isPM && hour == 12 {
		hour = 0
	}

	// Build the candidate time in the target timezone.
	refInTZ := reference.In(loc)
	candidate := time.Date(refInTZ.Year(), refInTZ.Month(), refInTZ.Day(), hour, min, 0, 0, loc)

	// If the candidate is in the past, it must be tomorrow.
	if !candidate.After(reference) {
		candidate = candidate.Add(24 * time.Hour)
	}

	return candidate, true
}

func atoiSafe(s string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(s))
	return v
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

func formatAttrs(attrs []otelAttribute) string {
	if len(attrs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(attrs))
	for _, a := range attrs {
		parts = append(parts, fmt.Sprintf("%s=%q", a.Key, attrValueString(a.Value)))
	}
	return strings.Join(parts, ", ")
}

func attrValueString(v otelAttrValue) string {
	if len(v.IntValue) > 0 {
		s := string(v.IntValue)
		if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
			s = s[1 : len(s)-1]
		}
		return s
	}
	return v.StringValue
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
