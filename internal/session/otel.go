package session

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
)

// OtelLogRecord represents a single log record in OTLP format.
type OtelLogRecord struct {
	Attributes []OtelAttribute `json:"attributes"`
}

// OtelAttribute represents a key-value attribute.
type OtelAttribute struct {
	Key   string          `json:"key"`
	Value OtelAttrValue   `json:"value"`
}

// OtelAttrValue holds the attribute value.
type OtelAttrValue struct {
	StringValue string `json:"stringValue,omitempty"`
	IntValue    string `json:"intValue,omitempty"`
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

// StartOtelCollector starts the OTEL HTTP server on a random port.
// Must be called before Start() so the port is available for child env vars.
func (s *Session) StartOtelCollector() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen for otel: %w", err)
	}
	s.otelListener = ln
	s.otelPort = ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/logs", s.handleOtelLogs)
	mux.HandleFunc("/v1/metrics", s.handleOtelMetrics)

	s.otelServer = &http.Server{Handler: mux}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		wg.Done()
		s.otelServer.Serve(ln)
	}()
	wg.Wait() // wait for goroutine to start

	return nil
}

// StopOtelCollector shuts down the OTEL HTTP server.
func (s *Session) StopOtelCollector() {
	if s.otelServer != nil {
		s.otelServer.Shutdown(context.Background())
	}
	if s.otelListener != nil {
		s.otelListener.Close()
	}
}

// OtelPort returns the port the OTEL collector is listening on.
func (s *Session) OtelPort() int {
	return s.otelPort
}

// OtelEnv returns environment variables to inject into the child process
// for OTEL telemetry export.
func (s *Session) OtelEnv() map[string]string {
	if s.otelPort == 0 {
		return nil
	}
	return map[string]string{
		"CLAUDE_CODE_ENABLE_TELEMETRY": "1",
		"OTEL_METRICS_EXPORTER":        "otlp",
		"OTEL_LOGS_EXPORTER":           "otlp",
		"OTEL_EXPORTER_OTLP_PROTOCOL":  "http/json",
		"OTEL_EXPORTER_OTLP_ENDPOINT":  fmt.Sprintf("http://127.0.0.1:%d", s.otelPort),
	}
}

// handleOtelLogs handles POST /v1/logs from OTLP exporters.
func (s *Session) handleOtelLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	var payload OtelLogsPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		// Could be protobuf — just signal activity anyway
		s.NoteOtelEvent()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
		return
	}

	// Extract event names and signal activity
	for _, rl := range payload.ResourceLogs {
		for _, sl := range rl.ScopeLogs {
			for _, lr := range sl.LogRecords {
				eventName := getAttr(lr.Attributes, "event.name")
				if eventName != "" {
					s.NoteOtelEvent()
					// Could store event details for debugging/metrics
				}
			}
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("{}"))
}

// handleOtelMetrics handles POST /v1/metrics from OTLP exporters.
// We don't need metrics for idle detection, but accept them to avoid errors.
func (s *Session) handleOtelMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Drain and discard the body
	io.Copy(io.Discard, r.Body)
	r.Body.Close()

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("{}"))
}

// NoteOtelEvent signals that an OTEL event was received.
// Safe to call from HTTP handlers — does only a non-blocking channel send.
func (s *Session) NoteOtelEvent() {
	select {
	case s.otelNotify <- struct{}{}:
	default:
	}
}

// getAttr extracts a string attribute value by key.
func getAttr(attrs []OtelAttribute, key string) string {
	for _, a := range attrs {
		if a.Key == key {
			return a.Value.StringValue
		}
	}
	return ""
}
