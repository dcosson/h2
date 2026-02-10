package session

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"
	"time"

	"h2/internal/session/agent"
)

func TestOtelCollector_StartsOnRandomPort(t *testing.T) {
	s := New("test", "claude", nil)
	defer s.Stop()

	err := s.StartOtelCollector()
	if err != nil {
		t.Fatalf("StartOtelCollector failed: %v", err)
	}

	port := s.OtelPort()
	if port == 0 {
		t.Fatal("expected non-zero port")
	}
	if port < 1024 || port > 65535 {
		t.Fatalf("port %d out of expected range", port)
	}
}

func TestChildEnv_ReturnsCorrectVars(t *testing.T) {
	s := New("test", "claude", nil)
	defer s.Stop()

	// Before starting collector, should return nil.
	env := s.ChildEnv()
	if env != nil {
		t.Fatal("expected nil env before collector started")
	}

	err := s.StartOtelCollector()
	if err != nil {
		t.Fatalf("StartOtelCollector failed: %v", err)
	}

	env = s.ChildEnv()
	if env == nil {
		t.Fatal("expected non-nil env after collector started")
	}

	// Check required keys.
	expected := []string{
		"CLAUDE_CODE_ENABLE_TELEMETRY",
		"OTEL_METRICS_EXPORTER",
		"OTEL_LOGS_EXPORTER",
		"OTEL_TRACES_EXPORTER",
		"OTEL_EXPORTER_OTLP_PROTOCOL",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_METRIC_EXPORT_INTERVAL",
		"OTEL_LOGS_EXPORT_INTERVAL",
	}
	for _, key := range expected {
		if _, ok := env[key]; !ok {
			t.Errorf("missing env key: %s", key)
		}
	}

	// Check endpoint contains the port.
	endpoint := env["OTEL_EXPORTER_OTLP_ENDPOINT"]
	if endpoint == "" {
		t.Fatal("expected non-empty endpoint")
	}
}

func TestOtelCollector_AcceptsLogsAndSignalsActivity(t *testing.T) {
	s := New("test", "claude", nil)
	defer s.Stop()

	err := s.StartOtelCollector()
	if err != nil {
		t.Fatalf("StartOtelCollector failed: %v", err)
	}

	// Give server time to start.
	time.Sleep(50 * time.Millisecond)

	// Send a logs payload.
	payload := `{
		"resourceLogs": [{
			"scopeLogs": [{
				"logRecords": [{
					"attributes": [
						{"key": "event.name", "value": {"stringValue": "api_request"}}
					]
				}]
			}]
		}]
	}`

	url := s.ChildEnv()["OTEL_EXPORTER_OTLP_ENDPOINT"] + "/v1/logs"
	resp, err := http.Post(url, "application/json", bytes.NewBufferString(payload))
	if err != nil {
		t.Fatalf("POST /v1/logs failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Check that the event was received by checking metrics.
	m := s.Metrics()
	if !m.EventsReceived {
		t.Fatal("expected EventsReceived=true after sending logs")
	}
}

func TestOtelCollector_AcceptsMetrics(t *testing.T) {
	s := New("test", "claude", nil)
	defer s.Stop()

	err := s.StartOtelCollector()
	if err != nil {
		t.Fatalf("StartOtelCollector failed: %v", err)
	}

	// Give server time to start.
	time.Sleep(50 * time.Millisecond)

	// Send a metrics payload (we just accept and discard).
	payload := `{"resourceMetrics": []}`

	url := s.ChildEnv()["OTEL_EXPORTER_OTLP_ENDPOINT"] + "/v1/metrics"
	resp, err := http.Post(url, "application/json", bytes.NewBufferString(payload))
	if err != nil {
		t.Fatalf("POST /v1/metrics failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

// otelOnlyAgentType is a test-only AgentType with OTEL but no hooks,
// so the OtelCollector becomes the primary state source.
type otelOnlyAgentType struct{}

func (t otelOnlyAgentType) Name() string                                     { return "otel-only" }
func (t otelOnlyAgentType) Command() string                                   { return "true" }
func (t otelOnlyAgentType) DisplayCommand() string                             { return "true" }
func (t otelOnlyAgentType) Collectors() agent.CollectorSet                     { return agent.CollectorSet{Otel: true, Hooks: false} }
func (t otelOnlyAgentType) OtelParser() agent.OtelParser                       { return nil }
func (t otelOnlyAgentType) PrependArgs(sessionID string) []string              { return nil }
func (t otelOnlyAgentType) ChildEnv(cp *agent.CollectorPorts) map[string]string { return nil }

func TestOtelCollector_StateTransitionOnEvent(t *testing.T) {
	// Use an otel-only agent type so the OtelCollector is the primary
	// state source (not hooks).
	s := New("test", "true", nil)
	s.Agent = agent.New(otelOnlyAgentType{})
	defer s.Stop()

	if err := s.Agent.StartCollectors(); err != nil {
		t.Fatalf("StartCollectors failed: %v", err)
	}

	// Let it go idle.
	time.Sleep(agent.IdleThreshold + 500*time.Millisecond)
	if got, _ := s.State(); got != agent.StateIdle {
		t.Fatalf("expected StateIdle, got %v", got)
	}

	// Send an OTEL event to wake it up.
	payload := `{
		"resourceLogs": [{
			"scopeLogs": [{
				"logRecords": [{
					"attributes": [
						{"key": "event.name", "value": {"stringValue": "api_request"}}
					]
				}]
			}]
		}]
	}`

	url := fmt.Sprintf("http://127.0.0.1:%d/v1/logs", s.Agent.OtelPort())
	resp, err := http.Post(url, "application/json", bytes.NewBufferString(payload))
	if err != nil {
		t.Fatalf("POST /v1/logs failed: %v", err)
	}
	resp.Body.Close()

	// Give watchState time to process.
	time.Sleep(100 * time.Millisecond)

	if got, _ := s.State(); got != agent.StateActive {
		t.Fatalf("expected StateActive after OTEL event, got %v", got)
	}
}

func TestOtelMetrics_AccumulatesTokensAndCost(t *testing.T) {
	s := New("test", "claude", nil)
	defer s.Stop()

	err := s.StartOtelCollector()
	if err != nil {
		t.Fatalf("StartOtelCollector failed: %v", err)
	}

	// Give server time to start.
	time.Sleep(50 * time.Millisecond)

	// Before any events, EventsReceived should be false.
	m := s.Metrics()
	if m.EventsReceived {
		t.Error("expected EventsReceived=false before any events")
	}

	// Send an api_request with tokens and cost.
	payload := `{
		"resourceLogs": [{
			"scopeLogs": [{
				"logRecords": [{
					"attributes": [
						{"key": "event.name", "value": {"stringValue": "api_request"}},
						{"key": "input_tokens", "value": {"intValue": "1000"}},
						{"key": "output_tokens", "value": {"intValue": "500"}},
						{"key": "cost_usd", "value": {"stringValue": "0.05"}}
					]
				}]
			}]
		}]
	}`

	url := s.ChildEnv()["OTEL_EXPORTER_OTLP_ENDPOINT"] + "/v1/logs"
	resp, err := http.Post(url, "application/json", bytes.NewBufferString(payload))
	if err != nil {
		t.Fatalf("POST /v1/logs failed: %v", err)
	}
	resp.Body.Close()

	// Check metrics.
	m = s.Metrics()
	if !m.EventsReceived {
		t.Error("expected EventsReceived=true after event")
	}
	if m.InputTokens != 1000 {
		t.Errorf("expected InputTokens=1000, got %d", m.InputTokens)
	}
	if m.OutputTokens != 500 {
		t.Errorf("expected OutputTokens=500, got %d", m.OutputTokens)
	}
	if m.TotalTokens != 1500 {
		t.Errorf("expected TotalTokens=1500, got %d", m.TotalTokens)
	}
	if m.TotalCostUSD != 0.05 {
		t.Errorf("expected TotalCostUSD=0.05, got %f", m.TotalCostUSD)
	}
	if m.APIRequestCount != 1 {
		t.Errorf("expected APIRequestCount=1, got %d", m.APIRequestCount)
	}

	// Send another request and verify accumulation.
	resp, err = http.Post(url, "application/json", bytes.NewBufferString(payload))
	if err != nil {
		t.Fatalf("POST /v1/logs failed: %v", err)
	}
	resp.Body.Close()

	m = s.Metrics()
	if m.TotalTokens != 3000 {
		t.Errorf("expected TotalTokens=3000 after second request, got %d", m.TotalTokens)
	}
	if m.APIRequestCount != 2 {
		t.Errorf("expected APIRequestCount=2, got %d", m.APIRequestCount)
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
