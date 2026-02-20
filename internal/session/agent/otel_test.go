package agent

import (
	"context"
	"testing"
	"time"

	"h2/internal/session/agent/monitor"
)

func TestMetrics_FromMonitor(t *testing.T) {
	a := New(nil)

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
		t.Error("EventsReceived should be true after token data")
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

func TestMetrics_NoData_EventsReceivedFalse(t *testing.T) {
	a := New(nil)

	m := a.Metrics()
	if m.EventsReceived {
		t.Error("EventsReceived should be false when no events have been processed")
	}
}

func TestOtelPort_NilHarness(t *testing.T) {
	a := New(nil)
	if port := a.OtelPort(); port != 0 {
		t.Errorf("OtelPort = %d, want 0 for nil harness", port)
	}
}

func TestHandleOutput_NilHarness(t *testing.T) {
	a := New(nil)
	// Should not panic with nil harness.
	a.HandleOutput()
}

func TestStop_NilHarness(t *testing.T) {
	a := New(nil)
	// Should not panic with nil harness.
	a.Stop()
}

func TestStop_Idempotent(t *testing.T) {
	a := New(nil)
	a.Stop()
	a.Stop() // second stop should not panic
}
