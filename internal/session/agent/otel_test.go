package agent

import (
	"os"
	"path/filepath"
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
