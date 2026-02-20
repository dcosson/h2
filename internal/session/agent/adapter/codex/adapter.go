// Package codex implements the AgentAdapter for OpenAI Codex CLI.
// It translates Codex's OTEL trace events into normalized AgentEvents.
package codex

import (
	"context"
	"encoding/json"

	"h2/internal/activitylog"
	"h2/internal/session/agent/adapter"
	"h2/internal/session/agent/monitor"
)

// CodexAdapter translates Codex CLI telemetry into AgentEvents.
// Codex emits OTEL traces (not logs/metrics like Claude Code).
// The full OTEL trace parser is implemented in a separate bead.
type CodexAdapter struct {
	activityLog *activitylog.Logger
	internalCh  chan monitor.AgentEvent
}

// New creates a CodexAdapter.
func New(log *activitylog.Logger) *CodexAdapter {
	if log == nil {
		log = activitylog.Nop()
	}
	return &CodexAdapter{
		activityLog: log,
		internalCh:  make(chan monitor.AgentEvent, 256),
	}
}

// Name returns the adapter identifier.
func (a *CodexAdapter) Name() string {
	return "codex"
}

// PrepareForLaunch returns the OTEL trace exporter config for Codex.
// The full implementation (creating OtelServer, returning -c flag) is
// in the OTEL trace parser bead.
func (a *CodexAdapter) PrepareForLaunch(agentName string) (adapter.LaunchConfig, error) {
	return adapter.LaunchConfig{}, nil
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

// Stop cleans up resources.
func (a *CodexAdapter) Stop() {
	// No resources to clean up in the stub.
}
