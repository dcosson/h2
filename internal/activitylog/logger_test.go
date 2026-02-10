package activitylog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHookEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.log")
	l := New(true, path, "test-agent", "sess-123")
	defer l.Close()

	l.HookEvent("hook-sess-456", "PreToolUse", "Bash")

	lines := readLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	var e struct {
		Actor     string `json:"actor"`
		SessionID string `json:"session_id"`
		Event     string `json:"event"`
		HookEvent string `json:"hook_event"`
		ToolName  string `json:"tool_name"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Actor != "test-agent" {
		t.Errorf("actor = %q, want %q", e.Actor, "test-agent")
	}
	if e.SessionID != "hook-sess-456" {
		t.Errorf("session_id = %q, want %q", e.SessionID, "hook-sess-456")
	}
	if e.Event != "hook" {
		t.Errorf("event = %q, want %q", e.Event, "hook")
	}
	if e.HookEvent != "PreToolUse" {
		t.Errorf("hook_event = %q, want %q", e.HookEvent, "PreToolUse")
	}
	if e.ToolName != "Bash" {
		t.Errorf("tool_name = %q, want %q", e.ToolName, "Bash")
	}
}

func TestHookEventOmitsEmptyToolName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.log")
	l := New(true, path, "agent", "sess")
	defer l.Close()

	l.HookEvent("sess", "SessionStart", "")

	lines := readLines(t, path)
	if strings.Contains(lines[0], "tool_name") {
		t.Error("expected tool_name to be omitted when empty")
	}
}

func TestPermissionDecision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.log")
	l := New(true, path, "agent", "sess")
	defer l.Close()

	l.PermissionDecision("sess", "Bash", "allow", "Safe tool")

	lines := readLines(t, path)
	var e struct {
		Event    string `json:"event"`
		ToolName string `json:"tool_name"`
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Event != "permission_decision" {
		t.Errorf("event = %q, want %q", e.Event, "permission_decision")
	}
	if e.Decision != "allow" {
		t.Errorf("decision = %q, want %q", e.Decision, "allow")
	}
}

func TestOtelMetrics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.log")
	l := New(true, path, "agent", "sess")
	defer l.Close()

	l.OtelMetrics(100, 200, 0.005)

	lines := readLines(t, path)
	var e struct {
		Event        string  `json:"event"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		CostUSD      float64 `json:"cost_usd"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Event != "otel_metrics" {
		t.Errorf("event = %q, want %q", e.Event, "otel_metrics")
	}
	if e.InputTokens != 100 || e.OutputTokens != 200 {
		t.Errorf("tokens = %d/%d, want 100/200", e.InputTokens, e.OutputTokens)
	}
}

func TestOtelConnected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.log")
	l := New(true, path, "agent", "sess")
	defer l.Close()

	l.OtelConnected("/v1/logs")

	lines := readLines(t, path)
	var e struct {
		Event    string `json:"event"`
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Event != "otel_connected" {
		t.Errorf("event = %q, want %q", e.Event, "otel_connected")
	}
	if e.Endpoint != "/v1/logs" {
		t.Errorf("endpoint = %q, want %q", e.Endpoint, "/v1/logs")
	}
}

func TestStateChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.log")
	l := New(true, path, "agent", "sess")
	defer l.Close()

	l.StateChange("active", "idle")

	lines := readLines(t, path)
	var e struct {
		Event string `json:"event"`
		From  string `json:"from"`
		To    string `json:"to"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.From != "active" || e.To != "idle" {
		t.Errorf("from/to = %q/%q, want active/idle", e.From, e.To)
	}
}

func TestDisabledLoggerIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.log")
	l := New(false, path, "agent", "sess")
	defer l.Close()

	l.HookEvent("sess", "PreToolUse", "Bash")
	l.PermissionDecision("sess", "Bash", "allow", "ok")
	l.OtelMetrics(100, 200, 0.01)
	l.OtelConnected("/v1/logs")
	l.StateChange("active", "idle")
	l.SessionSummary(100, 200, 0.01, 1, 2, 0, 0, nil)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected no file to be created when disabled")
	}
}

func TestNopLoggerIsNoop(t *testing.T) {
	l := Nop()
	// Should not panic.
	l.HookEvent("sess", "PreToolUse", "Bash")
	l.PermissionDecision("sess", "Bash", "allow", "ok")
	l.OtelMetrics(100, 200, 0.01)
	l.OtelConnected("/v1/logs")
	l.StateChange("active", "idle")
	l.SessionSummary(100, 200, 0.01, 1, 2, 0, 0, nil)
	l.Close()
}

func TestMultipleEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.log")
	l := New(true, path, "agent", "sess")
	defer l.Close()

	l.HookEvent("sess", "SessionStart", "")
	l.HookEvent("sess", "PreToolUse", "Bash")
	l.StateChange("active", "idle")

	lines := readLines(t, path)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
}

func TestTimestampPresent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.log")
	l := New(true, path, "agent", "sess")
	defer l.Close()

	l.HookEvent("sess", "Stop", "")

	lines := readLines(t, path)
	var e struct {
		Timestamp string `json:"ts"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Timestamp == "" {
		t.Error("expected ts field to be present")
	}
}

func TestSessionSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.log")
	l := New(true, path, "agent", "sess")
	defer l.Close()

	l.SessionSummary(5000, 3000, 0.42, 10, 25, 42, 7, map[string]int64{"Bash": 15, "Read": 8})

	lines := readLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	var e struct {
		Event        string           `json:"event"`
		InputTokens  int64            `json:"input_tokens"`
		OutputTokens int64            `json:"output_tokens"`
		CostUSD      float64          `json:"cost_usd"`
		APIRequests  int64            `json:"api_requests"`
		ToolCalls    int64            `json:"tool_calls"`
		LinesAdded   int64            `json:"lines_added"`
		LinesRemoved int64            `json:"lines_removed"`
		ToolCounts   map[string]int64 `json:"tool_counts"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Event != "session_summary" {
		t.Errorf("event = %q, want %q", e.Event, "session_summary")
	}
	if e.InputTokens != 5000 {
		t.Errorf("input_tokens = %d, want 5000", e.InputTokens)
	}
	if e.OutputTokens != 3000 {
		t.Errorf("output_tokens = %d, want 3000", e.OutputTokens)
	}
	if e.CostUSD != 0.42 {
		t.Errorf("cost_usd = %f, want 0.42", e.CostUSD)
	}
	if e.APIRequests != 10 {
		t.Errorf("api_requests = %d, want 10", e.APIRequests)
	}
	if e.ToolCalls != 25 {
		t.Errorf("tool_calls = %d, want 25", e.ToolCalls)
	}
	if e.LinesAdded != 42 {
		t.Errorf("lines_added = %d, want 42", e.LinesAdded)
	}
	if e.LinesRemoved != 7 {
		t.Errorf("lines_removed = %d, want 7", e.LinesRemoved)
	}
	if e.ToolCounts["Bash"] != 15 {
		t.Errorf("tool_counts[Bash] = %d, want 15", e.ToolCounts["Bash"])
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\n")
}
