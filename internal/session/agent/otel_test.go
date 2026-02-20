package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"h2/internal/session/agent/adapter"
	"h2/internal/session/agent/monitor"
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

// stubAdapter is a minimal AgentAdapter for testing.
type stubAdapter struct{}

func (s *stubAdapter) Name() string { return "stub" }
func (s *stubAdapter) PrepareForLaunch(agentName, sessionID string) (adapter.LaunchConfig, error) {
	return adapter.LaunchConfig{}, nil
}
func (s *stubAdapter) Start(ctx context.Context, events chan<- monitor.AgentEvent) error {
	<-ctx.Done()
	return nil
}
func (s *stubAdapter) HandleHookEvent(eventName string, payload json.RawMessage) bool {
	return false
}
func (s *stubAdapter) Stop() {}

func TestMetrics_AdaptedAgent_BridgesFromMonitor(t *testing.T) {
	a := New(nil)
	a.adapter = &stubAdapter{} // mark as adapted agent

	// Push a TurnCompleted event directly to the monitor's events channel.
	a.agentMonitor.Events() <- monitor.AgentEvent{
		Type:      monitor.EventTurnCompleted,
		Timestamp: time.Now(),
		Data: monitor.TurnCompletedData{
			InputTokens:  500,
			OutputTokens: 200,
			CachedTokens: 100,
			CostUSD:      0.05,
		},
	}

	// Start the monitor to process the event.
	ctx, cancel := context.WithCancel(context.Background())
	go a.agentMonitor.Run(ctx)

	// Give the monitor time to process.
	time.Sleep(50 * time.Millisecond)
	cancel()

	m := a.Metrics()
	if !m.EventsReceived {
		t.Error("EventsReceived should be true for adapted agent with token data")
	}
	if m.InputTokens != 500 {
		t.Errorf("InputTokens = %d, want 500", m.InputTokens)
	}
	if m.OutputTokens != 200 {
		t.Errorf("OutputTokens = %d, want 200", m.OutputTokens)
	}
	if m.TotalTokens != 700 {
		t.Errorf("TotalTokens = %d, want 700", m.TotalTokens)
	}
	if m.TotalCostUSD != 0.05 {
		t.Errorf("TotalCostUSD = %f, want 0.05", m.TotalCostUSD)
	}
}

func TestMetrics_AdaptedAgent_NoData_EventsReceivedFalse(t *testing.T) {
	a := New(nil)
	a.adapter = &stubAdapter{} // mark as adapted agent

	m := a.Metrics()
	if m.EventsReceived {
		t.Error("EventsReceived should be false when no events have been processed")
	}
}

func TestMetrics_LegacyAgent_UsesOtelMetrics(t *testing.T) {
	a := New(nil)
	// No adapter set — legacy path.
	a.metrics.Update(OtelMetricsDelta{
		InputTokens:  100,
		OutputTokens: 50,
		CostUSD:      0.01,
		IsAPIRequest: true,
	})

	m := a.Metrics()
	if !m.EventsReceived {
		t.Error("EventsReceived should be true for legacy agent with data")
	}
	if m.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", m.InputTokens)
	}
}

func TestSetOtelLogFiles_SkippedForAdaptedAgent(t *testing.T) {
	dir := t.TempDir()
	a := New(NewClaudeCodeType())

	if err := a.SetOtelLogFiles(dir); err != nil {
		t.Fatalf("SetOtelLogFiles: %v", err)
	}
	defer a.Stop()

	// Files should NOT be created for adapted agents.
	if _, err := os.Stat(filepath.Join(dir, "otel-logs.jsonl")); err == nil {
		t.Error("otel-logs.jsonl should not be created for adapted agents")
	}
	if a.otelLogsFile != nil {
		t.Error("otelLogsFile should be nil for adapted agents")
	}
}
