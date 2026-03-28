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
		Factory: func(rc *config.RuntimeConfig, log *activitylog.Logger) harness.Harness {
			return New(rc, log)
		},
		DefaultCommand: "codex",
	})
}

// CodexHarness implements harness.Harness for OpenAI Codex CLI.
type CodexHarness struct {
	rc          *config.RuntimeConfig
	activityLog *activitylog.Logger

	otelServer   *otelserver.OtelServer
	eventHandler *EventHandler

	// internalCh buffers events from the OTEL parser callbacks.
	// Start() forwards these to the external events channel.
	internalCh chan monitor.AgentEvent
}

// New creates a CodexHarness.
func New(rc *config.RuntimeConfig, log *activitylog.Logger) *CodexHarness {
	if log == nil {
		log = activitylog.Nop()
	}
	ch := make(chan monitor.AgentEvent, 256)
	return &CodexHarness{
		rc:           rc,
		activityLog:  log,
		internalCh:   ch,
		eventHandler: NewEventHandler(ch),
	}
}

// --- Identity ---

func (h *CodexHarness) Name() string           { return "codex" }
func (h *CodexHarness) Command() string        { return "codex" }
func (h *CodexHarness) DisplayCommand() string { return "codex" }

// --- Resume ---

func (h *CodexHarness) SupportsResume() bool { return true }

// --- Config (called before launch) ---

// BuildCommandArgs maps RuntimeConfig to Codex CLI flags, combined with
// prependArgs and extraArgs into the complete child process argument list.
func (h *CodexHarness) BuildCommandArgs(prependArgs, extraArgs []string) []string {
	var roleArgs []string
	rc := h.rc
	if rc.ResumeSessionID != "" {
		roleArgs = append(roleArgs, "resume", rc.ResumeSessionID)
	}
	// Configuration flags apply to both fresh and resumed sessions.
	// Codex does not persist config like sandbox mode or approval settings
	// in its session state, so they must always be passed explicitly.
	if rc.Instructions != "" && rc.ResumeSessionID == "" {
		// Instructions only apply to fresh sessions — the resumed session
		// already has its conversation history.
		encoded, _ := json.Marshal(rc.Instructions)
		roleArgs = append(roleArgs, "-c", "instructions="+string(encoded))
	}
	if rc.Model != "" {
		roleArgs = append(roleArgs, "--model", rc.Model)
	}
	if rc.CodexAskForApproval != "" {
		roleArgs = append(roleArgs, "--ask-for-approval", rc.CodexAskForApproval)
	}
	if rc.CodexSandboxMode != "" {
		roleArgs = append(roleArgs, "--sandbox", rc.CodexSandboxMode)
	}
	for _, dir := range rc.AdditionalDirs {
		roleArgs = append(roleArgs, "--add-dir", dir)
	}
	return harness.CombineArgs(prependArgs, extraArgs, roleArgs)
}

// BuildCommandEnvVars returns CODEX_HOME env var if configured.
func (h *CodexHarness) BuildCommandEnvVars(h2Dir string) map[string]string {
	configDir := h.rc.HarnessConfigDir()
	if configDir == "" {
		return nil
	}
	return map[string]string{
		"CODEX_HOME": configDir,
	}
}

// EnsureConfigDir creates the configured CODEX_HOME directory if needed.
func (h *CodexHarness) EnsureConfigDir(h2Dir string) error {
	configDir := h.rc.HarnessConfigDir()
	if configDir == "" {
		return nil
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("create codex config dir: %w", err)
	}
	return nil
}

// --- Launch (called once, before child process starts) ---

// PrepareForLaunch creates the OTEL server and returns the -c flag
// that configures Codex's log exporter to send to h2's collector.
// When dryRun is true, returns placeholder args without starting a server.
func (h *CodexHarness) PrepareForLaunch(dryRun bool) (harness.LaunchConfig, error) {
	if dryRun {
		return harness.LaunchConfig{
			PrependArgs: []string{
				"-c", `otel.exporter={otlp-http={endpoint="http://127.0.0.1:<PORT>/v1/logs",protocol="json"}}`,
			},
		}, nil
	}

	agentName := h.rc.AgentName
	sessionID := h.rc.SessionID
	debugPath := resolveDebugPath(agentName, sessionID)
	h.eventHandler.ConfigureDebug(debugPath)

	// Register callback to discover native session log path when the
	// conversation ID arrives. Codex log files are at:
	//   $CODEX_HOME/sessions/YYYY/MM/DD/rollout-<timestamp>-<convID>.jsonl
	// We glob for the file by conversation ID suffix.
	h.eventHandler.SetOnConversationStarted(func(convID string) {
		configDir := h.rc.HarnessConfigDir()
		if configDir == "" || convID == "" {
			return
		}
		pattern := filepath.Join(configDir, "sessions", "*", "*", "*", "*-"+convID+".jsonl")
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			return
		}
		// Use the first match. Compute suffix relative to configDir.
		rel, err := filepath.Rel(configDir, matches[0])
		if err != nil {
			return
		}
		h.rc.NativeLogPathSuffix = rel
	})

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
