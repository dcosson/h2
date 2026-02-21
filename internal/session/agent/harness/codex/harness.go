// Package codex implements the Harness for OpenAI Codex CLI.
// It merges the former CodexType (config/launch) and CodexAdapter
// (telemetry/lifecycle) into a single CodexHarness.
package codex

import (
	"context"
	"encoding/json"

	"h2/internal/activitylog"
	"h2/internal/session/agent/harness"
	"h2/internal/session/agent/monitor"
	"h2/internal/session/agent/shared/otelserver"
)

func init() {
	harness.Register(func(cfg harness.HarnessConfig, log *activitylog.Logger) harness.Harness {
		return New(cfg, log)
	}, "codex")
}

// CodexHarness implements harness.Harness for OpenAI Codex CLI.
type CodexHarness struct {
	configDir   string
	model       string
	activityLog *activitylog.Logger

	otelServer *otelserver.OtelServer
	otelParser *OtelParser

	// internalCh buffers events from the OTEL parser callbacks.
	// Start() forwards these to the external events channel.
	internalCh chan monitor.AgentEvent
}

// New creates a CodexHarness.
func New(cfg harness.HarnessConfig, log *activitylog.Logger) *CodexHarness {
	if log == nil {
		log = activitylog.Nop()
	}
	ch := make(chan monitor.AgentEvent, 256)
	return &CodexHarness{
		configDir:   cfg.ConfigDir,
		model:       cfg.Model,
		activityLog: log,
		internalCh:  ch,
		otelParser:  NewOtelParser(ch),
	}
}

// --- Identity ---

func (h *CodexHarness) Name() string           { return "codex" }
func (h *CodexHarness) Command() string         { return "codex" }
func (h *CodexHarness) DisplayCommand() string   { return "codex" }

// --- Config (called before launch) ---

// BuildCommandArgs maps role config to Codex CLI flags.
// SystemPrompt, AllowedTools, and DisallowedTools have no Codex equivalent
// and are silently ignored.
func (h *CodexHarness) BuildCommandArgs(cfg harness.CommandArgsConfig) []string {
	var args []string
	if cfg.Instructions != "" {
		// JSON-encode the value so newlines become \n and quotes are escaped.
		// Codex -c parses values as JSON when possible.
		encoded, _ := json.Marshal(cfg.Instructions)
		args = append(args, "-c", "instructions="+string(encoded))
	}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	switch cfg.PermissionMode {
	case "full-auto":
		args = append(args, "--full-auto")
	case "suggest":
		args = append(args, "--suggest")
	case "ask":
		// --ask is the default for Codex, no flag needed.
	default:
		// Default to full-auto for h2-managed agents.
		args = append(args, "--full-auto")
	}
	return args
}

// BuildCommandEnvVars returns nil — Codex doesn't need special env vars.
func (h *CodexHarness) BuildCommandEnvVars(h2Dir string) map[string]string {
	return nil
}

// EnsureConfigDir is a no-op for Codex.
func (h *CodexHarness) EnsureConfigDir(h2Dir string) error { return nil }

// --- Launch (called once, before child process starts) ---

// PrepareForLaunch creates the OTEL server and returns the -c flag
// that configures Codex's trace exporter to send to h2's collector.
// When dryRun is true, returns placeholder args without starting a server.
func (h *CodexHarness) PrepareForLaunch(agentName, sessionID string, dryRun bool) (harness.LaunchConfig, error) {
	if dryRun {
		return harness.LaunchConfig{
			PrependArgs: []string{
				"-c", `otel.trace_exporter={otlp-http={endpoint="http://127.0.0.1:<PORT>",protocol="json"}}`,
			},
		}, nil
	}
	cfg, s, err := BuildLaunchConfig(otelserver.Callbacks{
		OnTraces: h.otelParser.OnTraces,
	})
	if err != nil {
		return harness.LaunchConfig{}, err
	}
	h.otelServer = s
	return cfg, nil
}

// --- Runtime (called after child process starts) ---

// Start forwards internal events to the external channel and blocks
// until ctx is cancelled.
func (h *CodexHarness) Start(ctx context.Context, events chan<- monitor.AgentEvent) error {
	for {
		select {
		case ev := <-h.internalCh:
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
func (h *CodexHarness) HandleHookEvent(eventName string, payload json.RawMessage) bool {
	return false
}

// HandleOutput is a no-op for Codex (state is tracked via OTEL traces).
func (h *CodexHarness) HandleOutput() {}

// Stop cleans up the OTEL server.
func (h *CodexHarness) Stop() {
	if h.otelServer != nil {
		h.otelServer.Stop()
	}
}

// --- Extra accessors ---

// OtelPort returns the OTEL server port (available after PrepareForLaunch).
func (h *CodexHarness) OtelPort() int {
	if h.otelServer != nil {
		return h.otelServer.Port
	}
	return 0
}

