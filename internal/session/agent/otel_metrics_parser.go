package agent

import "encoding/json"

// --- OTLP Metrics JSON types ---

// OtelMetricsPayload is the top-level structure for /v1/metrics.
type OtelMetricsPayload struct {
	ResourceMetrics []OtelResourceMetrics `json:"resourceMetrics"`
}

// OtelResourceMetrics contains scope metrics.
type OtelResourceMetrics struct {
	ScopeMetrics []OtelScopeMetrics `json:"scopeMetrics"`
}

// OtelScopeMetrics contains metrics.
type OtelScopeMetrics struct {
	Metrics []OtelMetric `json:"metrics"`
}

// OtelMetric represents a single metric with its data points.
type OtelMetric struct {
	Name string         `json:"name"`
	Sum  *OtelMetricSum `json:"sum,omitempty"`
}

// OtelMetricSum holds sum-type metric data.
type OtelMetricSum struct {
	DataPoints []OtelDataPoint `json:"dataPoints"`
}

// OtelDataPoint holds a single data point with attributes and value.
type OtelDataPoint struct {
	Attributes []OtelAttribute `json:"attributes"`
	AsDouble   *float64        `json:"asDouble,omitempty"`
	AsInt      json.RawMessage `json:"asInt,omitempty"`
}

// ParseOtelMetricsPayload parses an OTLP metrics payload and returns
// cumulative metric values keyed by metric name and relevant attributes.
// Values are cumulative (monotonic counters) — take the latest directly.
func ParseOtelMetricsPayload(body []byte) (*ParsedOtelMetrics, error) {
	var payload OtelMetricsPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	result := &ParsedOtelMetrics{
		ModelCosts:  make(map[string]float64),
		ModelTokens: make(map[string]map[string]int64),
	}

	for _, rm := range payload.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if m.Sum == nil {
					continue
				}
				for _, dp := range m.Sum.DataPoints {
					val := dataPointValue(dp)
					attrs := dataPointAttrs(dp)

					switch m.Name {
					case "claude_code.lines_of_code.count":
						switch attrs["type"] {
						case "added":
							result.LinesAdded = int64(val)
						case "removed":
							result.LinesRemoved = int64(val)
						}

					case "claude_code.active_time.total":
						result.ActiveTimeHrs = val

					case "claude_code.cost.usage":
						if model := attrs["model"]; model != "" {
							result.ModelCosts[model] = val
						}

					case "claude_code.token.usage":
						model := attrs["model"]
						tokenType := attrs["type"]
						if model != "" && tokenType != "" {
							if result.ModelTokens[model] == nil {
								result.ModelTokens[model] = make(map[string]int64)
							}
							result.ModelTokens[model][tokenType] = int64(val)
						}
					}
				}
			}
		}
	}

	return result, nil
}

// ParsedOtelMetrics holds the extracted values from an OTLP metrics payload.
type ParsedOtelMetrics struct {
	LinesAdded    int64
	LinesRemoved  int64
	ActiveTimeHrs float64
	ModelCosts    map[string]float64          // model -> cost USD
	ModelTokens   map[string]map[string]int64 // model -> type -> count
}

// dataPointValue extracts the numeric value from a data point.
func dataPointValue(dp OtelDataPoint) float64 {
	if dp.AsDouble != nil {
		return *dp.AsDouble
	}
	if len(dp.AsInt) > 0 {
		s := string(dp.AsInt)
		if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
			s = s[1 : len(s)-1]
		}
		var v float64
		if err := json.Unmarshal([]byte(s), &v); err == nil {
			return v
		}
	}
	return 0
}

// dataPointAttrs extracts relevant attributes as a map (skipping user/session/org IDs).
func dataPointAttrs(dp OtelDataPoint) map[string]string {
	result := make(map[string]string)
	for _, a := range dp.Attributes {
		switch a.Key {
		case "session.id", "user.id", "user.email", "user.account_uuid", "organization.id", "terminal.type":
			continue
		default:
			result[a.Key] = a.Value.StringValue
		}
	}
	return result
}
