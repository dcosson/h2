package session

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"h2/internal/automation"
	"h2/internal/config"
	"h2/internal/session/agent/monitor"
	"h2/internal/session/message"
	"h2/internal/session/virtualterminal"
	"h2/internal/socketdir"
)

// Daemon manages the Unix socket listener and attach protocol for a Session.
type Daemon struct {
	Session        *Session
	Listener       net.Listener
	StartTime      time.Time
	TriggerEngine  *automation.TriggerEngine
	ScheduleEngine *automation.ScheduleEngine
}

// sessionEnqueuer adapts a Session's MessageQueue to the automation.MessageEnqueuer interface.
type sessionEnqueuer struct {
	queue     *message.MessageQueue
	agentName string
}

func (e *sessionEnqueuer) EnqueueMessage(from, body, header string, priority message.Priority) (string, error) {
	return message.PrepareMessage(e.queue, e.agentName, from, body, priority, message.PrepareOpts{
		Header: header,
	})
}

// TerminalHints holds transient terminal color and type hints that
// the launcher captures and passes to the daemon via environment variables.
// These are not persisted in RuntimeConfig because they are ephemeral.
type TerminalHints struct {
	OscFg     string // X11 rgb foreground from OSC query
	OscBg     string // X11 rgb background from OSC query
	ColorFGBG string // COLORFGBG hint
	Term      string // TERM value
	ColorTerm string // COLORTERM value
}

// RunDaemon creates a Session and Daemon, sets up the socket, and runs
// the session in daemon mode. This is the main entry point for the _daemon command.
// All configuration comes from the RuntimeConfig which was written to
// sessionDir by the launcher before forking. sessionDir is the exact path
// used by the launcher (not reconstructed from agent name) to ensure
// consistency across symlinks, worktrees, and custom paths.
func RunDaemon(sessionDir string, rc *config.RuntimeConfig, resume bool) error {
	if resume {
		rc.ResumeSessionID = rc.HarnessSessionID
	}

	s := NewFromConfig(rc)

	s.StartTime = time.Now()
	s.SessionDir = sessionDir

	// Track whether NativeLogPathSuffix has been persisted to disk.
	// PrepareForLaunch sets it in memory but the initial WriteRuntimeConfig
	// (done by the launcher) may not include it.
	nativeLogSuffixPersisted := rc.NativeLogPathSuffix != ""

	// Wire OnSessionStarted callback to persist harness_session_id and
	// native_log_path_suffix. Write when the harness session ID changes
	// (e.g. Codex reports its conversation ID async) OR when NativeLogPathSuffix
	// was set by PrepareForLaunch but not yet persisted to disk.
	s.monitor.SetOnSessionStarted(func(data monitor.SessionStartedData) {
		needsWrite := false
		if data.SessionID != "" && data.SessionID != rc.HarnessSessionID {
			rc.HarnessSessionID = data.SessionID
			needsWrite = true
		}
		// PrepareForLaunch may have set NativeLogPathSuffix in memory
		// (e.g. Claude Code computes it deterministically at launch) but
		// it won't be on disk yet. Persist it so rotate can move the log.
		if rc.NativeLogPathSuffix != "" && !nativeLogSuffixPersisted {
			nativeLogSuffixPersisted = true
			needsWrite = true
		}
		if needsWrite {
			if err := config.WriteRuntimeConfig(sessionDir, rc); err != nil {
				log.Printf("warning: update runtime config: %v", err)
			}
		}

	})

	// Wire OnUsageLimit callback to persist rate limit info to the profile's
	// ratelimit.json so other tools (e.g. rotate) can check it.
	s.monitor.SetOnUsageLimit(func(data monitor.UsageLimitData) {
		profileDir := rc.HarnessConfigDir()
		if profileDir == "" {
			return
		}
		info := &config.RateLimitInfo{
			ResetsAt:   data.ResetsAt,
			Message:    data.Message,
			RecordedAt: time.Now(),
			AgentName:  rc.AgentName,
		}
		if err := config.WriteRateLimit(profileDir, info); err != nil {
			log.Printf("warning: write rate limit info: %v", err)
		}
	})

	// Wire OnAuthError callback to persist auth error info to the profile's
	// autherror.json so other tools (e.g. rotate, auth) can check it.
	s.monitor.SetOnAuthError(func(data monitor.AuthErrorData) {
		profileDir := rc.HarnessConfigDir()
		if profileDir == "" {
			return
		}
		info := &config.AuthErrorInfo{
			Message:    data.Message,
			RecordedAt: time.Now(),
			AgentName:  rc.AgentName,
		}
		if err := config.WriteAuthError(profileDir, info); err != nil {
			log.Printf("warning: write auth error info: %v", err)
		}
	})

	// Wire OnAuthErrorCleared callback to remove autherror.json when the
	// agent transitions out of auth_error state (e.g. after /login).
	s.monitor.SetOnAuthErrorCleared(func() {
		profileDir := rc.HarnessConfigDir()
		if profileDir == "" {
			return
		}
		if err := config.ClearAuthError(profileDir); err != nil {
			log.Printf("warning: clear auth error info: %v", err)
		}
	})

	// Create socket directory.
	if err := os.MkdirAll(socketdir.Dir(), 0o700); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}

	sockPath := socketdir.Path(socketdir.TypeAgent, rc.AgentName)

	if err := socketdir.ProbeSocket(sockPath, fmt.Sprintf("agent %q", rc.AgentName)); err != nil {
		return err
	}

	// Create Unix socket.
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("listen on socket: %w", err)
	}
	defer func() {
		ln.Close()
		os.Remove(sockPath)
	}()

	// Create automation engines with base env vars for conditions and actions.
	baseEnv := map[string]string{
		"H2_ACTOR": rc.AgentName,
	}
	if rc.RoleName != "" {
		baseEnv["H2_ROLE"] = rc.RoleName
	}
	if sessionDir != "" {
		baseEnv["H2_SESSION_DIR"] = sessionDir
	}

	stateProvider := func() (string, string) {
		st, sub := s.State()
		return st.String(), sub.String()
	}

	enqueuer := &sessionEnqueuer{queue: s.Queue, agentName: rc.AgentName}
	runner := automation.NewActionRunner(enqueuer, baseEnv, rc.CWD)
	triggerEngine := automation.NewTriggerEngine(runner, stateProvider)
	scheduleEngine := automation.NewScheduleEngine(runner, automation.WithStateProvider(stateProvider))

	// Subscribe TriggerEngine to monitor events.
	eventCh := s.monitor.Subscribe()

	d := &Daemon{
		Session:        s,
		Listener:       ln,
		StartTime:      s.StartTime,
		TriggerEngine:  triggerEngine,
		ScheduleEngine: scheduleEngine,
	}
	s.Daemon = d

	// Load role-defined triggers and schedules from RuntimeConfig.
	if err := d.loadRoleAutomations(rc); err != nil {
		ln.Close()
		os.Remove(sockPath)
		return fmt.Errorf("load role automations: %w", err)
	}

	// Start automation engines.
	automationCtx, automationCancel := context.WithCancel(context.Background())
	go triggerEngine.Run(automationCtx, eventCh)
	go scheduleEngine.Run(automationCtx)

	// Start socket listener.
	go d.acceptLoop()

	// Run session in daemon mode (blocks until exit).
	err = s.RunDaemon()
	automationCancel()
	runner.Wait()
	return err
}

// ReloadAutomations clears existing role-defined triggers/schedules and
// re-registers them from the current RuntimeConfig. Called after configRelaunch
// to pick up changes to the session metadata.
func (d *Daemon) ReloadAutomations() error {
	d.TriggerEngine.Clear()
	d.ScheduleEngine.Clear()
	return d.loadRoleAutomations(d.Session.RC)
}

// loadRoleAutomations registers triggers and schedules from the RuntimeConfig
// (originally defined in the role YAML). Called during daemon startup.
func (d *Daemon) loadRoleAutomations(rc *config.RuntimeConfig) error {
	now := time.Now()
	for _, ts := range rc.Triggers {
		t := &automation.Trigger{
			ID:        ts.ID,
			Name:      ts.Name,
			Event:     ts.Event,
			State:     ts.State,
			SubState:  ts.SubState,
			Condition: ts.Condition,
			Action: automation.Action{
				Exec:     ts.Exec,
				Message:  ts.Message,
				From:     ts.From,
				Priority: ts.Priority,
			},
			MaxFirings: ts.MaxFirings,
		}
		if ts.ExpiresAt != "" {
			parsed, err := automation.ResolveExpiresAt(ts.ExpiresAt, now)
			if err != nil {
				return fmt.Errorf("trigger %q: %w", ts.ID, err)
			}
			t.ExpiresAt = parsed
		}
		if ts.Cooldown != "" {
			parsed, err := time.ParseDuration(ts.Cooldown)
			if err != nil {
				return fmt.Errorf("trigger %q: parse cooldown %q: %w", ts.ID, ts.Cooldown, err)
			}
			if parsed < 0 {
				return fmt.Errorf("trigger %q: cooldown must be non-negative, got %s", ts.ID, parsed)
			}
			t.Cooldown = parsed
		}
		if !d.TriggerEngine.Add(t) {
			return fmt.Errorf("duplicate trigger ID %q in role config", ts.ID)
		}
	}

	for _, ss := range rc.Schedules {
		mode, _ := automation.ParseConditionMode(ss.ConditionMode)
		s := &automation.Schedule{
			ID:            ss.ID,
			Name:          ss.Name,
			Start:         ss.Start,
			RRule:         ss.RRule,
			Condition:     ss.Condition,
			ConditionMode: mode,
			Action: automation.Action{
				Exec:     ss.Exec,
				Message:  ss.Message,
				From:     ss.From,
				Priority: ss.Priority,
			},
		}
		if err := d.ScheduleEngine.Add(s); err != nil {
			return fmt.Errorf("register schedule %q: %w", ss.ID, err)
		}
	}
	return nil
}

// AgentInfo returns status information about this daemon.
func (d *Daemon) AgentInfo() *message.AgentInfo {
	s := d.Session
	uptime := time.Since(d.StartTime)
	st, sub := s.State()
	activity := s.ActivitySnapshot()
	var toolName string
	if st == monitor.StateActive {
		toolName = activity.LastToolName
	}
	info := &message.AgentInfo{
		Name:             s.Name(),
		Command:          s.RC.Command,
		SessionID:        s.RC.SessionID,
		Profile:          s.RC.Profile,
		RoleName:         s.RC.RoleName,
		Pod:              os.Getenv("H2_POD"),
		PodIndex:         s.RC.PodIndex,
		Uptime:           virtualterminal.FormatIdleDuration(uptime),
		State:            st.String(),
		SubState:         sub.String(),
		StateDisplayText: monitor.FormatStateLabel(st.String(), sub.String(), toolName),
		StateDuration:    virtualterminal.FormatIdleDuration(s.StateDuration()),
		QueuedCount:      s.Queue.PendingCount(),
	}
	if !activity.LastActivityAt.IsZero() {
		info.LastActivity = virtualterminal.FormatIdleDuration(time.Since(activity.LastActivityAt))
	}

	// Pull from OTEL collector if active.
	m := s.Metrics()
	if m.EventsReceived {
		info.InputTokens = m.InputTokens
		info.OutputTokens = m.OutputTokens
		info.TotalTokens = m.TotalTokens
		info.TotalCostUSD = m.TotalCostUSD
		info.ToolCounts = m.ToolCounts
	}

	// Point-in-time git stats.
	if gs := gitStats(); gs != nil {
		info.GitFilesChanged = gs.FilesChanged
		info.GitLinesAdded = gs.LinesAdded
		info.GitLinesRemoved = gs.LinesRemoved
	}

	info.LastToolUse = activity.LastToolName
	info.ToolUseCount = activity.ToolUseCount
	info.BlockedOnPermission = activity.BlockedOnPermission
	info.BlockedToolName = activity.BlockedToolName

	if resetsAt := s.UsageLimitResetsAt(); resetsAt != nil {
		info.UsageLimitResetsAt = resetsAt.Format(time.RFC3339)
	}
	info.UsageLimitMessage = s.UsageLimitMessage()
	info.AuthErrorMessage = s.AuthErrorMessage()

	return info
}

// gitDiffStats holds parsed git diff --numstat output.
type gitDiffStats struct {
	FilesChanged int
	LinesAdded   int64
	LinesRemoved int64
}

// gitStats runs git diff --numstat to get current uncommitted changes.
func gitStats() *gitDiffStats {
	return parseGitDiffStats()
}

// ForkDaemon starts a daemon in a background process by re-execing with
// the hidden _daemon subcommand. The RuntimeConfig must already be written
// to sessionDir before calling ForkDaemon.
func ForkDaemon(sessionDir string, termHints TerminalHints, resume bool) error {
	// Read RuntimeConfig to get agent name, CWD, and pod.
	rc, err := config.ReadRuntimeConfig(sessionDir)
	if err != nil {
		return fmt.Errorf("read runtime config for fork: %w", err)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}

	daemonArgs := []string{"_daemon", "--session-dir", sessionDir}
	if resume {
		daemonArgs = append(daemonArgs, "--resume")
	}
	cmd := exec.Command(exe, daemonArgs...)
	cmd.SysProcAttr = NewSysProcAttr()

	// Explicitly build environment: inherit parent env + additions.
	// Filter CLAUDECODE to prevent "nested session" errors when an agent
	// (running inside Claude Code) spawns another agent.
	env := filteredEnv(os.Environ(), "CLAUDECODE")
	if h2Dir, err := config.ResolveDir(); err == nil {
		env = append(env, "H2_DIR="+h2Dir)
	}
	if rc.Pod != "" {
		env = append(env, "H2_POD="+rc.Pod)
	}
	if termHints.OscFg != "" {
		env = append(env, "H2_OSC_FG="+termHints.OscFg)
	}
	if termHints.OscBg != "" {
		env = append(env, "H2_OSC_BG="+termHints.OscBg)
	}
	if termHints.ColorFGBG != "" {
		env = append(env, "H2_COLORFGBG="+termHints.ColorFGBG)
	}
	if termHints.Term != "" {
		env = append(env, "H2_TERM="+termHints.Term)
	}
	if termHints.ColorTerm != "" {
		env = append(env, "H2_COLORTERM="+termHints.ColorTerm)
	}
	cmd.Env = env

	// Set working directory for the child process.
	if rc.CWD != "" {
		cmd.Dir = rc.CWD
	}

	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	// Open /dev/null for stdin and stdout.
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return fmt.Errorf("open /dev/null: %w", err)
	}
	cmd.Stdin = devNull
	cmd.Stdout = devNull

	// Open a log file for stderr so panic stack traces are captured.
	var stderrFile *os.File
	if sessionDir != "" {
		stderrFile, err = os.OpenFile(
			filepath.Join(sessionDir, "daemon.stderr.log"),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644,
		)
		if err != nil {
			stderrFile = nil // fall back to /dev/null
		}
	}
	if stderrFile != nil {
		cmd.Stderr = stderrFile
	} else {
		cmd.Stderr = devNull
	}

	if err := cmd.Start(); err != nil {
		devNull.Close()
		if stderrFile != nil {
			stderrFile.Close()
		}
		return fmt.Errorf("start daemon: %w", err)
	}

	// Don't wait for the daemon - it runs independently.
	go func() {
		cmd.Wait()
		devNull.Close()
		if stderrFile != nil {
			stderrFile.Close()
		}
	}()

	// Wait for socket to appear.
	sockPath := socketdir.Path(socketdir.TypeAgent, rc.AgentName)
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if _, err := os.Stat(sockPath); err == nil {
			return nil
		}
	}

	return fmt.Errorf("daemon did not start (socket %s not found)", sockPath)
}

// filteredEnv returns a copy of env with entries matching any of the given
// keys removed. This prevents env vars like CLAUDECODE from leaking into
// child agent processes and triggering nested-session detection.
func filteredEnv(env []string, keys ...string) []string {
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		skip := false
		for _, key := range keys {
			if strings.HasPrefix(e, key+"=") {
				skip = true
				break
			}
		}
		if !skip {
			filtered = append(filtered, e)
		}
	}
	return filtered
}
