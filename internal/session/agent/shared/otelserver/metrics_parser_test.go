package otelserver

import (
	"encoding/json"
	"testing"
)

func TestParseOtelMetricsPayload_LOC(t *testing.T) {
	payload := buildTestPayload(t, []testMetric{
		{Name: "claude_code.lines_of_code.count", Attrs: map[string]string{"type": "added"}, Value: 42},
		{Name: "claude_code.lines_of_code.count", Attrs: map[string]string{"type": "removed"}, Value: 7},
	})

	parsed, err := ParseOtelMetricsPayload(payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.LinesAdded != 42 {
		t.Errorf("LinesAdded = %d, want 42", parsed.LinesAdded)
	}
	if parsed.LinesRemoved != 7 {
		t.Errorf("LinesRemoved = %d, want 7", parsed.LinesRemoved)
	}
}

func TestParseOtelMetricsPayload_ActiveTime(t *testing.T) {
	payload := buildTestPayload(t, []testMetric{
		{Name: "claude_code.active_time.total", Attrs: map[string]string{"type": "cli"}, Value: 1.5},
	})

	parsed, err := ParseOtelMetricsPayload(payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.ActiveTimeHrs != 1.5 {
		t.Errorf("ActiveTimeHrs = %f, want 1.5", parsed.ActiveTimeHrs)
	}
}

func TestParseOtelMetricsPayload_PerModelCosts(t *testing.T) {
	payload := buildTestPayload(t, []testMetric{
		{Name: "claude_code.cost.usage", Attrs: map[string]string{"model": "claude-opus-4-6"}, Value: 5.0},
		{Name: "claude_code.cost.usage", Attrs: map[string]string{"model": "claude-haiku-4-5-20251001"}, Value: 0.01},
	})

	parsed, err := ParseOtelMetricsPayload(payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.ModelCosts["claude-opus-4-6"] != 5.0 {
		t.Errorf("ModelCosts[opus] = %f, want 5.0", parsed.ModelCosts["claude-opus-4-6"])
	}
	if parsed.ModelCosts["claude-haiku-4-5-20251001"] != 0.01 {
		t.Errorf("ModelCosts[haiku] = %f, want 0.01", parsed.ModelCosts["claude-haiku-4-5-20251001"])
	}
}

func TestParseOtelMetricsPayload_PerModelTokens(t *testing.T) {
	payload := buildTestPayload(t, []testMetric{
		{Name: "claude_code.token.usage", Attrs: map[string]string{"model": "claude-opus-4-6", "type": "input"}, Value: 1000},
		{Name: "claude_code.token.usage", Attrs: map[string]string{"model": "claude-opus-4-6", "type": "output"}, Value: 200},
		{Name: "claude_code.token.usage", Attrs: map[string]string{"model": "claude-opus-4-6", "type": "cacheRead"}, Value: 5000},
	})

	parsed, err := ParseOtelMetricsPayload(payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tokens := parsed.ModelTokens["claude-opus-4-6"]
	if tokens == nil {
		t.Fatal("no tokens for opus")
	}
	if tokens["input"] != 1000 {
		t.Errorf("input = %d, want 1000", tokens["input"])
	}
	if tokens["output"] != 200 {
		t.Errorf("output = %d, want 200", tokens["output"])
	}
	if tokens["cacheRead"] != 5000 {
		t.Errorf("cacheRead = %d, want 5000", tokens["cacheRead"])
	}
}

func TestParseOtelMetricsPayload_InvalidJSON(t *testing.T) {
	_, err := ParseOtelMetricsPayload([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// --- test helpers ---

type testMetric struct {
	Name  string
	Attrs map[string]string
	Value float64
}

func buildTestPayload(t *testing.T, metrics []testMetric) []byte {
	t.Helper()
	var otelMetrics []OtelMetric
	for _, m := range metrics {
		var attrs []OtelAttribute
		for k, v := range m.Attrs {
			attrs = append(attrs, OtelAttribute{
				Key:   k,
				Value: OtelAttrValue{StringValue: v},
			})
		}
		val := m.Value
		otelMetrics = append(otelMetrics, OtelMetric{
			Name: m.Name,
			Sum: &OtelMetricSum{
				DataPoints: []OtelDataPoint{{
					Attributes: attrs,
					AsDouble:   &val,
				}},
			},
		})
	}

	payload := OtelMetricsPayload{
		ResourceMetrics: []OtelResourceMetrics{{
			ScopeMetrics: []OtelScopeMetrics{{
				Metrics: otelMetrics,
			}},
		}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal test payload: %v", err)
	}
	return data
}
