// Package claude implements the Harness for Claude Code.
// It merges the former ClaudeCodeType (config/launch) and ClaudeCodeAdapter
// (telemetry/hooks/lifecycle) into a single ClaudeCodeHarness.
package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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
	harness.Register(harness.HarnessSpec{
		Names: []string{"claude_code"},
		Factory: func(rc *config.RuntimeConfig, log *activitylog.Logger) harness.Harness {
			return New(rc, log)
		},
		DefaultCommand: "claude",
	})
}

// ClaudeCodeHarness implements harness.Harness for Claude Code.
type ClaudeCodeHarness struct {
	rc          *config.RuntimeConfig
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
func New(rc *config.RuntimeConfig, log *activitylog.Logger) *ClaudeCodeHarness {
	if log == nil {
		log = activitylog.Nop()
	}
	ch := make(chan monitor.AgentEvent, 256)
	return &ClaudeCodeHarness{
		rc:           rc,
		activityLog:  log,
		internalCh:   ch,
		eventHandler: NewEventHandler(ch, log),
	}
}

// --- Identity ---

func (h *ClaudeCodeHarness) Name() string           { return "claude_code" }
func (h *ClaudeCodeHarness) Command() string        { return "claude" }
func (h *ClaudeCodeHarness) DisplayCommand() string { return "claude" }

// --- Resume ---

func (h *ClaudeCodeHarness) SupportsResume() bool { return true }

// --- Config (called before launch) ---

// BuildCommandArgs maps RuntimeConfig to Claude Code CLI flags, combined with
// prependArgs and extraArgs into the complete child process argument list.
// When ResumeSessionID is set (via the session's resume flow), only --resume
// is emitted — Claude Code restores all settings from the session.
func (h *ClaudeCodeHarness) BuildCommandArgs(prependArgs, extraArgs []string) []string {
	var roleArgs []string
	rc := h.rc
	if rc.ResumeSessionID != "" {
		// Resume mode: only pass --resume, no other flags.
		roleArgs = append(roleArgs, "--resume", rc.ResumeSessionID)
		return harness.CombineArgs(prependArgs, extraArgs, roleArgs)
	}
	if rc.SessionID != "" {
		roleArgs = append(roleArgs, "--session-id", rc.SessionID)
	}
	if rc.SystemPrompt != "" {
		roleArgs = append(roleArgs, "--system-prompt", rc.SystemPrompt)
	}
	if rc.Instructions != "" {
		roleArgs = append(roleArgs, "--append-system-prompt", rc.Instructions)
	}
	if rc.Model != "" {
		roleArgs = append(roleArgs, "--model", rc.Model)
	}
	if rc.ClaudePermissionMode != "" {
		roleArgs = append(roleArgs, "--permission-mode", rc.ClaudePermissionMode)
	}
	for _, dir := range rc.AdditionalDirs {
		roleArgs = append(roleArgs, "--add-dir", dir)
	}
	return harness.CombineArgs(prependArgs, extraArgs, roleArgs)
}

// BuildCommandEnvVars returns env vars for Claude Code (CLAUDE_CONFIG_DIR).
func (h *ClaudeCodeHarness) BuildCommandEnvVars(h2Dir string) map[string]string {
	configDir := h.rc.HarnessConfigDir()
	if configDir != "" {
		return map[string]string{
			"CLAUDE_CONFIG_DIR": configDir,
		}
	}
	return nil
}

// EnsureConfigDir creates the Claude config directory and writes default settings.
func (h *ClaudeCodeHarness) EnsureConfigDir(h2Dir string) error {
	configDir := h.rc.HarnessConfigDir()
	if configDir == "" {
		return nil
	}
	return config.EnsureClaudeConfigDir(configDir)
}

// --- Launch (called once, before child process starts) ---

// PrepareForLaunch creates the OTEL server and returns the env vars and
// CLI args needed to launch Claude Code with telemetry enabled.
// When dryRun is true, returns placeholder env vars without starting a server.
func (h *ClaudeCodeHarness) PrepareForLaunch(dryRun bool) (harness.LaunchConfig, error) {
	sessionID := h.rc.SessionID
	if sessionID != "" {
		h.sessionID = sessionID
	} else {
		h.sessionID = uuid.New().String()
	}
	agentName := h.rc.AgentName
	h.sessionLogPath = resolveSessionLogPath(agentName, h.sessionID)
	// Set native log path suffix on the RuntimeConfig so it's persisted
	// in session metadata and available to external callers.
	h.rc.NativeLogPathSuffix = NativeLogPathSuffix(h.rc.CWD, h.sessionID)
	h.eventHandler.SetExpectedSessionID(h.sessionID)
	h.eventHandler.ConfigureDebug(resolveDebugPath(agentName, h.sessionID))

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

// HandleInterrupt emits an idle transition for a local Ctrl+C.
func (h *ClaudeCodeHarness) HandleInterrupt() bool {
	if h.eventHandler != nil {
		return h.eventHandler.HandleInterrupt()
	}
	return false
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

func resolveSessionDir(agentName, sessionID string) string {
	if agentName != "" {
		return config.SessionDir(agentName)
	}
	return config.FindSessionDirByID(sessionID)
}

func resolveSessionLogPath(agentName, sessionID string) string {
	sessionDir := resolveSessionDir(agentName, sessionID)
	if sessionDir == "" {
		return ""
	}
	return filepath.Join(sessionDir, "session.jsonl")
}

// NativeLogPathSuffix returns the path suffix for Claude Code's native session
// log file, relative to the harness config directory. Claude Code stores logs at:
//
//	<configDir>/projects/<sanitized-cwd>/<sessionID>.jsonl
//
// The CWD is sanitized by replacing path separators with dashes.
// Returns "" if any parameter is empty.
func NativeLogPathSuffix(cwd, sessionID string) string {
	if cwd == "" || sessionID == "" {
		return ""
	}
	sanitized := strings.ReplaceAll(cwd, string(filepath.Separator), "-")
	return filepath.Join("projects", sanitized, sessionID+".jsonl")
}

func resolveDebugPath(agentName, sessionID string) string {
	sessionDir := resolveSessionDir(agentName, sessionID)
	if sessionDir != "" {
		return filepath.Join(sessionDir, "claude-otel-debug.log")
	}
	name := sessionID
	if name == "" {
		name = "unknown"
	}
	return filepath.Join(config.ConfigDir(), "logs", fmt.Sprintf("claude-otel-%s.log", name))
}
