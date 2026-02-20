package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"h2/internal/session/agent/monitor"
	"h2/internal/session/agent/shared/eventstore"
)

func TestFormatAgentEvent_Message(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2025-01-15T12:00:30Z")
	ev := monitor.AgentEvent{
		Type:      monitor.EventAgentMessage,
		Timestamp: now.Add(-30 * time.Second),
		Data:      monitor.AgentMessageData{Content: "I'll help you with that task."},
	}
	got := formatAgentEvent(ev, now, 500)
	want := "[30s ago] I'll help you with that task."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatAgentEvent_MessageTruncation(t *testing.T) {
	now := time.Now()
	long := "This is a very long message that exceeds the character limit and should be truncated to avoid cluttering the output"
	ev := monitor.AgentEvent{
		Type:      monitor.EventAgentMessage,
		Timestamp: now.Add(-5 * time.Second),
		Data:      monitor.AgentMessageData{Content: long},
	}
	got := formatAgentEvent(ev, now, 40)
	// Should be truncated to 40 chars: 37 + "..."
	if len(got) == 0 {
		t.Fatal("expected non-empty output")
	}
	// Extract the text part after "[5s ago] "
	prefix := "[5s ago] "
	if got[:len(prefix)] != prefix {
		t.Errorf("expected prefix %q, got %q", prefix, got[:len(prefix)])
	}
	text := got[len(prefix):]
	if len(text) != 40 {
		t.Errorf("expected text length 40, got %d: %q", len(text), text)
	}
	if text[len(text)-3:] != "..." {
		t.Errorf("expected text to end with ..., got %q", text)
	}
}

func TestFormatAgentEvent_MessageNoLimit(t *testing.T) {
	now := time.Now()
	long := "This message should not be truncated when message-chars is zero"
	ev := monitor.AgentEvent{
		Type:      monitor.EventAgentMessage,
		Timestamp: now.Add(-1 * time.Second),
		Data:      monitor.AgentMessageData{Content: long},
	}
	got := formatAgentEvent(ev, now, 0)
	want := "[1s ago] " + long
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatAgentEvent_ToolCompleted(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2025-01-15T12:00:30Z")
	ev := monitor.AgentEvent{
		Type:      monitor.EventToolCompleted,
		Timestamp: now.Add(-10 * time.Second),
		Data: monitor.ToolCompletedData{
			ToolName:   "Read",
			DurationMs: 150,
			Success:    true,
		},
	}
	got := formatAgentEvent(ev, now, 500)
	want := "[10s ago] Read (150ms)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatAgentEvent_ToolCompletedLongDuration(t *testing.T) {
	now := time.Now()
	ev := monitor.AgentEvent{
		Type:      monitor.EventToolCompleted,
		Timestamp: now.Add(-5 * time.Second),
		Data: monitor.ToolCompletedData{
			ToolName:   "Bash",
			DurationMs: 2500,
			Success:    true,
		},
	}
	got := formatAgentEvent(ev, now, 500)
	want := "[5s ago] Bash (2.5s)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatAgentEvent_ToolCompletedFail(t *testing.T) {
	now := time.Now()
	ev := monitor.AgentEvent{
		Type:      monitor.EventToolCompleted,
		Timestamp: now.Add(-3 * time.Second),
		Data: monitor.ToolCompletedData{
			ToolName:   "Bash",
			DurationMs: 500,
			Success:    false,
		},
	}
	got := formatAgentEvent(ev, now, 500)
	want := "[3s ago] Bash (500ms, FAIL)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatAgentEvent_TurnCompleted(t *testing.T) {
	now := time.Now()
	ev := monitor.AgentEvent{
		Type:      monitor.EventTurnCompleted,
		Timestamp: now.Add(-20 * time.Second),
		Data: monitor.TurnCompletedData{
			InputTokens:  1500,
			OutputTokens: 800,
			CostUSD:      0.02,
		},
	}
	got := formatAgentEvent(ev, now, 500)
	want := "[20s ago] turn: 1500in 800out ($0.02)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatAgentEvent_TurnCompletedNoCost(t *testing.T) {
	now := time.Now()
	ev := monitor.AgentEvent{
		Type:      monitor.EventTurnCompleted,
		Timestamp: now.Add(-10 * time.Second),
		Data:      monitor.TurnCompletedData{InputTokens: 100, OutputTokens: 200},
	}
	got := formatAgentEvent(ev, now, 500)
	want := "[10s ago] turn: 100in 200out"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatAgentEvent_ApprovalRequested(t *testing.T) {
	now := time.Now()
	ev := monitor.AgentEvent{
		Type:      monitor.EventApprovalRequested,
		Timestamp: now.Add(-15 * time.Second),
		Data:      monitor.ApprovalRequestedData{ToolName: "Bash"},
	}
	got := formatAgentEvent(ev, now, 500)
	want := "[15s ago] approval: Bash"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatAgentEvent_SessionStarted(t *testing.T) {
	now := time.Now()
	ev := monitor.AgentEvent{
		Type:      monitor.EventSessionStarted,
		Timestamp: now.Add(-5 * time.Minute),
		Data:      monitor.SessionStartedData{Model: "claude-4"},
	}
	got := formatAgentEvent(ev, now, 500)
	want := "[5m ago] session started (claude-4)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatAgentEvent_SessionEnded(t *testing.T) {
	now := time.Now()
	ev := monitor.AgentEvent{
		Type:      monitor.EventSessionEnded,
		Timestamp: now.Add(-1 * time.Second),
	}
	got := formatAgentEvent(ev, now, 500)
	want := "[1s ago] session ended"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatAgentEvent_SkipsNoisyEvents(t *testing.T) {
	now := time.Now()
	noisy := []monitor.AgentEvent{
		{Type: monitor.EventTurnStarted, Timestamp: now},
		{Type: monitor.EventToolStarted, Timestamp: now, Data: monitor.ToolStartedData{ToolName: "Read"}},
		{Type: monitor.EventStateChange, Timestamp: now, Data: monitor.StateChangeData{State: monitor.StateActive}},
	}
	for _, ev := range noisy {
		got := formatAgentEvent(ev, now, 500)
		if got != "" {
			t.Errorf("expected empty for %v, got %q", ev.Type, got)
		}
	}
}

func TestFormatEventLog(t *testing.T) {
	dir := t.TempDir()
	es, err := eventstore.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer es.Close()

	now := time.Now()
	events := []monitor.AgentEvent{
		{Type: monitor.EventSessionStarted, Timestamp: now.Add(-5 * time.Minute), Data: monitor.SessionStartedData{Model: "claude-4"}},
		{Type: monitor.EventAgentMessage, Timestamp: now.Add(-4 * time.Minute), Data: monitor.AgentMessageData{Content: "Starting work"}},
		{Type: monitor.EventToolCompleted, Timestamp: now.Add(-3 * time.Minute), Data: monitor.ToolCompletedData{ToolName: "Read", DurationMs: 100, Success: true}},
		{Type: monitor.EventStateChange, Timestamp: now.Add(-2 * time.Minute), Data: monitor.StateChangeData{State: monitor.StateActive}},
		{Type: monitor.EventTurnCompleted, Timestamp: now.Add(-1 * time.Minute), Data: monitor.TurnCompletedData{InputTokens: 500, OutputTokens: 200, CostUSD: 0.01}},
	}
	for _, ev := range events {
		if err := es.Append(ev); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	lines, err := formatEventLog(dir, 150, 500)
	if err != nil {
		t.Fatalf("formatEventLog: %v", err)
	}

	// Should have 4 lines (state_change is skipped).
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d: %v", len(lines), lines)
	}
}

func TestFormatEventLog_LastN(t *testing.T) {
	dir := t.TempDir()
	es, err := eventstore.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer es.Close()

	now := time.Now()
	for i := 0; i < 10; i++ {
		ev := monitor.AgentEvent{
			Type:      monitor.EventAgentMessage,
			Timestamp: now.Add(-time.Duration(10-i) * time.Minute),
			Data:      monitor.AgentMessageData{Content: "message"},
		}
		if err := es.Append(ev); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	lines, err := formatEventLog(dir, 3, 500)
	if err != nil {
		t.Fatalf("formatEventLog: %v", err)
	}

	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
}

func TestFormatEventLog_NoFile(t *testing.T) {
	dir := t.TempDir()
	_, err := formatEventLog(dir, 150, 500)
	if err == nil {
		t.Fatal("expected error for missing events.jsonl")
	}
}

func TestFormatEventLog_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	// Create empty events.jsonl.
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), nil, 0o644); err != nil {
		t.Fatalf("create empty file: %v", err)
	}

	lines, err := formatEventLog(dir, 150, 500)
	if err != nil {
		t.Fatalf("formatEventLog: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("expected 0 lines, got %d", len(lines))
	}
}

func TestFormatToolDuration(t *testing.T) {
	tests := []struct {
		ms   int64
		want string
	}{
		{0, "0ms"},
		{50, "50ms"},
		{999, "999ms"},
		{1000, "1.0s"},
		{2500, "2.5s"},
		{10000, "10.0s"},
	}
	for _, tt := range tests {
		got := formatToolDuration(tt.ms)
		if got != tt.want {
			t.Errorf("formatToolDuration(%d) = %q, want %q", tt.ms, got, tt.want)
		}
	}
}

func TestFormatRelativeDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "now"},
		{500 * time.Millisecond, "now"},
		{30 * time.Second, "30s ago"},
		{5 * time.Minute, "5m ago"},
		{2 * time.Hour, "2h ago"},
		{48 * time.Hour, "2d ago"},
		{-1 * time.Second, "now"},
	}
	for _, tt := range tests {
		got := formatRelativeDuration(tt.d)
		if got != tt.want {
			t.Errorf("formatRelativeDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}
