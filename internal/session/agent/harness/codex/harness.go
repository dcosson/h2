// Package codex implements the Harness for OpenAI Codex CLI.
// It merges the former CodexType (config/launch) and CodexAdapter
// (telemetry/lifecycle) into a single CodexHarness.
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	stdlog "log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"h2/internal/activitylog"
	"h2/internal/config"
	"h2/internal/session/agent/harness"
	"h2/internal/session/agent/monitor"
	"h2/internal/session/agent/shared/otelserver"
	"h2/internal/session/agent/shared/sessionlogcollector"
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

	// sessionLogPathCh delivers the native rollout log path to the tailer once
	// it is discovered (async, when conversation.id arrives). Buffered so the
	// discovery callback never blocks and the path survives if it fires before
	// Start()'s tailer goroutine is waiting.
	sessionLogPathCh chan string

	// topLevelConversationID tracks the conversation h2 treats as this agent's
	// session identity. Codex sub-agents share the parent's OTEL exporter, so
	// their conversation-start events reach us too and must never take over the
	// identity used by resume and profile rotation. It is not pinned for the
	// life of the daemon: `codex resume <id>` starts a *new* conversation forked
	// from the old one, so the top-level ID legitimately changes on every
	// relaunch and must follow the live conversation.
	conversationMu         sync.Mutex
	topLevelConversationID string
	topLevelRolloutPath    string
	sessionLogTailed       bool
}

// New creates a CodexHarness.
func New(rc *config.RuntimeConfig, log *activitylog.Logger) *CodexHarness {
	if log == nil {
		log = activitylog.Nop()
	}
	ch := make(chan monitor.AgentEvent, 256)
	return &CodexHarness{
		rc:               rc,
		activityLog:      log,
		internalCh:       ch,
		eventHandler:     NewEventHandler(ch),
		sessionLogPathCh: make(chan string, 1),
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
	// Resolve the resume target before launch: a stored ID that turns out to be
	// a sub-agent conversation would drop the user into a dead child session.
	resumeID := ""
	if rc.ResumeSessionID != "" {
		resumeID = resolveResumeTarget(rc.HarnessConfigDir(), rc.ResumeSessionID)
	}
	if resumeID != "" {
		roleArgs = append(roleArgs, "resume", resumeID)
	}
	// Configuration flags apply to both fresh and resumed sessions.
	// Codex does not persist config like sandbox mode or approval settings
	// in its session state, so they must always be passed explicitly.
	if rc.Instructions != "" && resumeID == "" {
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
	h.eventHandler.SetOnConversationStarted(h.acceptConversation)

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

// acceptConversation decides whether a codex.conversation_starts event belongs
// to this agent's top-level conversation. Returning true adopts convID as the
// session identity (persisted as harness_session_id and used by resume and
// rotate); returning false ignores the event.
//
// The decision is made from the rollout file's session_meta record, never from
// the ID itself: `codex resume <id>` forks a brand new conversation ID, so an
// ID that differs from the one we asked to resume is the normal case, not a
// sign of a sub-agent.
func (h *CodexHarness) acceptConversation(convID string) bool {
	if convID == "" {
		return false
	}

	h.conversationMu.Lock()
	defer h.conversationMu.Unlock()

	// Duplicate event for the conversation we already track. Fall through when
	// its rollout was never found so path discovery gets another attempt.
	if convID == h.topLevelConversationID && h.topLevelRolloutPath != "" {
		return true
	}

	configDir := h.rc.HarnessConfigDir()
	if configDir == "" {
		h.topLevelConversationID = convID
		return true
	}

	path, meta, found := findCodexRollout(configDir, convID)
	if found && meta.isSubagent() {
		return false
	}
	if !found && h.topLevelConversationID != "" && convID != h.topLevelConversationID {
		// No rollout on disk to vouch for this conversation and we already have
		// a top-level one. Codex writes the rollout as the conversation opens,
		// so an unidentifiable late arrival is far more likely a sub-agent than
		// a new top-level session — refuse to hand it the session identity.
		stdlog.Printf("codex: ignoring conversation %s (no rollout metadata; current top-level %s)", convID, h.topLevelConversationID)
		return false
	}

	h.topLevelConversationID = convID
	if path == "" {
		return true
	}

	rel, err := filepath.Rel(configDir, path)
	if err != nil {
		return true
	}
	h.topLevelRolloutPath = path
	h.rc.NativeLogPathSuffix = rel

	// Hand the rollout path to the tailer, replacing any path queued but not
	// yet picked up. On relaunch the tailer switches to the new rollout.
	select {
	case <-h.sessionLogPathCh:
	default:
	}
	h.sessionLogPathCh <- path
	return true
}

// codexRolloutMeta is the session_meta record that opens every Codex rollout.
type codexRolloutMeta struct {
	ID             string          `json:"id"`
	SessionID      string          `json:"session_id"`
	ParentThreadID string          `json:"parent_thread_id"`
	ForkedFromID   string          `json:"forked_from_id"`
	ThreadSource   string          `json:"thread_source"`
	Source         json.RawMessage `json:"source"`
}

// isSubagent reports whether the rollout belongs to a Codex sub-agent rather
// than the top-level TUI conversation. Sub-agents are marked three ways
// (thread_source, a source.subagent object, and parent_thread_id); any one of
// them is enough, so a Codex version that drops one still classifies correctly.
// A rollout with none of them — including older Codex versions that predate
// thread_source — counts as top-level.
func (m codexRolloutMeta) isSubagent() bool {
	if m.ThreadSource == "subagent" || m.ParentThreadID != "" {
		return true
	}
	var src struct {
		Subagent json.RawMessage `json:"subagent"`
	}
	if err := json.Unmarshal(m.Source, &src); err == nil && len(src.Subagent) > 0 {
		return true
	}
	return false
}

// parentID returns the conversation this rollout was spawned or forked from.
func (m codexRolloutMeta) parentID() string {
	if m.ParentThreadID != "" {
		return m.ParentThreadID
	}
	return m.ForkedFromID
}

// rolloutMetaRetries bounds how long findCodexRollout waits for a rollout file
// to appear. Codex creates it as the conversation opens, so this only covers
// the sub-second race against the OTEL event.
var (
	rolloutMetaRetries = 10
	rolloutMetaDelay   = 50 * time.Millisecond
)

// findCodexRollout locates the rollout file for a conversation ID under
// configDir and reads its session_meta record.
func findCodexRollout(configDir, convID string) (string, codexRolloutMeta, bool) {
	if configDir == "" || convID == "" {
		return "", codexRolloutMeta{}, false
	}
	pattern := filepath.Join(configDir, "sessions", "*", "*", "*", "*-"+convID+".jsonl")
	for attempt := 0; ; attempt++ {
		matches, err := filepath.Glob(pattern)
		if err == nil && len(matches) > 0 {
			meta, ok := readCodexRolloutMeta(matches[0])
			return matches[0], meta, ok
		}
		if attempt >= rolloutMetaRetries {
			return "", codexRolloutMeta{}, false
		}
		time.Sleep(rolloutMetaDelay)
	}
}

// readCodexRolloutMeta decodes the session_meta record at the head of a rollout.
func readCodexRolloutMeta(path string) (codexRolloutMeta, bool) {
	f, err := os.Open(path)
	if err != nil {
		return codexRolloutMeta{}, false
	}
	defer f.Close()

	var record struct {
		Type    string           `json:"type"`
		Payload codexRolloutMeta `json:"payload"`
	}
	if err := json.NewDecoder(f).Decode(&record); err != nil || record.Type != "session_meta" {
		return codexRolloutMeta{}, false
	}
	return record.Payload, true
}

// maxResumeAncestorHops bounds the walk up the sub-agent chain in
// resolveResumeTarget. Sub-agents can nest, but only a handful deep.
const maxResumeAncestorHops = 8

// resolveResumeTarget maps a stored conversation ID to the one Codex should
// actually resume. h2 builds before sub-agent detection could record a
// sub-agent's conversation as the agent's session identity; resuming that ID
// drops the user into an uncontrollable child session. Walk up to the top-level
// ancestor instead. Returns "" when no resumable ancestor exists, meaning the
// caller should start a fresh session rather than resume a broken one.
func resolveResumeTarget(configDir, convID string) string {
	if configDir == "" || convID == "" {
		return convID
	}
	for hop := 0; hop <= maxResumeAncestorHops; hop++ {
		_, meta, found := findCodexRollout(configDir, convID)
		if !found {
			// Nothing on disk to check against — resume as asked.
			return convID
		}
		if !meta.isSubagent() {
			return convID
		}
		parent := meta.parentID()
		if parent == "" || parent == convID {
			stdlog.Printf("codex: stored session %s is a sub-agent with no resumable parent; starting a fresh session", convID)
			return ""
		}
		stdlog.Printf("codex: stored session %s is a sub-agent; resuming parent %s instead", convID, parent)
		convID = parent
	}
	stdlog.Printf("codex: sub-agent chain for resume too deep; starting a fresh session")
	return ""
}

// --- Runtime (called after child process starts) ---

// Start forwards internal events to the external channel and blocks
// until ctx is cancelled.
func (h *CodexHarness) Start(ctx context.Context, events chan<- monitor.AgentEvent) error {
	// Tail the native rollout log for full assistant message text. Unlike
	// Claude, Codex's log path is only known once the conversation starts, so
	// the tailer waits for the discovery callback to deliver the path.
	go h.tailSessionLog(ctx)

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

// tailSessionLog tails the native rollout log, emitting an EventAgentMessage
// for each assistant message. The path arrives asynchronously once the
// conversation starts, and changes again on every relaunch — `codex resume`
// forks a new conversation with its own rollout file — so this loops, stopping
// the previous tailer whenever a new path is discovered.
//
// Only a fresh session's first rollout is read from the beginning. A resumed
// session's rollout opens with a copy of the prior conversation, which would
// otherwise be replayed as new activity.
func (h *CodexHarness) tailSessionLog(ctx context.Context) {
	var cancelPrev context.CancelFunc
	defer func() {
		if cancelPrev != nil {
			cancelPrev()
		}
	}()

	for {
		var path string
		select {
		case path = <-h.sessionLogPathCh:
		case <-ctx.Done():
			return
		}
		if path == "" {
			continue
		}

		if cancelPrev != nil {
			cancelPrev()
		}
		tailCtx, cancel := context.WithCancel(ctx)
		cancelPrev = cancel

		tailOnly := h.rc.ResumeSessionID != "" || h.sessionLogTailed
		h.sessionLogTailed = true

		collector := sessionlogcollector.New(path, h.eventHandler.OnSessionLogLine)
		if tailOnly {
			collector = sessionlogcollector.NewTailOnly(path, h.eventHandler.OnSessionLogLine)
		}
		go collector.Run(tailCtx)
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
