// Package codex implements the AgentAdapter for OpenAI Codex CLI.
// It translates Codex's OTEL trace events into normalized AgentEvents.
package codex

import (
	"context"
	"encoding/json"
	"fmt"

	"h2/internal/activitylog"
	"h2/internal/session/agent/adapter"
	"h2/internal/session/agent/monitor"
	"h2/internal/session/agent/shared/otelserver"
)

// CodexAdapter translates Codex CLI telemetry into AgentEvents.
// Codex emits OTEL traces via /v1/traces. The adapter owns an OtelServer
// and an OtelParser that converts trace spans into normalized events.
type CodexAdapter struct {
	otelServer  *otelserver.OtelServer
	otelParser  *OtelParser
	activityLog *activitylog.Logger

	// internalCh buffers events from the OTEL parser callbacks.
	// Start() forwards these to the external events channel.
	internalCh chan monitor.AgentEvent
}

// New creates a CodexAdapter.
func New(log *activitylog.Logger) *CodexAdapter {
	if log == nil {
		log = activitylog.Nop()
	}
	ch := make(chan monitor.AgentEvent, 256)
	return &CodexAdapter{
		activityLog: log,
		internalCh:  ch,
		otelParser:  NewOtelParser(ch),
	}
}

// Name returns the adapter identifier.
func (a *CodexAdapter) Name() string {
	return "codex"
}

// PrepareForLaunch creates the OTEL server and returns the -c flag
// that configures Codex's trace exporter to send to h2's collector.
func (a *CodexAdapter) PrepareForLaunch(agentName string) (adapter.LaunchConfig, error) {
	s, err := otelserver.New(otelserver.Callbacks{
		OnTraces: a.otelParser.OnTraces,
	})
	if err != nil {
		return adapter.LaunchConfig{}, fmt.Errorf("create otel server: %w", err)
	}
	a.otelServer = s

	endpoint := fmt.Sprintf("http://127.0.0.1:%d", s.Port)
	return adapter.LaunchConfig{
		PrependArgs: []string{
			"-c", fmt.Sprintf(`otel.trace_exporter={type="otlp-http",endpoint="%s"}`, endpoint),
		},
	}, nil
}

// Start forwards internal events to the external channel and blocks
// until ctx is cancelled.
func (a *CodexAdapter) Start(ctx context.Context, events chan<- monitor.AgentEvent) error {
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

// HandleHookEvent returns false — Codex doesn't use h2 hooks.
func (a *CodexAdapter) HandleHookEvent(eventName string, payload json.RawMessage) bool {
	return false
}

// Stop cleans up the OTEL server.
func (a *CodexAdapter) Stop() {
	if a.otelServer != nil {
		a.otelServer.Stop()
	}
}

// OtelPort returns the OTEL server port (available after PrepareForLaunch).
func (a *CodexAdapter) OtelPort() int {
	if a.otelServer != nil {
		return a.otelServer.Port
	}
	return 0
}
