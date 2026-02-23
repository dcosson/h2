// Package codex implements the Harness for OpenAI Codex CLI.
// It merges the former CodexType (config/launch) and CodexAdapter
// (telemetry/lifecycle) into a single CodexHarness.
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"h2/internal/activitylog"
	"h2/internal/config"
	"h2/internal/session/agent/harness"
	"h2/internal/session/agent/monitor"
	"h2/internal/session/agent/shared/otelserver"
)

func init() {
	harness.Register(harness.HarnessSpec{
		Names: []string{"codex"},
		Factory: func(cfg harness.HarnessConfig, log *activitylog.Logger) harness.Harness {
			return New(cfg, log)
		},
		DefaultCommand: "codex",
		DefaultModel:   "gpt-5.3-codex",
	})
}

// CodexHarness implements harness.Harness for OpenAI Codex CLI.
type CodexHarness struct {
	configDir   string
	model       string
	activityLog *activitylog.Logger

	otelServer   *otelserver.OtelServer
	eventHandler *EventHandler

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
		configDir:    cfg.ConfigDir,
		model:        cfg.Model,
		activityLog:  log,
		internalCh:   ch,
		eventHandler: NewEventHandler(ch),
	}
}

// --- Identity ---

func (h *CodexHarness) Name() string           { return "codex" }
func (h *CodexHarness) Command() string        { return "codex" }
func (h *CodexHarness) DisplayCommand() string { return "codex" }

// --- Config (called before launch) ---

// BuildCommandArgs maps role config to Codex CLI flags, combined with
// PrependArgs and ExtraArgs into the complete child process argument list.
// SystemPrompt, AllowedTools, and DisallowedTools have no Codex equivalent
// and are silently ignored.
func (h *CodexHarness) BuildCommandArgs(cfg harness.CommandArgsConfig) []string {
	var roleArgs []string
	if cfg.Instructions != "" {
		// JSON-encode the value so newlines become \n and quotes are escaped.
		// Codex -c parses values as JSON when possible.
		encoded, _ := json.Marshal(cfg.Instructions)
		roleArgs = append(roleArgs, "-c", "instructions="+string(encoded))
	}
	if cfg.Model != "" {
		roleArgs = append(roleArgs, "--model", cfg.Model)
	}
	if cfg.ApprovalPolicy != "" || cfg.CodexSandboxMode != "" {
		// Granular settings: use explicit Codex flags.
		switch cfg.ApprovalPolicy {
		case "plan":
			roleArgs = append(roleArgs, "--ask-for-approval", "untrusted")
			// Also default to read-only sandbox for plan mode unless overridden.
			if cfg.CodexSandboxMode == "" {
				roleArgs = append(roleArgs, "--sandbox", "read-only")
			}
		case "confirm":
			roleArgs = append(roleArgs, "--ask-for-approval", "untrusted")
		case "auto-edit":
			roleArgs = append(roleArgs, "--ask-for-approval", "on-request")
		case "auto":
			roleArgs = append(roleArgs, "--ask-for-approval", "never")
		}
		if cfg.CodexSandboxMode != "" {
			roleArgs = append(roleArgs, "--sandbox", cfg.CodexSandboxMode)
		}
	} else {
		// Fall back to PermissionMode → combo flag mapping.
		switch cfg.PermissionMode {
		case "full-auto":
			roleArgs = append(roleArgs, "--full-auto")
		case "ask":
			// --ask is the default for Codex, no flag needed.
		default:
			// Default to full-auto for h2-managed agents.
			roleArgs = append(roleArgs, "--full-auto")
		}
	}
	return harness.CombineArgs(cfg, roleArgs)
}

// BuildCommandEnvVars returns nil — Codex doesn't need special env vars.
func (h *CodexHarness) BuildCommandEnvVars(h2Dir string) map[string]string {
	if h.configDir == "" {
		return nil
	}
	return map[string]string{
		"CODEX_HOME": h.configDir,
	}
}

// EnsureConfigDir creates the configured CODEX_HOME directory if needed.
func (h *CodexHarness) EnsureConfigDir(h2Dir string) error {
	if h.configDir == "" {
		return nil
	}
	if err := os.MkdirAll(h.configDir, 0o755); err != nil {
		return fmt.Errorf("create codex config dir: %w", err)
	}
	return nil
}

// --- Launch (called once, before child process starts) ---

// PrepareForLaunch creates the OTEL server and returns the -c flag
// that configures Codex's log exporter to send to h2's collector.
// When dryRun is true, returns placeholder args without starting a server.
func (h *CodexHarness) PrepareForLaunch(agentName, sessionID string, dryRun bool) (harness.LaunchConfig, error) {
	if dryRun {
		return harness.LaunchConfig{
			PrependArgs: []string{
				"-c", `otel.exporter={otlp-http={endpoint="http://127.0.0.1:<PORT>/v1/logs",protocol="json"}}`,
			},
		}, nil
	}

	debugPath := resolveDebugPath(agentName, sessionID)
	h.eventHandler.ConfigureDebug(debugPath)

	s, err := otelserver.New(otelserver.Callbacks{
		OnLogs:    h.eventHandler.OnLogs,
		OnMetrics: h.eventHandler.OnMetricsRaw,
		OnTraces:  h.eventHandler.OnTraces,
	})
	if err != nil {
		return harness.LaunchConfig{}, fmt.Errorf("create otel server: %w", err)
	}
	h.otelServer = s
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/v1/logs", s.Port)
	return harness.LaunchConfig{
		PrependArgs: []string{
			"-c", fmt.Sprintf(`otel.exporter={otlp-http={endpoint="%s",protocol="json"}}`, endpoint),
		},
	}, nil
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

// HandleInterrupt handles local interrupts by emitting an idle state change and
// suppressing stale post-interrupt active transitions.
func (h *CodexHarness) HandleInterrupt() bool {
	if h.eventHandler != nil {
		h.eventHandler.OnInterrupt()
		return true
	}
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

func resolveSessionDir(agentName, sessionID string) string {
	if agentName != "" {
		return config.SessionDir(agentName)
	}
	return config.FindSessionDirByID(sessionID)
}

func resolveDebugPath(agentName, sessionID string) string {
	sessionDir := resolveSessionDir(agentName, sessionID)
	if sessionDir != "" {
		return filepath.Join(sessionDir, "codex-otel-debug.log")
	}
	// Last-resort path so parser startup logging still lands somewhere.
	name := sessionID
	if name == "" {
		name = "unknown"
	}
	return filepath.Join(config.ConfigDir(), "logs", fmt.Sprintf("codex-otel-%s.log", name))
}
