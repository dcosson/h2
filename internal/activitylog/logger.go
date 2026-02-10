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

// OtelMetrics logs an OTEL metrics update (token/cost delta).
func (l *Logger) OtelMetrics(inputTokens, outputTokens int64, costUSD float64) {
	l.log(struct {
		entry
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		CostUSD      float64 `json:"cost_usd"`
	}{
		entry:        l.entry("otel_metrics"),
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		CostUSD:      costUSD,
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
