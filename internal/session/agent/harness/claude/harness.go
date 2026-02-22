// Package claude implements the Harness for Claude Code.
// It merges the former ClaudeCodeType (config/launch) and ClaudeCodeAdapter
// (telemetry/hooks/lifecycle) into a single ClaudeCodeHarness.
package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"h2/internal/activitylog"
	"h2/internal/config"
	"h2/internal/session/agent/harness"
	"h2/internal/session/agent/monitor"
	"h2/internal/session/agent/shared/otelserver"
	"h2/internal/session/agent/shared/sessionlogcollector"
)

func init() {
	harness.Register(func(cfg harness.HarnessConfig, log *activitylog.Logger) harness.Harness {
		return New(cfg, log)
	}, "claude_code", "claude")
}

// ClaudeCodeHarness implements harness.Harness for Claude Code.
type ClaudeCodeHarness struct {
	configDir   string
	model       string
	activityLog *activitylog.Logger

	otelServer     *otelserver.OtelServer
	eventHandler   *EventHandler
	sessionLogPath string
	sessionID      string

	// internalCh buffers events from callbacks and hook handlers.
	// Start() forwards these to the external events channel.
	internalCh chan monitor.AgentEvent
}

// New creates a ClaudeCodeHarness.
func New(cfg harness.HarnessConfig, log *activitylog.Logger) *ClaudeCodeHarness {
	if log == nil {
		log = activitylog.Nop()
	}
	ch := make(chan monitor.AgentEvent, 256)
	return &ClaudeCodeHarness{
		configDir:    cfg.ConfigDir,
		model:        cfg.Model,
		activityLog:  log,
		internalCh:   ch,
		eventHandler: NewEventHandler(ch, log),
	}
}

// --- Identity ---

func (h *ClaudeCodeHarness) Name() string           { return "claude_code" }
func (h *ClaudeCodeHarness) Command() string        { return "claude" }
func (h *ClaudeCodeHarness) DisplayCommand() string { return "claude" }

// --- Config (called before launch) ---

// BuildCommandArgs maps role config to Claude Code CLI flags, combined with
// PrependArgs and ExtraArgs into the complete child process argument list.
func (h *ClaudeCodeHarness) BuildCommandArgs(cfg harness.CommandArgsConfig) []string {
	var roleArgs []string
	if cfg.SessionID != "" {
		roleArgs = append(roleArgs, "--session-id", cfg.SessionID)
	}
	if cfg.SystemPrompt != "" {
		roleArgs = append(roleArgs, "--system-prompt", cfg.SystemPrompt)
	}
	if cfg.Instructions != "" {
		roleArgs = append(roleArgs, "--append-system-prompt", cfg.Instructions)
	}
	if cfg.Model != "" {
		roleArgs = append(roleArgs, "--model", cfg.Model)
	}
	if cfg.PermissionMode != "" {
		roleArgs = append(roleArgs, "--permission-mode", cfg.PermissionMode)
	}
	if len(cfg.AllowedTools) > 0 {
		roleArgs = append(roleArgs, "--allowedTools", strings.Join(cfg.AllowedTools, ","))
	}
	if len(cfg.DisallowedTools) > 0 {
		roleArgs = append(roleArgs, "--disallowedTools", strings.Join(cfg.DisallowedTools, ","))
	}
	return harness.CombineArgs(cfg, roleArgs)
}

// BuildCommandEnvVars returns env vars for Claude Code (CLAUDE_CONFIG_DIR).
// Uses the stored configDir from HarnessConfig instead of loading role.
func (h *ClaudeCodeHarness) BuildCommandEnvVars(h2Dir string) map[string]string {
	if h.configDir != "" {
		return map[string]string{
			"CLAUDE_CONFIG_DIR": h.configDir,
		}
	}
	return nil
}

// EnsureConfigDir creates the Claude config directory and writes default settings.
func (h *ClaudeCodeHarness) EnsureConfigDir(h2Dir string) error {
	if h.configDir == "" {
		return nil
	}
	return config.EnsureClaudeConfigDir(h.configDir)
}

// --- Launch (called once, before child process starts) ---

// PrepareForLaunch creates the OTEL server and returns the env vars and
// CLI args needed to launch Claude Code with telemetry enabled.
// When dryRun is true, returns placeholder env vars without starting a server.
func (h *ClaudeCodeHarness) PrepareForLaunch(agentName, sessionID string, dryRun bool) (harness.LaunchConfig, error) {
	if sessionID != "" {
		h.sessionID = sessionID
	} else {
		h.sessionID = uuid.New().String()
	}

	endpoint := "http://127.0.0.1:<PORT>"
	if !dryRun {
		// Create OTEL server with callbacks that parse and emit events.
		s, err := otelserver.New(otelserver.Callbacks{
			OnLogs:    h.eventHandler.OnLogs,
			OnMetrics: h.eventHandler.OnMetrics,
		})
		if err != nil {
			return harness.LaunchConfig{}, fmt.Errorf("create otel server: %w", err)
		}
		h.otelServer = s
		endpoint = fmt.Sprintf("http://127.0.0.1:%d", s.Port)
	}

	return harness.LaunchConfig{
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

// --- Runtime (called after child process starts) ---

// Start forwards internal events to the external channel and blocks
// until ctx is cancelled.
func (h *ClaudeCodeHarness) Start(ctx context.Context, events chan<- monitor.AgentEvent) error {
	// Start session log tailer if configured.
	if h.sessionLogPath != "" {
		go sessionlogcollector.New(h.sessionLogPath, h.eventHandler.OnSessionLogLine).Run(ctx)
	}

	// Forward internal events to the external channel.
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

// HandleHookEvent delegates hook events to the HookHandler.
func (h *ClaudeCodeHarness) HandleHookEvent(eventName string, payload json.RawMessage) bool {
	return h.eventHandler.ProcessHookEvent(eventName, payload)
}

// HandleOutput is a no-op for Claude Code (state is tracked via OTEL/hooks).
func (h *ClaudeCodeHarness) HandleOutput() {}

// Stop cleans up the OTEL server and other resources.
func (h *ClaudeCodeHarness) Stop() {
	if h.otelServer != nil {
		h.otelServer.Stop()
	}
}

// --- Extra accessors (used by Agent) ---

// SessionID returns the generated session ID (available after PrepareForLaunch).
func (h *ClaudeCodeHarness) SessionID() string {
	return h.sessionID
}

// OtelPort returns the OTEL server port (available after PrepareForLaunch).
func (h *ClaudeCodeHarness) OtelPort() int {
	if h.otelServer != nil {
		return h.otelServer.Port
	}
	return 0
}

// SetSessionLogPath configures the path to Claude Code's session JSONL
// for the session log tailer. Must be called before Start().
func (h *ClaudeCodeHarness) SetSessionLogPath(path string) {
	h.sessionLogPath = path
}
