package claude

import (
	"encoding/json"
	"strconv"
	"time"

	"h2/internal/session/agent/monitor"
)

// OtelParser parses Claude Code OTEL log and metric payloads and emits
// AgentEvents on the provided channel.
type OtelParser struct {
	events chan<- monitor.AgentEvent
}

// NewOtelParser creates an OtelParser that emits events on the given channel.
func NewOtelParser(events chan<- monitor.AgentEvent) *OtelParser {
	return &OtelParser{events: events}
}

// OnLogs is the callback for /v1/logs payloads from the OTEL server.
func (p *OtelParser) OnLogs(body []byte) {
	var payload otelLogsPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return
	}
	p.processLogs(payload)
}

// OnMetrics is the callback for /v1/metrics payloads from the OTEL server.
// Cumulative metrics don't map cleanly to discrete events, so this is
// currently a no-op. The Agent's OtelMetrics handles these directly.
func (p *OtelParser) OnMetrics(body []byte) {
	// Cumulative metrics (/v1/metrics) are handled by the Agent's
	// OtelMetrics system, not through the event stream.
}

func (p *OtelParser) processLogs(payload otelLogsPayload) {
	now := time.Now()
	for _, rl := range payload.ResourceLogs {
		for _, sl := range rl.ScopeLogs {
			for _, lr := range sl.LogRecords {
				eventName := getAttr(lr.Attributes, "event.name")
				if eventName == "" {
					continue
				}
				p.processLogRecord(eventName, lr, now)
			}
		}
	}
}

func (p *OtelParser) processLogRecord(eventName string, lr otelLogRecord, ts time.Time) {
	switch eventName {
	case "api_request":
		input := getIntAttr(lr.Attributes, "input_tokens")
		output := getIntAttr(lr.Attributes, "output_tokens")
		cost := getFloatAttr(lr.Attributes, "cost_usd")
		if input > 0 || output > 0 || cost > 0 {
			p.emit(monitor.AgentEvent{
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
			p.emit(monitor.AgentEvent{
				Type:      monitor.EventToolCompleted,
				Timestamp: ts,
				Data: monitor.ToolCompletedData{
					ToolName: toolName,
					Success:  true,
				},
			})
		}
	}
}

func (p *OtelParser) emit(ev monitor.AgentEvent) {
	select {
	case p.events <- ev:
	default:
		// Drop event if channel is full — shouldn't happen with buffered channel.
	}
}

// --- OTEL JSON types (local to this package to avoid circular imports) ---

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

func getFloatAttr(attrs []otelAttribute, key string) float64 {
	for _, a := range attrs {
		if a.Key == key {
			if a.Value.StringValue != "" {
				if v, err := strconv.ParseFloat(a.Value.StringValue, 64); err == nil {
					return v
				}
			}
		}
	}
	return 0
}
