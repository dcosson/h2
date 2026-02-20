package session

import (
	"testing"

	"h2/internal/session/agent"
)

// OTEL integration tests for the adapter path are in the adapter packages
// (claude/adapter_test.go, codex/adapter_test.go). These tests cover
// the Agent-level Metrics() and OtelPort() accessors.

func TestOtelPort_NoAdapter(t *testing.T) {
	// Generic agent has no adapter, so OtelPort returns 0.
	s := New("test", "bash", nil)
	defer s.Stop()

	if port := s.OtelPort(); port != 0 {
		t.Fatalf("expected OtelPort=0 for generic agent, got %d", port)
	}
}

func TestMetrics_DefaultSnapshot(t *testing.T) {
	s := New("test", "bash", nil)
	defer s.Stop()

	m := s.Metrics()
	if m.InputTokens != 0 || m.OutputTokens != 0 || m.TotalCostUSD != 0 {
		t.Fatalf("expected zero metrics, got %+v", m)
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{500, "500"},
		{999, "999"},
		{1000, "1.0k"},
		{1500, "1.5k"},
		{9999, "10.0k"},
		{10000, "10k"},
		{50000, "50k"},
		{999999, "999k"},
		{1000000, "1.0M"},
		{1500000, "1.5M"},
		{10000000, "10M"},
	}
	for _, tt := range tests {
		got := agent.FormatTokens(tt.n)
		if got != tt.want {
			t.Errorf("FormatTokens(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestFormatCost(t *testing.T) {
	tests := []struct {
		usd  float64
		want string
	}{
		{0.0, "$0.00"},
		{0.001, "$0.00"},
		{0.009, "$0.01"},
		{0.01, "$0.01"},
		{0.05, "$0.05"},
		{0.10, "$0.10"},
		{1.23, "$1.23"},
		{10.50, "$10.50"},
	}
	for _, tt := range tests {
		got := agent.FormatCost(tt.usd)
		if got != tt.want {
			t.Errorf("FormatCost(%f) = %q, want %q", tt.usd, got, tt.want)
		}
	}
}
