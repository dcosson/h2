package agent

import (
	"encoding/json"
)

// --- OTEL types ---
// These types are used by adapter parsers to process OTEL payloads.

// OtelLogRecord represents a single log record in OTLP format.
type OtelLogRecord struct {
	Attributes []OtelAttribute `json:"attributes"`
}

// OtelAttribute represents a key-value attribute.
type OtelAttribute struct {
	Key   string        `json:"key"`
	Value OtelAttrValue `json:"value"`
}

// OtelAttrValue holds the attribute value.
type OtelAttrValue struct {
	StringValue string          `json:"stringValue,omitempty"`
	IntValue    json.RawMessage `json:"intValue,omitempty"`
}

// OtelLogsPayload is the top-level structure for /v1/logs.
type OtelLogsPayload struct {
	ResourceLogs []OtelResourceLogs `json:"resourceLogs"`
}

// OtelResourceLogs contains scope logs.
type OtelResourceLogs struct {
	ScopeLogs []OtelScopeLogs `json:"scopeLogs"`
}

// OtelScopeLogs contains log records.
type OtelScopeLogs struct {
	LogRecords []OtelLogRecord `json:"logRecords"`
}

// --- Helpers ---

// getAttr extracts a string attribute value by key from OTEL attributes.
func getAttr(attrs []OtelAttribute, key string) string {
	for _, a := range attrs {
		if a.Key == key {
			return a.Value.StringValue
		}
	}
	return ""
}
