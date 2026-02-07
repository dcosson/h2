package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Agent wraps the OTEL collector, agent helper, and metrics for a session.
type Agent struct {
	helper      AgentHelper
	metrics     *OtelMetrics
	listener    net.Listener
	server      *http.Server
	port        int
	otelNotify  chan struct{} // buffered(1), signaled on OTEL event
	stopCh      chan struct{}
}

// New creates a new Agent with the given helper.
func New(helper AgentHelper) *Agent {
	return &Agent{
		helper:     helper,
		metrics:    &OtelMetrics{},
		otelNotify: make(chan struct{}, 1),
		stopCh:     make(chan struct{}),
	}
}

// OtelNotify returns the channel that is signaled on OTEL events.
func (a *Agent) OtelNotify() <-chan struct{} {
	return a.otelNotify
}

// SetHelper sets the agent-specific helper.
func (a *Agent) SetHelper(helper AgentHelper) {
	a.helper = helper
}

// Metrics returns a snapshot of the current OTEL metrics.
func (a *Agent) Metrics() OtelMetricsSnapshot {
	if a.metrics == nil {
		return OtelMetricsSnapshot{}
	}
	return a.metrics.Snapshot()
}

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

// StartOtelCollector starts the OTEL HTTP server on a random port.
func (a *Agent) StartOtelCollector() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen for otel: %w", err)
	}
	a.listener = ln
	a.port = ln.Addr().(*net.TCPAddr).Port

	otelDebugLog("OTEL collector started on port %d", a.port)
	if a.helper != nil {
		env := a.helper.OtelEnv(a.port)
		for k, v := range env {
			otelDebugLog("  env: %s=%s", k, v)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/logs", a.handleOtelLogs)
	mux.HandleFunc("/v1/metrics", a.handleOtelMetrics)

	a.server = &http.Server{Handler: mux}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		wg.Done()
		a.server.Serve(ln)
	}()
	wg.Wait() // wait for goroutine to start

	return nil
}

// StopOtelCollector shuts down the OTEL HTTP server.
func (a *Agent) StopOtelCollector() {
	if a.server != nil {
		a.server.Shutdown(context.Background())
	}
	if a.listener != nil {
		a.listener.Close()
	}
}

// OtelPort returns the port the OTEL collector is listening on.
func (a *Agent) OtelPort() int {
	return a.port
}

// OtelEnv returns environment variables to inject into the child process
// for OTEL telemetry export. Delegates to the agent helper.
func (a *Agent) OtelEnv() map[string]string {
	if a.helper == nil {
		return nil
	}
	return a.helper.OtelEnv(a.port)
}

// Stop cleans up the agent resources.
func (a *Agent) Stop() {
	a.StopOtelCollector()
}

// otelDebugLog writes a debug message to ~/.h2/otel-debug.log
func otelDebugLog(format string, args ...interface{}) {
	logPath := filepath.Join(os.Getenv("HOME"), ".h2", "otel-debug.log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	timestamp := time.Now().Format("15:04:05.000")
	fmt.Fprintf(f, "[%s] %s\n", timestamp, fmt.Sprintf(format, args...))
}

// processLogs extracts events from an OTLP logs payload.
func (a *Agent) processLogs(payload OtelLogsPayload) {
	for _, rl := range payload.ResourceLogs {
		otelDebugLog("  resourceLog has %d scopeLogs", len(rl.ScopeLogs))
		for _, sl := range rl.ScopeLogs {
			otelDebugLog("    scopeLog has %d logRecords", len(sl.LogRecords))
			for _, lr := range sl.LogRecords {
				otelDebugLog("      logRecord has %d attrs", len(lr.Attributes))
				eventName := getAttr(lr.Attributes, "event.name")
				if eventName != "" {
					otelDebugLog("event: %s", eventName)
					a.noteOtelEvent()

					// Mark that we received an event (for connection status)
					if a.metrics != nil {
						a.metrics.NoteEvent()
					}

					// Parse metrics if we have an agent helper with a parser
					if a.helper != nil && a.metrics != nil {
						if parser := a.helper.OtelParser(); parser != nil {
							if delta := parser.ParseLogRecord(lr); delta != nil {
								otelDebugLog("  -> tokens: in=%d out=%d cost=%.4f", delta.InputTokens, delta.OutputTokens, delta.CostUSD)
								a.metrics.Update(*delta)
							}
						}
					}
				}
			}
		}
	}
}

// handleOtelLogs handles POST /v1/logs from OTLP exporters.
func (a *Agent) handleOtelLogs(w http.ResponseWriter, r *http.Request) {
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
		a.noteOtelEvent()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
		return
	}

	a.processLogs(payload)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("{}"))
}

// handleOtelMetrics handles POST /v1/metrics from OTLP exporters.
func (a *Agent) handleOtelMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()

	otelDebugLog("/v1/metrics received %d bytes", len(body))

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("{}"))
}

// noteOtelEvent signals that an OTEL event was received.
// Safe to call from HTTP handlers — does only a non-blocking channel send.
func (a *Agent) noteOtelEvent() {
	select {
	case a.otelNotify <- struct{}{}:
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
