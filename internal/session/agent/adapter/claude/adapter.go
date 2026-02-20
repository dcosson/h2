// Package claude implements the AgentAdapter for Claude Code.
// It translates Claude Code's OTEL telemetry and hook events into
// normalized AgentEvents.
package claude

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"h2/internal/activitylog"
	"h2/internal/session/agent/adapter"
	"h2/internal/session/agent/monitor"
	"h2/internal/session/agent/shared/otelserver"
)

// ClaudeCodeAdapter translates Claude Code telemetry into AgentEvents.
// It owns an OtelServer (for /v1/logs and /v1/metrics), a HookHandler
// (for lifecycle hooks), and a SessionLogCollector (for peek data).
type ClaudeCodeAdapter struct {
	otelServer  *otelserver.OtelServer
	hookHandler *HookHandler
	sessionLog  *SessionLogCollector
	otelParser  *OtelParser
	activityLog *activitylog.Logger
	sessionID   string

	// internalCh buffers events from callbacks and hook handlers.
	// Start() forwards these to the external events channel.
	internalCh chan monitor.AgentEvent
}

// New creates a ClaudeCodeAdapter.
func New(log *activitylog.Logger) *ClaudeCodeAdapter {
	if log == nil {
		log = activitylog.Nop()
	}
	ch := make(chan monitor.AgentEvent, 256)
	return &ClaudeCodeAdapter{
		activityLog: log,
		internalCh:  ch,
		hookHandler: NewHookHandler(ch, log),
		otelParser:  NewOtelParser(ch),
	}
}

// Name returns the adapter identifier.
func (a *ClaudeCodeAdapter) Name() string {
	return "claude-code"
}

// PrepareForLaunch generates a session ID, creates the OTEL server, and
// returns the env vars and CLI args needed to launch Claude Code with
// telemetry enabled.
func (a *ClaudeCodeAdapter) PrepareForLaunch(agentName string) (adapter.LaunchConfig, error) {
	a.sessionID = uuid.New().String()

	// Create OTEL server with callbacks that parse and emit events.
	s, err := otelserver.New(otelserver.Callbacks{
		OnLogs:    a.otelParser.OnLogs,
		OnMetrics: a.otelParser.OnMetrics,
	})
	if err != nil {
		return adapter.LaunchConfig{}, fmt.Errorf("create otel server: %w", err)
	}
	a.otelServer = s

	endpoint := fmt.Sprintf("http://127.0.0.1:%d", s.Port)
	return adapter.LaunchConfig{
		PrependArgs: []string{"--session-id", a.sessionID},
		Env: map[string]string{
			"CLAUDE_CODE_ENABLE_TELEMETRY": "1",
			"OTEL_METRICS_EXPORTER":        "otlp",
			"OTEL_LOGS_EXPORTER":           "otlp",
			"OTEL_TRACES_EXPORTER":         "none",
			"OTEL_EXPORTER_OTLP_PROTOCOL":  "http/json",
			"OTEL_EXPORTER_OTLP_ENDPOINT":  endpoint,
			"OTEL_METRIC_EXPORT_INTERVAL":  "5000",
			"OTEL_LOGS_EXPORT_INTERVAL":    "1000",
		},
	}, nil
}

// Start forwards internal events to the external channel and blocks
// until ctx is cancelled. If a session log path is configured, starts
// the session log tailer.
func (a *ClaudeCodeAdapter) Start(ctx context.Context, events chan<- monitor.AgentEvent) error {
	// Start session log tailer if configured.
	if a.sessionLog != nil {
		go a.sessionLog.Run(ctx, a.internalCh)
	}

	// Forward internal events to the external channel.
	for {
		select {
		case ev := <-a.internalCh:
			select {
			case events <- ev:
			case <-ctx.Done():
				return nil
			}
		case <-ctx.Done():
			return nil
		}
	}
}

// HandleHookEvent delegates hook events to the HookHandler, which
// translates them into AgentEvents.
func (a *ClaudeCodeAdapter) HandleHookEvent(eventName string, payload json.RawMessage) bool {
	return a.hookHandler.ProcessEvent(eventName, payload)
}

// Stop cleans up the OTEL server and other resources.
func (a *ClaudeCodeAdapter) Stop() {
	if a.otelServer != nil {
		a.otelServer.Stop()
	}
}

// SessionID returns the generated session ID (available after PrepareForLaunch).
func (a *ClaudeCodeAdapter) SessionID() string {
	return a.sessionID
}

// OtelPort returns the OTEL server port (available after PrepareForLaunch).
func (a *ClaudeCodeAdapter) OtelPort() int {
	if a.otelServer != nil {
		return a.otelServer.Port
	}
	return 0
}

// SetSessionLogPath configures the path to Claude Code's session JSONL
// for the session log tailer. Must be called before Start().
func (a *ClaudeCodeAdapter) SetSessionLogPath(path string) {
	if path != "" {
		a.sessionLog = NewSessionLogCollector(path)
	}
}
