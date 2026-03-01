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
	l.OtelConnected("/v1/logs")
	l.StateChange("active", "idle")
	l.SessionSummary(SessionSummaryData{InputTokens: 100, OutputTokens: 200, CostUSD: 0.01, APIRequests: 1, ToolCalls: 2})

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected no file to be created when disabled")
	}
}

func TestNopLoggerIsNoop(t *testing.T) {
	l := Nop()
	// Should not panic.
	l.HookEvent("sess", "PreToolUse", "Bash")
	l.PermissionDecision("sess", "Bash", "allow", "ok")
	l.OtelConnected("/v1/logs")
	l.StateChange("active", "idle")
	l.SessionSummary(SessionSummaryData{InputTokens: 100, OutputTokens: 200, CostUSD: 0.01, APIRequests: 1, ToolCalls: 2})
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

	l.SessionSummary(SessionSummaryData{
		InputTokens:     5000,
		OutputTokens:    3000,
		TotalTokens:     8000,
		CostUSD:         0.42,
		APIRequests:     10,
		ToolCalls:       25,
		LinesAdded:      42,
		LinesRemoved:    7,
		ToolCounts:      map[string]int64{"Bash": 15, "Read": 8},
		ActiveTimeHrs:   1.5,
		ModelCosts:      map[string]float64{"claude-sonnet": 0.30, "claude-haiku": 0.12},
		ModelTokens:     map[string]map[string]int64{"claude-sonnet": {"input": 3000, "output": 2000}},
		ToolUseCount:    20,
		Uptime:          "5m30s",
		GitFilesChanged: 3,
		GitLinesAdded:   100,
		GitLinesRemoved: 50,
	})

	lines := readLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	var e struct {
		Event           string                      `json:"event"`
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
	if e.TotalTokens != 8000 {
		t.Errorf("total_tokens = %d, want 8000", e.TotalTokens)
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
	if e.ActiveTimeHrs != 1.5 {
		t.Errorf("active_time_hrs = %f, want 1.5", e.ActiveTimeHrs)
	}
	if e.ModelCosts["claude-sonnet"] != 0.30 {
		t.Errorf("model_costs[claude-sonnet] = %f, want 0.30", e.ModelCosts["claude-sonnet"])
	}
	if e.ModelTokens["claude-sonnet"]["input"] != 3000 {
		t.Errorf("model_tokens[claude-sonnet][input] = %d, want 3000", e.ModelTokens["claude-sonnet"]["input"])
	}
	if e.ToolUseCount != 20 {
		t.Errorf("tool_use_count = %d, want 20", e.ToolUseCount)
	}
	if e.Uptime != "5m30s" {
		t.Errorf("uptime = %q, want %q", e.Uptime, "5m30s")
	}
	if e.GitFilesChanged != 3 {
		t.Errorf("git_files_changed = %d, want 3", e.GitFilesChanged)
	}
	if e.GitLinesAdded != 100 {
		t.Errorf("git_lines_added = %d, want 100", e.GitLinesAdded)
	}
	if e.GitLinesRemoved != 50 {
		t.Errorf("git_lines_removed = %d, want 50", e.GitLinesRemoved)
	}
}

func TestSessionSummaryZeroValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.log")
	l := New(true, path, "agent", "sess")
	defer l.Close()

	l.SessionSummary(SessionSummaryData{})

	lines := readLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	// Verify all fields are present even with zero values (no omitempty).
	line := lines[0]
	for _, field := range []string{
		"input_tokens", "output_tokens", "total_tokens", "cost_usd",
		"api_requests", "tool_calls", "lines_added", "lines_removed",
		"tool_counts", "active_time_hrs", "model_costs", "model_tokens",
		"tool_use_count", "uptime", "git_files_changed", "git_lines_added",
		"git_lines_removed",
	} {
		if !strings.Contains(line, `"`+field+`"`) {
			t.Errorf("expected field %q to be present in output", field)
		}
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
