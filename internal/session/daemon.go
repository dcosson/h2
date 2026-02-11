package session

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"time"

	"h2/internal/config"
	"h2/internal/session/agent"
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

// RunDaemon creates a Session and Daemon, sets up the socket, and runs
// the session in daemon mode. This is the main entry point for the _daemon command.
func RunDaemon(name, sessionID, command string, args []string, roleName, sessionDir, claudeConfigDir string, heartbeat DaemonHeartbeat) error {
	s := New(name, command, args)
	s.SessionID = sessionID
	s.RoleName = roleName
	s.SessionDir = sessionDir
	s.ClaudeConfigDir = claudeConfigDir
	s.HeartbeatIdleTimeout = heartbeat.IdleTimeout
	s.HeartbeatMessage = heartbeat.Message
	s.HeartbeatCondition = heartbeat.Condition
	s.StartTime = time.Now()

	// Create socket directory.
	if err := os.MkdirAll(socketdir.Dir(), 0o700); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}

	sockPath := socketdir.Path(socketdir.TypeAgent, name)

	// Check if socket already exists.
	if _, err := os.Stat(sockPath); err == nil {
		conn, err := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return fmt.Errorf("agent %q is already running", name)
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
			AgentName:              name,
			SessionID:              sessionID,
			ClaudeConfigDir:        s.ClaudeConfigDir,
			CWD:                    cwd,
			ClaudeCodeSessionLogPath: config.ClaudeCodeSessionLogPath(s.ClaudeConfigDir, cwd, sessionID),
			Command:                command,
			Role:                   roleName,
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
	if st == agent.StateActive {
		if hc := s.Agent.HookCollector(); hc != nil {
			toolName = hc.Snapshot().LastToolName
		}
	}
	info := &message.AgentInfo{
		Name:             s.Name,
		Command:          s.Command,
		SessionID:        s.SessionID,
		RoleName:         s.RoleName,
		Uptime:           virtualterminal.FormatIdleDuration(uptime),
		State:            st.String(),
		SubState:         sub.String(),
		StateDisplayText: agent.FormatStateLabel(st.String(), sub.String(), toolName),
		StateDuration:    virtualterminal.FormatIdleDuration(s.StateDuration()),
		QueuedCount:      s.Queue.PendingCount(),
	}

	// Pull from OTEL collector if active.
	m := s.Agent.Metrics()
	if m.EventsReceived {
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

// ForkDaemon starts a daemon in a background process by re-execing with
// the hidden _daemon subcommand.
func ForkDaemon(name, sessionID, command string, args []string, roleName, sessionDir, claudeConfigDir string, heartbeat DaemonHeartbeat) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}

	daemonArgs := []string{"_daemon", "--name", name, "--session-id", sessionID}
	if roleName != "" {
		daemonArgs = append(daemonArgs, "--role", roleName)
	}
	if sessionDir != "" {
		daemonArgs = append(daemonArgs, "--session-dir", sessionDir)
	}
	if claudeConfigDir != "" {
		daemonArgs = append(daemonArgs, "--claude-config-dir", claudeConfigDir)
	}
	if heartbeat.IdleTimeout > 0 {
		daemonArgs = append(daemonArgs, "--heartbeat-idle-timeout", heartbeat.IdleTimeout.String())
		daemonArgs = append(daemonArgs, "--heartbeat-message", heartbeat.Message)
		if heartbeat.Condition != "" {
			daemonArgs = append(daemonArgs, "--heartbeat-condition", heartbeat.Condition)
		}
	}
	daemonArgs = append(daemonArgs, "--")
	daemonArgs = append(daemonArgs, command)
	daemonArgs = append(daemonArgs, args...)

	cmd := exec.Command(exe, daemonArgs...)
	cmd.SysProcAttr = NewSysProcAttr()

	// Propagate H2_DIR so the child process resolves the same h2 directory.
	if h2Dir, err := config.ResolveDir(); err == nil {
		cmd.Env = append(os.Environ(), "H2_DIR="+h2Dir)
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
	sockPath := socketdir.Path(socketdir.TypeAgent, name)
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if _, err := os.Stat(sockPath); err == nil {
			return nil
		}
	}

	return fmt.Errorf("daemon did not start (socket %s not found)", sockPath)
}
