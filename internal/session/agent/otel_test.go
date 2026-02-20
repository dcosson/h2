package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetOtelLogFiles_CreatesFiles(t *testing.T) {
	dir := t.TempDir()
	a := New(nil)

	if err := a.SetOtelLogFiles(dir); err != nil {
		t.Fatalf("SetOtelLogFiles: %v", err)
	}
	defer a.Stop()

	// Files should exist.
	for _, name := range []string{"otel-logs.jsonl", "otel-metrics.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s to exist: %v", name, err)
		}
	}
}

func TestSetOtelLogFiles_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "logs")
	a := New(nil)

	if err := a.SetOtelLogFiles(dir); err != nil {
		t.Fatalf("SetOtelLogFiles: %v", err)
	}
	defer a.Stop()

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("expected dir to be created: %v", err)
	}
}

func TestOnOtelLogs_WritesRawPayload(t *testing.T) {
	dir := t.TempDir()
	a := New(nil)
	a.metrics = &OtelMetrics{}
	if err := a.SetOtelLogFiles(dir); err != nil {
		t.Fatalf("SetOtelLogFiles: %v", err)
	}
	defer a.Stop()

	payload := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[]}]}]}`
	a.onOtelLogs([]byte(payload))

	data, err := os.ReadFile(filepath.Join(dir, "otel-logs.jsonl"))
	if err != nil {
		t.Fatalf("read otel-logs.jsonl: %v", err)
	}
	got := strings.TrimSpace(string(data))
	if got != payload {
		t.Errorf("expected payload %q, got %q", payload, got)
	}
}

func TestOnOtelMetrics_WritesRawPayload(t *testing.T) {
	dir := t.TempDir()
	a := New(nil)
	a.metrics = &OtelMetrics{}
	if err := a.SetOtelLogFiles(dir); err != nil {
		t.Fatalf("SetOtelLogFiles: %v", err)
	}
	defer a.Stop()

	payload := `{"resourceMetrics":[{"scopeMetrics":[]}]}`
	a.onOtelMetrics([]byte(payload))

	data, err := os.ReadFile(filepath.Join(dir, "otel-metrics.jsonl"))
	if err != nil {
		t.Fatalf("read otel-metrics.jsonl: %v", err)
	}
	got := strings.TrimSpace(string(data))
	if got != payload {
		t.Errorf("expected payload %q, got %q", payload, got)
	}
}

func TestOnOtelLogs_MultiplePayloads_Appended(t *testing.T) {
	dir := t.TempDir()
	a := New(nil)
	a.metrics = &OtelMetrics{}
	if err := a.SetOtelLogFiles(dir); err != nil {
		t.Fatalf("SetOtelLogFiles: %v", err)
	}
	defer a.Stop()

	payloads := []string{
		`{"resourceLogs":[]}`,
		`{"resourceLogs":[{"scopeLogs":[]}]}`,
	}

	for _, p := range payloads {
		a.onOtelLogs([]byte(p))
	}

	data, err := os.ReadFile(filepath.Join(dir, "otel-logs.jsonl"))
	if err != nil {
		t.Fatalf("read otel-logs.jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	for i, want := range payloads {
		if lines[i] != want {
			t.Errorf("line %d: expected %q, got %q", i, want, lines[i])
		}
	}
}

func TestOnOtelLogs_NoFiles_NoError(t *testing.T) {
	// When otel log files are not set, callbacks should still work fine.
	a := New(nil)
	a.metrics = &OtelMetrics{}
	defer a.Stop()

	payload := `{"resourceLogs":[]}`
	// Should not panic or error.
	a.onOtelLogs([]byte(payload))
}

func TestStopClosesOtelFiles(t *testing.T) {
	dir := t.TempDir()
	a := New(nil)
	if err := a.SetOtelLogFiles(dir); err != nil {
		t.Fatalf("SetOtelLogFiles: %v", err)
	}

	a.Stop()

	if a.otelLogsFile != nil {
		t.Error("expected otelLogsFile to be nil after Stop")
	}
	if a.otelMetricsFile != nil {
		t.Error("expected otelMetricsFile to be nil after Stop")
	}
}

func TestStartOtelCollector_UsesSharedServer(t *testing.T) {
	a := New(nil)
	if err := a.StartOtelCollector(); err != nil {
		t.Fatalf("StartOtelCollector: %v", err)
	}
	defer a.Stop()

	if a.otelServer == nil {
		t.Fatal("expected otelServer to be set")
	}
	if a.OtelPort() == 0 {
		t.Fatal("expected non-zero OtelPort")
	}
}
