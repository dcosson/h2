package session

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"h2/internal/config"
	"h2/internal/session/agent"
	"h2/internal/session/agent/monitor"
	"h2/internal/session/message"
	"h2/internal/session/virtualterminal"
	"h2/internal/socketdir"
)

// Daemon manages the Unix socket listener and attach protocol for a Session.
type Daemon struct {
	Session   *Session
	Listener  net.Listener
	StartTime time.Time
}

// DaemonHeartbeat holds heartbeat configuration for the daemon.
type DaemonHeartbeat struct {
	IdleTimeout time.Duration
	Message     string
	Condition   string
}

// RunDaemonOpts holds all options for running a daemon.
type RunDaemonOpts struct {
	Name            string
	SessionID       string
	Command         string
	Args            []string
	RoleName        string
	SessionDir      string
	ClaudeConfigDir string
	Instructions    string   // role instructions → --append-system-prompt
	SystemPrompt    string   // replaces default system prompt → --system-prompt
	Model           string   // model selection → --model
	PermissionMode  string   // permission mode → --permission-mode
	AllowedTools    []string // allowed tools → --allowedTools (comma-joined)
	DisallowedTools []string // disallowed tools → --disallowedTools (comma-joined)
	Heartbeat       DaemonHeartbeat
	Overrides       map[string]string // --override key=value pairs for metadata
}

// RunDaemon creates a Session and Daemon, sets up the socket, and runs
// the session in daemon mode. This is the main entry point for the _daemon command.
func RunDaemon(opts RunDaemonOpts) error {
	s := New(opts.Name, opts.Command, opts.Args)
	s.SessionID = opts.SessionID
	s.RoleName = opts.RoleName
	s.SessionDir = opts.SessionDir
	s.ClaudeConfigDir = opts.ClaudeConfigDir
	s.Instructions = opts.Instructions
	s.SystemPrompt = opts.SystemPrompt
	s.Model = opts.Model
	s.PermissionMode = opts.PermissionMode
	s.AllowedTools = opts.AllowedTools
	s.DisallowedTools = opts.DisallowedTools
	s.HeartbeatIdleTimeout = opts.Heartbeat.IdleTimeout
	s.HeartbeatMessage = opts.Heartbeat.Message
	s.HeartbeatCondition = opts.Heartbeat.Condition
	s.StartTime = time.Now()

	// Create socket directory.
	if err := os.MkdirAll(socketdir.Dir(), 0o700); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}

	sockPath := socketdir.Path(socketdir.TypeAgent, opts.Name)

	// Check if socket already exists.
	if _, err := os.Stat(sockPath); err == nil {
		conn, err := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return fmt.Errorf("agent %q is already running", opts.Name)
		}
		os.Remove(sockPath)
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

	// Write session metadata for h2 peek and other tools.
	if s.SessionDir != "" && s.ClaudeConfigDir != "" {
		cwd, _ := os.Getwd()
		meta := config.SessionMetadata{
			AgentName:              opts.Name,
			SessionID:              opts.SessionID,
			ClaudeConfigDir:        s.ClaudeConfigDir,
			CWD:                    cwd,
			ClaudeCodeSessionLogPath: config.ClaudeCodeSessionLogPath(s.ClaudeConfigDir, cwd, opts.SessionID),
			Command:                opts.Command,
			Role:                   opts.RoleName,
			Overrides:              opts.Overrides,
			StartedAt:              s.StartTime.UTC().Format(time.RFC3339),
		}
		if err := config.WriteSessionMetadata(s.SessionDir, meta); err != nil {
			log.Printf("warning: write session metadata: %v", err)
		}
	}

	d := &Daemon{
		Session:   s,
		Listener:  ln,
		StartTime: s.StartTime,
	}
	s.Daemon = d

	// Start socket listener.
	go d.acceptLoop()

	// Run session in daemon mode (blocks until exit).
	return s.RunDaemon()
}

// AgentInfo returns status information about this daemon.
func (d *Daemon) AgentInfo() *message.AgentInfo {
	s := d.Session
	uptime := time.Since(d.StartTime)
	st, sub := s.State()
	var toolName string
	if st == monitor.StateActive {
		if hc := s.Agent.HookCollector(); hc != nil {
			toolName = hc.Snapshot().LastToolName
		}
	}
	info := &message.AgentInfo{
		Name:             s.Name,
		Command:          s.Command,
		SessionID:        s.SessionID,
		RoleName:         s.RoleName,
		Pod:              os.Getenv("H2_POD"),
		Uptime:           virtualterminal.FormatIdleDuration(uptime),
		State:            st.String(),
		SubState:         sub.String(),
		StateDisplayText: monitor.FormatStateLabel(st.String(), sub.String(), toolName),
		StateDuration:    virtualterminal.FormatIdleDuration(s.StateDuration()),
		QueuedCount:      s.Queue.PendingCount(),
	}

	// Pull from OTEL collector if active.
	m := s.Agent.Metrics()
	if m.EventsReceived {
		info.InputTokens = m.InputTokens
		info.OutputTokens = m.OutputTokens
		info.TotalTokens = m.TotalTokens
		info.TotalCostUSD = m.TotalCostUSD
		info.LinesAdded = m.LinesAdded
		info.LinesRemoved = m.LinesRemoved
		info.ToolCounts = m.ToolCounts

		// Build per-model stats from OTEL metrics endpoint data.
		info.ModelStats = buildModelStats(m)
	}

	// Point-in-time git stats.
	if gs := gitStats(); gs != nil {
		info.GitFilesChanged = gs.FilesChanged
		info.GitLinesAdded = gs.LinesAdded
		info.GitLinesRemoved = gs.LinesRemoved
	}

	// Pull from hook collector if active.
	if hc := s.Agent.HookCollector(); hc != nil {
		hs := hc.Snapshot()
		info.LastToolUse = hs.LastToolName
		info.ToolUseCount = hs.ToolUseCount
		info.BlockedOnPermission = hs.BlockedOnPermission
		info.BlockedToolName = hs.BlockedToolName
	}

	return info
}

// buildModelStats converts per-model maps into a sorted slice of ModelStat.
func buildModelStats(m agent.OtelMetricsSnapshot) []message.ModelStat {
	if len(m.ModelCosts) == 0 && len(m.ModelTokens) == 0 {
		return nil
	}

	// Collect all model names.
	models := make(map[string]bool)
	for model := range m.ModelCosts {
		models[model] = true
	}
	for model := range m.ModelTokens {
		models[model] = true
	}

	var stats []message.ModelStat
	for model := range models {
		stat := message.ModelStat{
			Model:   model,
			CostUSD: m.ModelCosts[model],
		}
		if tokens, ok := m.ModelTokens[model]; ok {
			stat.InputTokens = tokens["input"]
			stat.OutputTokens = tokens["output"]
			stat.CacheRead = tokens["cacheRead"]
			stat.CacheCreate = tokens["cacheCreation"]
		}
		stats = append(stats, stat)
	}
	return stats
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

// ForkDaemonOpts holds all options for forking a daemon process.
type ForkDaemonOpts struct {
	Name            string
	SessionID       string
	Command         string
	Args            []string
	RoleName        string
	SessionDir      string
	ClaudeConfigDir string
	Instructions    string   // role instructions → --append-system-prompt
	SystemPrompt    string   // replaces default system prompt → --system-prompt
	Model           string   // model selection → --model
	PermissionMode  string   // permission mode → --permission-mode
	AllowedTools    []string // allowed tools → --allowedTools (comma-joined)
	DisallowedTools []string // disallowed tools → --disallowedTools (comma-joined)
	Heartbeat       DaemonHeartbeat
	CWD             string   // working directory for the child process
	Pod             string   // pod name (set as H2_POD env var)
	Overrides       []string // --override key=value pairs (recorded in session metadata)
}

// ForkDaemon starts a daemon in a background process by re-execing with
// the hidden _daemon subcommand.
func ForkDaemon(opts ForkDaemonOpts) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}

	daemonArgs := []string{"_daemon", "--name", opts.Name, "--session-id", opts.SessionID}
	if opts.RoleName != "" {
		daemonArgs = append(daemonArgs, "--role", opts.RoleName)
	}
	if opts.SessionDir != "" {
		daemonArgs = append(daemonArgs, "--session-dir", opts.SessionDir)
	}
	if opts.ClaudeConfigDir != "" {
		daemonArgs = append(daemonArgs, "--claude-config-dir", opts.ClaudeConfigDir)
	}
	if opts.Heartbeat.IdleTimeout > 0 {
		daemonArgs = append(daemonArgs, "--heartbeat-idle-timeout", opts.Heartbeat.IdleTimeout.String())
		daemonArgs = append(daemonArgs, "--heartbeat-message", opts.Heartbeat.Message)
		if opts.Heartbeat.Condition != "" {
			daemonArgs = append(daemonArgs, "--heartbeat-condition", opts.Heartbeat.Condition)
		}
	}
	if opts.Instructions != "" {
		daemonArgs = append(daemonArgs, "--instructions", opts.Instructions)
	}
	if opts.SystemPrompt != "" {
		daemonArgs = append(daemonArgs, "--system-prompt", opts.SystemPrompt)
	}
	if opts.Model != "" {
		daemonArgs = append(daemonArgs, "--model", opts.Model)
	}
	if opts.PermissionMode != "" {
		daemonArgs = append(daemonArgs, "--permission-mode", opts.PermissionMode)
	}
	for _, tool := range opts.AllowedTools {
		daemonArgs = append(daemonArgs, "--allowed-tool", tool)
	}
	for _, tool := range opts.DisallowedTools {
		daemonArgs = append(daemonArgs, "--disallowed-tool", tool)
	}
	for _, ov := range opts.Overrides {
		daemonArgs = append(daemonArgs, "--override", ov)
	}
	daemonArgs = append(daemonArgs, "--")
	daemonArgs = append(daemonArgs, opts.Command)
	daemonArgs = append(daemonArgs, opts.Args...)

	cmd := exec.Command(exe, daemonArgs...)
	cmd.SysProcAttr = NewSysProcAttr()

	// Explicitly build environment: inherit parent env + additions.
	// Filter CLAUDECODE to prevent "nested session" errors when an agent
	// (running inside Claude Code) spawns another agent.
	env := filteredEnv(os.Environ(), "CLAUDECODE")
	if h2Dir, err := config.ResolveDir(); err == nil {
		env = append(env, "H2_DIR="+h2Dir)
	}
	if opts.Pod != "" {
		env = append(env, "H2_POD="+opts.Pod)
	}
	cmd.Env = env

	// Set working directory for the child process.
	if opts.CWD != "" {
		cmd.Dir = opts.CWD
	}

	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	// Open /dev/null for stdio.
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return fmt.Errorf("open /dev/null: %w", err)
	}
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull

	if err := cmd.Start(); err != nil {
		devNull.Close()
		return fmt.Errorf("start daemon: %w", err)
	}

	// Don't wait for the daemon - it runs independently.
	go func() {
		cmd.Wait()
		devNull.Close()
	}()

	// Wait for socket to appear.
	sockPath := socketdir.Path(socketdir.TypeAgent, opts.Name)
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
