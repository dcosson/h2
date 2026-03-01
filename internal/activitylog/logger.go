package activitylog

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Logger writes structured JSONL entries to an activity log file.
// All methods are safe for concurrent use. When disabled (w is nil),
// all methods are no-ops.
type Logger struct {
	mu        sync.Mutex
	w         *os.File
	actor     string
	sessionID string
}

// New creates a Logger that appends to logPath. If enabled is false or the
// file cannot be opened, returns a no-op logger (safe to call methods on).
func New(enabled bool, logPath, actor, sessionID string) *Logger {
	if !enabled {
		return &Logger{}
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return &Logger{}
	}
	return &Logger{w: f, actor: actor, sessionID: sessionID}
}

// Nop returns a disabled logger. All methods are no-ops.
func Nop() *Logger {
	return &Logger{}
}

// entry is the common envelope for all log lines.
type entry struct {
	Timestamp string `json:"ts"`
	Actor     string `json:"actor"`
	SessionID string `json:"session_id"`
	Event     string `json:"event"`
}

// HookEvent logs a Claude Code hook event.
func (l *Logger) HookEvent(sessionID, eventName, toolName string) {
	l.log(struct {
		entry
		HookEvent string `json:"hook_event"`
		ToolName  string `json:"tool_name,omitempty"`
	}{
		entry:     l.entryWithSession("hook", sessionID),
		HookEvent: eventName,
		ToolName:  toolName,
	})
}

// PermissionDecision logs a permission reviewer decision.
func (l *Logger) PermissionDecision(sessionID, toolName, decision, reason string) {
	l.log(struct {
		entry
		ToolName string `json:"tool_name"`
		Decision string `json:"decision"`
		Reason   string `json:"reason,omitempty"`
	}{
		entry:    l.entryWithSession("permission_decision", sessionID),
		ToolName: toolName,
		Decision: decision,
		Reason:   reason,
	})
}

// OtelConnected logs that the OTEL logs endpoint received its first event.
func (l *Logger) OtelConnected(endpoint string) {
	l.log(struct {
		entry
		Endpoint string `json:"endpoint"`
	}{
		entry:    l.entry("otel_connected"),
		Endpoint: endpoint,
	})
}

// StateChange logs an agent state transition.
func (l *Logger) StateChange(from, to string) {
	l.log(struct {
		entry
		From string `json:"from"`
		To   string `json:"to"`
	}{
		entry: l.entry("state_change"),
		From:  from,
		To:    to,
	})
}

// SessionSummaryData contains all metrics for a session_summary log entry.
type SessionSummaryData struct {
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	CostUSD      float64
	APIRequests  int64
	ToolCalls    int64
	LinesAdded   int64
	LinesRemoved int64
	ToolCounts   map[string]int64

	// From OTEL /v1/metrics
	ActiveTimeHrs float64
	ModelCosts    map[string]float64          // model -> cost USD
	ModelTokens   map[string]map[string]int64 // model -> type -> count

	// From hook collector
	ToolUseCount int64 // hook-based count (may differ from OTEL ToolCalls)

	// Session-level
	Uptime string // human-readable uptime

	// Point-in-time git working tree stats
	GitFilesChanged int
	GitLinesAdded   int64
	GitLinesRemoved int64
}

// SessionSummary logs cumulative session metrics at exit.
func (l *Logger) SessionSummary(d SessionSummaryData) {
	l.log(struct {
		entry
		InputTokens     int64                       `json:"input_tokens"`
		OutputTokens    int64                       `json:"output_tokens"`
		TotalTokens     int64                       `json:"total_tokens"`
		CostUSD         float64                     `json:"cost_usd"`
		APIRequests     int64                       `json:"api_requests"`
		ToolCalls       int64                       `json:"tool_calls"`
		LinesAdded      int64                       `json:"lines_added"`
		LinesRemoved    int64                       `json:"lines_removed"`
		ToolCounts      map[string]int64            `json:"tool_counts"`
		ActiveTimeHrs   float64                     `json:"active_time_hrs"`
		ModelCosts      map[string]float64          `json:"model_costs"`
		ModelTokens     map[string]map[string]int64 `json:"model_tokens"`
		ToolUseCount    int64                       `json:"tool_use_count"`
		Uptime          string                      `json:"uptime"`
		GitFilesChanged int                         `json:"git_files_changed"`
		GitLinesAdded   int64                       `json:"git_lines_added"`
		GitLinesRemoved int64                       `json:"git_lines_removed"`
	}{
		entry:           l.entry("session_summary"),
		InputTokens:     d.InputTokens,
		OutputTokens:    d.OutputTokens,
		TotalTokens:     d.TotalTokens,
		CostUSD:         d.CostUSD,
		APIRequests:     d.APIRequests,
		ToolCalls:       d.ToolCalls,
		LinesAdded:      d.LinesAdded,
		LinesRemoved:    d.LinesRemoved,
		ToolCounts:      d.ToolCounts,
		ActiveTimeHrs:   d.ActiveTimeHrs,
		ModelCosts:      d.ModelCosts,
		ModelTokens:     d.ModelTokens,
		ToolUseCount:    d.ToolUseCount,
		Uptime:          d.Uptime,
		GitFilesChanged: d.GitFilesChanged,
		GitLinesAdded:   d.GitLinesAdded,
		GitLinesRemoved: d.GitLinesRemoved,
	})
}

// Close closes the underlying file.
func (l *Logger) Close() error {
	if l.w == nil {
		return nil
	}
	return l.w.Close()
}

func (l *Logger) entry(event string) entry {
	return entry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Actor:     l.actor,
		SessionID: l.sessionID,
		Event:     event,
	}
}

func (l *Logger) entryWithSession(event, sessionID string) entry {
	return entry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Actor:     l.actor,
		SessionID: sessionID,
		Event:     event,
	}
}

func (l *Logger) log(v any) {
	if l.w == nil {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	data = append(data, '\n')
	l.mu.Lock()
	l.w.Write(data)
	l.mu.Unlock()
}
