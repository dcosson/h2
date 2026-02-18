package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetEnv_New(t *testing.T) {
	env := []string{"FOO=bar", "BAZ=qux"}
	result := setEnv(env, "NEW", "value")
	if len(result) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(result))
	}
	found := false
	for _, e := range result {
		if e == "NEW=value" {
			found = true
		}
	}
	if !found {
		t.Error("NEW=value not found in env")
	}
}

func TestSetEnv_Replace(t *testing.T) {
	env := []string{"FOO=bar", "BAZ=qux"}
	result := setEnv(env, "FOO", "newbar")
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}
	if result[0] != "FOO=newbar" {
		t.Errorf("expected FOO=newbar, got %s", result[0])
	}
}

func TestWriteMetricsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.json")
	metrics := PrintMetrics{
		TotalTokens:  50000,
		InputTokens:  30000,
		OutputTokens: 20000,
		TotalCostUSD: 0.45,
		ModelCosts: map[string]float64{
			"claude-opus-4-6": 0.45,
		},
	}

	if err := writeMetricsFile(path, metrics); err != nil {
		t.Fatalf("writeMetricsFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	var loaded PrintMetrics
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if loaded.TotalTokens != 50000 {
		t.Errorf("TotalTokens = %d, want 50000", loaded.TotalTokens)
	}
	if loaded.TotalCostUSD != 0.45 {
		t.Errorf("TotalCostUSD = %f, want 0.45", loaded.TotalCostUSD)
	}
	if loaded.ModelCosts["claude-opus-4-6"] != 0.45 {
		t.Errorf("ModelCosts[claude-opus-4-6] = %f, want 0.45", loaded.ModelCosts["claude-opus-4-6"])
	}
}

func TestWriteMetricsFile_EmptyMetrics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.json")
	metrics := PrintMetrics{}

	if err := writeMetricsFile(path, metrics); err != nil {
		t.Fatalf("writeMetricsFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	var loaded PrintMetrics
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if loaded.TotalTokens != 0 {
		t.Errorf("TotalTokens = %d, want 0", loaded.TotalTokens)
	}
}

func TestPrintMetrics_JSON(t *testing.T) {
	metrics := PrintMetrics{
		TotalTokens:  100000,
		InputTokens:  60000,
		OutputTokens: 40000,
		TotalCostUSD: 1.23,
		ModelCosts: map[string]float64{
			"haiku": 0.23,
			"opus":  1.00,
		},
	}

	data, err := json.Marshal(metrics)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded PrintMetrics
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.TotalTokens != metrics.TotalTokens {
		t.Errorf("TotalTokens = %d, want %d", decoded.TotalTokens, metrics.TotalTokens)
	}
	if decoded.InputTokens != metrics.InputTokens {
		t.Errorf("InputTokens = %d, want %d", decoded.InputTokens, metrics.InputTokens)
	}
	if decoded.TotalCostUSD != metrics.TotalCostUSD {
		t.Errorf("TotalCostUSD = %f, want %f", decoded.TotalCostUSD, metrics.TotalCostUSD)
	}
	if len(decoded.ModelCosts) != 2 {
		t.Errorf("ModelCosts len = %d, want 2", len(decoded.ModelCosts))
	}
}

func TestRunPrint_SimpleCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := RunPrint(PrintOpts{
		Command: "echo",
		Args:    []string{"hello world"},
		Stdout:  &stdout,
		Stderr:  &stderr,
	})
	if err != nil {
		t.Fatalf("RunPrint: %v", err)
	}

	if !strings.Contains(stdout.String(), "hello world") {
		t.Errorf("stdout = %q, want 'hello world'", stdout.String())
	}
}

func TestRunPrint_WithStdin(t *testing.T) {
	var stdout bytes.Buffer

	err := RunPrint(PrintOpts{
		Command: "cat",
		Stdin:   strings.NewReader("input data"),
		Stdout:  &stdout,
	})
	if err != nil {
		t.Fatalf("RunPrint: %v", err)
	}

	if !strings.Contains(stdout.String(), "input data") {
		t.Errorf("stdout = %q, want 'input data'", stdout.String())
	}
}

func TestRunPrint_WithCWD(t *testing.T) {
	dir := t.TempDir()
	var stdout bytes.Buffer

	err := RunPrint(PrintOpts{
		Command: "pwd",
		CWD:     dir,
		Stdout:  &stdout,
	})
	if err != nil {
		t.Fatalf("RunPrint: %v", err)
	}

	// Resolve symlinks (macOS /private/var vs /var).
	resolvedDir, _ := filepath.EvalSymlinks(dir)
	if !strings.Contains(stdout.String(), resolvedDir) {
		t.Errorf("stdout = %q, want to contain %q", stdout.String(), resolvedDir)
	}
}

func TestRunPrint_WithMetricsFile(t *testing.T) {
	dir := t.TempDir()
	metricsPath := filepath.Join(dir, "metrics.json")

	var stdout bytes.Buffer
	err := RunPrint(PrintOpts{
		Command:     "echo",
		Args:        []string{"test"},
		MetricsFile: metricsPath,
		Stdout:      &stdout,
	})
	if err != nil {
		t.Fatalf("RunPrint: %v", err)
	}

	// Metrics file should be written (echo is a GenericType so no OTEL,
	// but the file should still be created with zero metrics).
	data, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}

	var metrics PrintMetrics
	if err := json.Unmarshal(data, &metrics); err != nil {
		t.Fatalf("unmarshal metrics: %v", err)
	}

	// Generic commands don't emit OTEL, so metrics should be zero.
	if metrics.TotalTokens != 0 {
		t.Errorf("TotalTokens = %d, want 0 (generic command)", metrics.TotalTokens)
	}
}

func TestRunPrint_CommandNotFound(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := RunPrint(PrintOpts{
		Command: "nonexistent-command-12345",
		Stdout:  &stdout,
		Stderr:  &stderr,
	})
	if err == nil {
		t.Error("expected error for nonexistent command")
	}
}

func TestRunPrint_FailingCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := RunPrint(PrintOpts{
		Command: "false",
		Stdout:  &stdout,
		Stderr:  &stderr,
	})
	if err == nil {
		t.Error("expected error for failing command")
	}
}

func TestRunPrint_CLAUDECODEFiltered(t *testing.T) {
	// Set CLAUDECODE to verify it gets filtered.
	t.Setenv("CLAUDECODE", "1")

	var stdout bytes.Buffer
	err := RunPrint(PrintOpts{
		Command: "env",
		Stdout:  &stdout,
	})
	if err != nil {
		t.Fatalf("RunPrint: %v", err)
	}

	// CLAUDECODE should not appear in the child's environment.
	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.HasPrefix(line, "CLAUDECODE=") {
			t.Error("CLAUDECODE should be filtered from child environment")
		}
	}
}
