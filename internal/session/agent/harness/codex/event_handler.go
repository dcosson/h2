package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"h2/internal/session/agent/monitor"
)

// EventHandler parses Codex OTEL payloads and emits AgentEvents.
// Codex emits high-level codex.* events as OTEL logs via /v1/logs.
// We also keep /v1/traces parsing for backwards compatibility/debugging.
type EventHandler struct {
	events    chan<- monitor.AgentEvent
	debugPath string
	debugMu   sync.Mutex
	debugFile *os.File

	tokenMu          sync.Mutex
	hasTokenBase     bool
	lastInputTokens  int64
	lastCachedTokens int64

	stateMu         sync.Mutex
	currentState    monitor.State
	currentSubState monitor.SubState

	idleMu    sync.Mutex
	idleTimer *time.Timer
	idleSeq   uint64
}

var codexIdleDebounceDelay = 200 * time.Millisecond

// NewEventHandler creates a parser that emits events on the given channel.
func NewEventHandler(events chan<- monitor.AgentEvent) *EventHandler {
	return &EventHandler{
		events:          events,
		currentState:    monitor.StateInitialized,
		currentSubState: monitor.SubStateNone,
	}
}

// ConfigureDebug sets the debug log path and eagerly initializes the file.
func (p *EventHandler) ConfigureDebug(path string) {
	p.debugMu.Lock()
	defer p.debugMu.Unlock()
	if !otelDebugLoggingEnabled() {
		p.debugPath = ""
		return
	}
	p.debugPath = path
	p.ensureDebugFile()
	if p.debugFile != nil {
		_, _ = p.debugFile.WriteString(time.Now().Format(time.RFC3339Nano) + " " + fmt.Sprintf("startup parser=codex_otel path=%s pid=%d", path, os.Getpid()) + "\n")
	}
}

// OnTraces is the callback for /v1/traces payloads from the OTEL server.
func (p *EventHandler) OnTraces(body []byte) {
	p.debugf("received /v1/traces payload bytes=%d", len(body))

	var payload otelTracesPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		p.debugf("invalid json: %v body=%q", err, truncate(body, 600))
		return
	}

	spanCount, emittedCount, unknown := p.processTraces(payload)
	if spanCount == 0 {
		p.debugf("parsed payload but found zero spans")
		return
	}
	if len(unknown) > 0 {
		p.debugf("processed spans=%d emitted=%d unknown_spans=%s", spanCount, emittedCount, strings.Join(unknown, ","))
		return
	}
	p.debugf("processed spans=%d emitted=%d", spanCount, emittedCount)
}

// OnLogs is the callback for /v1/logs payloads from the OTEL server.
func (p *EventHandler) OnLogs(body []byte) {
	p.debugf("received /v1/logs payload bytes=%d", len(body))

	var payload otelLogsPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		p.debugf("invalid json logs: %v body=%q", err, truncate(body, 600))
		return
	}

	recordCount, emittedCount, unknown := p.processLogs(payload)
	if recordCount == 0 {
		p.debugf("parsed logs payload but found zero log records")
		return
	}
	if len(unknown) > 0 {
		p.debugf("processed log_records=%d emitted=%d unknown_events=%s", recordCount, emittedCount, strings.Join(unknown, ","))
		return
	}
	p.debugf("processed log_records=%d emitted=%d", recordCount, emittedCount)
}

// OnMetricsRaw records that /v1/metrics was hit for Codex OTEL debugging.
func (p *EventHandler) OnMetricsRaw(body []byte) {
	p.debugf("received /v1/metrics payload bytes=%d", len(body))
}

func (p *EventHandler) processTraces(payload otelTracesPayload) (spanCount int, emittedCount int, unknown []string) {
	now := time.Now()
	unknownSet := make(map[string]struct{})
	for _, rs := range payload.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			for _, span := range ss.Spans {
				spanCount++
				res := p.processEvent(span.Name, span.Attributes, now)
				emittedCount += res.emitted
				if !res.recognized {
					unknownSet[span.Name] = struct{}{}
				}
			}
		}
	}
	for name := range unknownSet {
		unknown = append(unknown, name)
	}
	return spanCount, emittedCount, unknown
}

func (p *EventHandler) processLogs(payload otelLogsPayload) (recordCount int, emittedCount int, unknown []string) {
	now := time.Now()
	unknownSet := make(map[string]struct{})
	for ri, rl := range payload.ResourceLogs {
		for si, sl := range rl.ScopeLogs {
			for li, rec := range sl.LogRecords {
				recordCount++
				eventName := getAttr(rec.Attributes, "event.name")
				p.debugf("log_record resource=%d scope=%d index=%d event.name=%q attrs={%s}", ri, si, li, eventName, formatAttrs(rec.Attributes))
				res := p.processEvent(eventName, rec.Attributes, now)
				emittedCount += res.emitted
				if !res.recognized {
					if eventName == "" {
						eventName = "(missing event.name)"
					}
					unknownSet[eventName] = struct{}{}
				}
			}
		}
	}
	for name := range unknownSet {
		unknown = append(unknown, name)
	}
	return recordCount, emittedCount, unknown
}

type spanProcessResult struct {
	recognized bool
	emitted    int
}

func (p *EventHandler) processEvent(name string, attrs []otelAttribute, ts time.Time) spanProcessResult {
	switch name {
	case "codex.conversation_starts":
		p.cancelPendingIdle()
		p.resetTokenBaselines()
		convID := getAttr(attrs, "conversation.id")
		model := getAttr(attrs, "model")
		p.emit(monitor.AgentEvent{
			Type:      monitor.EventSessionStarted,
			Timestamp: ts,
			Data: monitor.SessionStartedData{
				ThreadID: convID,
				Model:    model,
			},
		})
		p.emitStateChange(ts, monitor.StateIdle, monitor.SubStateNone)
		p.debugf("span=codex.conversation_starts conversation.id=%q model=%q", convID, model)
		return spanProcessResult{recognized: true, emitted: 2}

	case "codex.user_prompt":
		p.cancelPendingIdle()
		p.emit(monitor.AgentEvent{
			Type:      monitor.EventUserPrompt,
			Timestamp: ts,
		})
		p.emitStateChange(ts, monitor.StateActive, monitor.SubStateThinking)
		p.debugf("span=codex.user_prompt")
		return spanProcessResult{recognized: true, emitted: 2}

	case "codex.sse_event":
		eventKind := getAttr(attrs, "event.kind")
		if eventKind == "response.created" {
			p.cancelPendingIdle()
			p.emitStateChange(ts, monitor.StateActive, monitor.SubStateThinking)
			p.debugf("span=codex.sse_event event.kind=%q", eventKind)
			return spanProcessResult{recognized: true, emitted: 1}
		}
		if eventKind != "response.completed" {
			p.debugf("span=codex.sse_event event.kind=%q (ignored)", eventKind)
			return spanProcessResult{recognized: true}
		}
		input := getIntAttr(attrs, "input_token_count")
		output := getIntAttr(attrs, "output_token_count")
		cached := getIntAttr(attrs, "cached_token_count")
		if input > 0 || output > 0 {
			deltaInput, deltaCached := p.deltaInputAndCached(input, cached)
			p.emit(monitor.AgentEvent{
				Type:      monitor.EventTurnCompleted,
				Timestamp: ts,
				Data: monitor.TurnCompletedData{
					InputTokens:  deltaInput,
					OutputTokens: output,
					CachedTokens: deltaCached,
				},
			})
			if p.shouldScheduleIdleAfterCompletion() {
				p.schedulePendingIdle()
			}
			p.debugf("span=codex.sse_event completed input=%d output=%d cached=%d delta_input=%d delta_cached=%d", input, output, cached, deltaInput, deltaCached)
			return spanProcessResult{recognized: true, emitted: 1}
		}
		p.debugf("span=codex.sse_event completed but zero tokens (ignored)")
		return spanProcessResult{recognized: true}

	case "codex.tool_result":
		toolName := getAttr(attrs, "tool_name")
		callID := getAttr(attrs, "call_id")
		durationMs := getIntAttr(attrs, "duration_ms")
		success := getAttr(attrs, "success") != "false"
		if toolName != "" {
			p.emit(monitor.AgentEvent{
				Type:      monitor.EventToolCompleted,
				Timestamp: ts,
				Data: monitor.ToolCompletedData{
					ToolName:   toolName,
					CallID:     callID,
					DurationMs: durationMs,
					Success:    success,
				},
			})
			p.debugf("span=codex.tool_result tool=%q call_id=%q duration_ms=%d success=%t", toolName, callID, durationMs, success)
			return spanProcessResult{recognized: true, emitted: 1}
		}
		p.debugf("span=codex.tool_result missing tool_name")
		return spanProcessResult{recognized: true}

	case "codex.tool_decision":
		decision := getAttr(attrs, "decision")
		if decision == "approved" {
			p.cancelPendingIdle()
			toolName := getAttr(attrs, "tool_name")
			callID := getAttr(attrs, "call_id")
			p.emit(monitor.AgentEvent{
				Type:      monitor.EventToolStarted,
				Timestamp: ts,
				Data: monitor.ToolStartedData{
					ToolName: toolName,
					CallID:   callID,
				},
			})
			p.emitStateChange(ts, monitor.StateActive, monitor.SubStateToolUse)
			p.debugf("span=codex.tool_decision decision=approved tool=%q call_id=%q", toolName, callID)
			return spanProcessResult{recognized: true, emitted: 2}
		}
		if decision == "ask_user" {
			p.cancelPendingIdle()
			toolName := getAttr(attrs, "tool_name")
			callID := getAttr(attrs, "call_id")
			p.emit(monitor.AgentEvent{
				Type:      monitor.EventApprovalRequested,
				Timestamp: ts,
				Data: monitor.ApprovalRequestedData{
					ToolName: toolName,
					CallID:   callID,
				},
			})
			p.emitStateChange(ts, monitor.StateActive, monitor.SubStateWaitingForPermission)
			p.debugf("span=codex.tool_decision decision=ask_user tool=%q call_id=%q", toolName, callID)
			return spanProcessResult{recognized: true, emitted: 2}
		}
		p.debugf("span=codex.tool_decision decision=%q (ignored)", decision)
		return spanProcessResult{recognized: true}
	}
	p.debugf("event=%q (unknown)", name)
	return spanProcessResult{}
}

func formatAttrs(attrs []otelAttribute) string {
	if len(attrs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(attrs))
	for _, a := range attrs {
		parts = append(parts, fmt.Sprintf("%s=%q", a.Key, attrValueString(a.Value)))
	}
	sort.Strings(parts)
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

func (p *EventHandler) resetTokenBaselines() {
	p.tokenMu.Lock()
	defer p.tokenMu.Unlock()
	p.hasTokenBase = false
	p.lastInputTokens = 0
	p.lastCachedTokens = 0
}

func (p *EventHandler) emitStateChange(ts time.Time, state monitor.State, subState monitor.SubState) {
	p.setState(state, subState)
	p.emit(monitor.AgentEvent{
		Type:      monitor.EventStateChange,
		Timestamp: ts,
		Data: monitor.StateChangeData{
			State:    state,
			SubState: subState,
		},
	})
}

func (p *EventHandler) setState(state monitor.State, subState monitor.SubState) {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	p.currentState = state
	p.currentSubState = subState
}

func (p *EventHandler) shouldScheduleIdleAfterCompletion() bool {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	return p.currentState == monitor.StateActive && p.currentSubState != monitor.SubStateToolUse
}

func (p *EventHandler) schedulePendingIdle() {
	p.idleMu.Lock()
	defer p.idleMu.Unlock()

	p.idleSeq++
	seq := p.idleSeq
	if p.idleTimer != nil {
		p.idleTimer.Stop()
	}
	p.idleTimer = time.AfterFunc(codexIdleDebounceDelay, func() {
		p.idleMu.Lock()
		if seq != p.idleSeq {
			p.idleMu.Unlock()
			return
		}
		p.idleMu.Unlock()

		p.stateMu.Lock()
		state := p.currentState
		sub := p.currentSubState
		p.stateMu.Unlock()
		if state == monitor.StateActive && sub != monitor.SubStateToolUse {
			p.emitStateChange(time.Now(), monitor.StateIdle, monitor.SubStateNone)
		}
	})
}

func (p *EventHandler) cancelPendingIdle() {
	p.idleMu.Lock()
	defer p.idleMu.Unlock()
	p.idleSeq++
	if p.idleTimer != nil {
		p.idleTimer.Stop()
		p.idleTimer = nil
	}
}

func (p *EventHandler) deltaInputAndCached(input, cached int64) (int64, int64) {
	p.tokenMu.Lock()
	defer p.tokenMu.Unlock()

	if !p.hasTokenBase {
		p.hasTokenBase = true
		p.lastInputTokens = input
		p.lastCachedTokens = cached
		return input, cached
	}

	deltaInput := input - p.lastInputTokens
	deltaCached := cached - p.lastCachedTokens
	if deltaInput < 0 {
		deltaInput = input
	}
	if deltaCached < 0 {
		deltaCached = cached
	}

	p.lastInputTokens = input
	p.lastCachedTokens = cached
	return deltaInput, deltaCached
}

func (p *EventHandler) emit(ev monitor.AgentEvent) {
	p.debugf("emit type=%s", ev.Type.String())
	select {
	case p.events <- ev:
	default:
		// Drop event if channel is full.
		p.debugf("drop type=%s reason=events_channel_full", ev.Type.String())
	}
}

// --- OTEL trace JSON types (local to avoid circular imports) ---

type otelTracesPayload struct {
	ResourceSpans []otelResourceSpans `json:"resourceSpans"`
}

type otelResourceSpans struct {
	ScopeSpans []otelScopeSpans `json:"scopeSpans"`
}

type otelScopeSpans struct {
	Spans []otelSpan `json:"spans"`
}

type otelSpan struct {
	Name       string          `json:"name"`
	Attributes []otelAttribute `json:"attributes"`
}

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

// --- Attribute extraction helpers ---

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
		if a.Key == key {
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
	}
	return 0
}

func (p *EventHandler) debugf(format string, args ...any) {
	if p.debugPath == "" {
		return
	}
	if !otelDebugLoggingEnabled() {
		return
	}

	p.debugMu.Lock()
	defer p.debugMu.Unlock()

	p.ensureDebugFile()
	if p.debugFile == nil {
		return
	}

	msg := fmt.Sprintf(format, args...)
	_, _ = p.debugFile.WriteString(time.Now().Format(time.RFC3339Nano) + " " + msg + "\n")
}

func otelDebugLoggingEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("OTEL_DEBUG_LOGGING_ENABLED")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func (p *EventHandler) ensureDebugFile() {
	if p.debugFile != nil || p.debugPath == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p.debugPath), 0o755)
	f, err := os.OpenFile(p.debugPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		p.debugFile = f
	}
}

func truncate(body []byte, n int) string {
	s := string(body)
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
