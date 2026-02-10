package agent

import (
	"fmt"
	"sync"
)

// OtelMetrics holds aggregated metrics from OTEL events.
// This is agent-agnostic — different parsers populate these fields.
type OtelMetrics struct {
	mu sync.RWMutex

	// Token usage
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64

	// Cost in USD
	TotalCostUSD float64

	// Event counts (for debugging/verification)
	APIRequestCount int64
	ToolResultCount int64

	// Per-tool counts from OTEL logs (tool_result events)
	ToolCounts map[string]int64

	// From OTEL /v1/metrics (cumulative, overwritten on each update)
	LinesAdded    int64
	LinesRemoved  int64
	ActiveTimeHrs float64
	ModelCosts    map[string]float64          // model -> cost USD
	ModelTokens   map[string]map[string]int64 // model -> type -> count

	// Connection status
	EventsReceived bool // true after first OTEL event
}

// Update adds values to the aggregated metrics.
func (m *OtelMetrics) Update(delta OtelMetricsDelta) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.EventsReceived = true
	m.InputTokens += delta.InputTokens
	m.OutputTokens += delta.OutputTokens
	m.TotalTokens += delta.TotalTokens
	m.TotalCostUSD += delta.CostUSD
	if delta.IsAPIRequest {
		m.APIRequestCount++
	}
	if delta.IsToolResult {
		m.ToolResultCount++
		if delta.ToolName != "" {
			if m.ToolCounts == nil {
				m.ToolCounts = make(map[string]int64)
			}
			m.ToolCounts[delta.ToolName]++
		}
	}
}

// UpdateFromMetricsEndpoint applies cumulative values from /v1/metrics.
// These are running totals, so they overwrite (not add to) the existing values.
func (m *OtelMetrics) UpdateFromMetricsEndpoint(parsed *ParsedOtelMetrics) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.EventsReceived = true
	m.LinesAdded = parsed.LinesAdded
	m.LinesRemoved = parsed.LinesRemoved
	m.ActiveTimeHrs = parsed.ActiveTimeHrs
	m.ModelCosts = parsed.ModelCosts
	m.ModelTokens = parsed.ModelTokens
}

// NoteEvent marks that an OTEL event was received (even if no metrics extracted).
func (m *OtelMetrics) NoteEvent() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.EventsReceived = true
}

// Snapshot returns a copy of the current metrics.
func (m *OtelMetrics) Snapshot() OtelMetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Copy maps to avoid races.
	var toolCounts map[string]int64
	if len(m.ToolCounts) > 0 {
		toolCounts = make(map[string]int64, len(m.ToolCounts))
		for k, v := range m.ToolCounts {
			toolCounts[k] = v
		}
	}
	var modelCosts map[string]float64
	if len(m.ModelCosts) > 0 {
		modelCosts = make(map[string]float64, len(m.ModelCosts))
		for k, v := range m.ModelCosts {
			modelCosts[k] = v
		}
	}
	var modelTokens map[string]map[string]int64
	if len(m.ModelTokens) > 0 {
		modelTokens = make(map[string]map[string]int64, len(m.ModelTokens))
		for model, types := range m.ModelTokens {
			mt := make(map[string]int64, len(types))
			for k, v := range types {
				mt[k] = v
			}
			modelTokens[model] = mt
		}
	}

	return OtelMetricsSnapshot{
		InputTokens:     m.InputTokens,
		OutputTokens:    m.OutputTokens,
		TotalTokens:     m.TotalTokens,
		TotalCostUSD:    m.TotalCostUSD,
		APIRequestCount: m.APIRequestCount,
		ToolResultCount: m.ToolResultCount,
		ToolCounts:      toolCounts,
		LinesAdded:      m.LinesAdded,
		LinesRemoved:    m.LinesRemoved,
		ActiveTimeHrs:   m.ActiveTimeHrs,
		ModelCosts:      modelCosts,
		ModelTokens:     modelTokens,
		EventsReceived:  m.EventsReceived,
	}
}

// OtelMetricsSnapshot is a point-in-time copy of metrics (no mutex needed).
type OtelMetricsSnapshot struct {
	InputTokens     int64
	OutputTokens    int64
	TotalTokens     int64
	TotalCostUSD    float64
	APIRequestCount int64
	ToolResultCount int64
	ToolCounts      map[string]int64
	LinesAdded      int64
	LinesRemoved    int64
	ActiveTimeHrs   float64
	ModelCosts      map[string]float64
	ModelTokens     map[string]map[string]int64
	EventsReceived  bool
}

// FormatTokens returns a human-readable token count (e.g., "6k", "1.2M").
func FormatTokens(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 10000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	if n < 1000000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	if n < 10000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	return fmt.Sprintf("%dM", n/1000000)
}

// FormatCost returns a human-readable cost (e.g., "$0.12", "$1.23").
func FormatCost(usd float64) string {
	return fmt.Sprintf("$%.2f", usd)
}

// OtelMetricsDelta represents incremental changes to apply to metrics.
type OtelMetricsDelta struct {
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	CostUSD      float64
	IsAPIRequest bool
	IsToolResult bool
	ToolName     string // from tool_result events
}

// OtelParser extracts metrics from OTEL log records.
// Different agents (Claude Code, etc.) implement this interface.
type OtelParser interface {
	// ParseLogRecord extracts a metrics delta from a log record.
	// Returns nil if the record doesn't contain relevant metrics.
	ParseLogRecord(record OtelLogRecord) *OtelMetricsDelta
}
