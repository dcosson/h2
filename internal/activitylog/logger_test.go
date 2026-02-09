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

	l.HookEvent("PreToolUse", "Bash")

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
	if e.SessionID != "sess-123" {
		t.Errorf("session_id = %q, want %q", e.SessionID, "sess-123")
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

	l.HookEvent("SessionStart", "")

	lines := readLines(t, path)
	if strings.Contains(lines[0], "tool_name") {
		t.Error("expected tool_name to be omitted when empty")
	}
}

func TestPermissionDecision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.log")
	l := New(true, path, "agent", "sess")
	defer l.Close()

	l.PermissionDecision("Bash", "allow", "Safe tool")

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

	l.HookEvent("PreToolUse", "Bash")
	l.PermissionDecision("Bash", "allow", "ok")
	l.OtelMetrics(100, 200, 0.01)
	l.OtelConnected("/v1/logs")
	l.StateChange("active", "idle")

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected no file to be created when disabled")
	}
}

func TestNopLoggerIsNoop(t *testing.T) {
	l := Nop()
	// Should not panic.
	l.HookEvent("PreToolUse", "Bash")
	l.PermissionDecision("Bash", "allow", "ok")
	l.OtelMetrics(100, 200, 0.01)
	l.OtelConnected("/v1/logs")
	l.StateChange("active", "idle")
	l.Close()
}

func TestMultipleEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.log")
	l := New(true, path, "agent", "sess")
	defer l.Close()

	l.HookEvent("SessionStart", "")
	l.HookEvent("PreToolUse", "Bash")
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

	l.HookEvent("Stop", "")

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
