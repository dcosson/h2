package codex

import (
	"encoding/json"
	"strconv"
	"time"

	"h2/internal/session/agent/monitor"
)

// OtelParser parses Codex OTEL trace payloads and emits AgentEvents.
// Codex emits traces (not logs) via /v1/traces with span names like
// "codex.conversation_starts", "codex.user_prompt", etc.
type OtelParser struct {
	events chan<- monitor.AgentEvent
}

// NewOtelParser creates a parser that emits events on the given channel.
func NewOtelParser(events chan<- monitor.AgentEvent) *OtelParser {
	return &OtelParser{events: events}
}

// OnTraces is the callback for /v1/traces payloads from the OTEL server.
func (p *OtelParser) OnTraces(body []byte) {
	var payload otelTracesPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return
	}
	p.processTraces(payload)
}

func (p *OtelParser) processTraces(payload otelTracesPayload) {
	now := time.Now()
	for _, rs := range payload.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			for _, span := range ss.Spans {
				p.processSpan(span, now)
			}
		}
	}
}

func (p *OtelParser) processSpan(span otelSpan, ts time.Time) {
	switch span.Name {
	case "codex.conversation_starts":
		convID := getAttr(span.Attributes, "conversation.id")
		model := getAttr(span.Attributes, "model")
		p.emit(monitor.AgentEvent{
			Type:      monitor.EventSessionStarted,
			Timestamp: ts,
			Data: monitor.SessionStartedData{
				ThreadID: convID,
				Model:    model,
			},
		})

	case "codex.user_prompt":
		p.emit(monitor.AgentEvent{
			Type:      monitor.EventTurnStarted,
			Timestamp: ts,
			Data:      monitor.TurnStartedData{},
		})

	case "codex.sse_event":
		// Only completed SSE events carry token counts.
		eventKind := getAttr(span.Attributes, "event.kind")
		if eventKind != "response.completed" {
			return
		}
		input := getIntAttr(span.Attributes, "input_token_count")
		output := getIntAttr(span.Attributes, "output_token_count")
		cached := getIntAttr(span.Attributes, "cached_token_count")
		if input > 0 || output > 0 {
			p.emit(monitor.AgentEvent{
				Type:      monitor.EventTurnCompleted,
				Timestamp: ts,
				Data: monitor.TurnCompletedData{
					InputTokens:  input,
					OutputTokens: output,
					CachedTokens: cached,
				},
			})
		}

	case "codex.tool_result":
		toolName := getAttr(span.Attributes, "tool_name")
		callID := getAttr(span.Attributes, "call_id")
		durationMs := getIntAttr(span.Attributes, "duration_ms")
		success := getAttr(span.Attributes, "success") != "false"
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
		}

	case "codex.tool_decision":
		decision := getAttr(span.Attributes, "decision")
		if decision == "ask_user" {
			toolName := getAttr(span.Attributes, "tool_name")
			callID := getAttr(span.Attributes, "call_id")
			p.emit(monitor.AgentEvent{
				Type:      monitor.EventApprovalRequested,
				Timestamp: ts,
				Data: monitor.ApprovalRequestedData{
					ToolName: toolName,
					CallID:   callID,
				},
			})
		}
	}
}

func (p *OtelParser) emit(ev monitor.AgentEvent) {
	select {
	case p.events <- ev:
	default:
		// Drop event if channel is full.
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
