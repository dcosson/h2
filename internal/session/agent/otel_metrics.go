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
	}
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
	return OtelMetricsSnapshot{
		InputTokens:     m.InputTokens,
		OutputTokens:    m.OutputTokens,
		TotalTokens:     m.TotalTokens,
		TotalCostUSD:    m.TotalCostUSD,
		APIRequestCount: m.APIRequestCount,
		ToolResultCount: m.ToolResultCount,
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
	if usd < 0.01 {
		return fmt.Sprintf("$%.3f", usd)
	}
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
}

// OtelParser extracts metrics from OTEL log records.
// Different agents (Claude Code, etc.) implement this interface.
type OtelParser interface {
	// ParseLogRecord extracts a metrics delta from a log record.
	// Returns nil if the record doesn't contain relevant metrics.
	ParseLogRecord(record OtelLogRecord) *OtelMetricsDelta
}
