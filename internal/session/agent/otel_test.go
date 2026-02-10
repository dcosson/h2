package agent

import (
	"net/http"
	"net/http/httptest"
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

func TestHandleOtelLogs_WritesRawPayload(t *testing.T) {
	dir := t.TempDir()
	a := New(nil)
	a.metrics = &OtelMetrics{}
	if err := a.SetOtelLogFiles(dir); err != nil {
		t.Fatalf("SetOtelLogFiles: %v", err)
	}
	defer a.Stop()

	payload := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[]}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/logs", strings.NewReader(payload))
	w := httptest.NewRecorder()
	a.handleOtelLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	data, err := os.ReadFile(filepath.Join(dir, "otel-logs.jsonl"))
	if err != nil {
		t.Fatalf("read otel-logs.jsonl: %v", err)
	}
	got := strings.TrimSpace(string(data))
	if got != payload {
		t.Errorf("expected payload %q, got %q", payload, got)
	}
}

func TestHandleOtelMetrics_WritesRawPayload(t *testing.T) {
	dir := t.TempDir()
	a := New(nil)
	a.metrics = &OtelMetrics{}
	if err := a.SetOtelLogFiles(dir); err != nil {
		t.Fatalf("SetOtelLogFiles: %v", err)
	}
	defer a.Stop()

	payload := `{"resourceMetrics":[{"scopeMetrics":[]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", strings.NewReader(payload))
	w := httptest.NewRecorder()
	a.handleOtelMetrics(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	data, err := os.ReadFile(filepath.Join(dir, "otel-metrics.jsonl"))
	if err != nil {
		t.Fatalf("read otel-metrics.jsonl: %v", err)
	}
	got := strings.TrimSpace(string(data))
	if got != payload {
		t.Errorf("expected payload %q, got %q", payload, got)
	}
}

func TestHandleOtelLogs_MultiplePayloads_Appended(t *testing.T) {
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
		req := httptest.NewRequest(http.MethodPost, "/v1/logs", strings.NewReader(p))
		w := httptest.NewRecorder()
		a.handleOtelLogs(w, req)
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

func TestHandleOtelLogs_NoFiles_NoError(t *testing.T) {
	// When otel log files are not set, handlers should still work fine.
	a := New(nil)
	a.metrics = &OtelMetrics{}
	defer a.Stop()

	payload := `{"resourceLogs":[]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/logs", strings.NewReader(payload))
	w := httptest.NewRecorder()
	a.handleOtelLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
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
