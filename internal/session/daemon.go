package session

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"

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

// RunDaemon creates a Session and Daemon, sets up the socket, and runs
// the session in daemon mode. This is the main entry point for the _daemon command.
func RunDaemon(name, sessionID, command string, args []string, roleName, sessionDir string) error {
	s := New(name, command, args)
	s.SessionID = sessionID
	s.RoleName = roleName
	s.SessionDir = sessionDir
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
	info := &message.AgentInfo{
		Name:          s.Name,
		Command:       s.Command,
		SessionID:     s.SessionID,
		RoleName:      s.RoleName,
		Uptime:        virtualterminal.FormatIdleDuration(uptime),
		State:         s.State().String(),
		StateDuration: virtualterminal.FormatIdleDuration(s.StateDuration()),
		QueuedCount:   s.Queue.PendingCount(),
	}

	// Pull from OTEL collector if active.
	m := s.Agent.Metrics()
	if m.EventsReceived {
		info.TotalTokens = m.TotalTokens
		info.TotalCostUSD = m.TotalCostUSD
	}

	// Pull from hook collector if active.
	if hc := s.Agent.HookCollector(); hc != nil {
		hs := hc.State()
		info.LastToolUse = hs.LastToolName
		info.ToolUseCount = hs.ToolUseCount
	}

	return info
}

// ForkDaemon starts a daemon in a background process by re-execing with
// the hidden _daemon subcommand.
func ForkDaemon(name, sessionID, command string, args []string, roleName, sessionDir string) error {
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
	daemonArgs = append(daemonArgs, "--")
	daemonArgs = append(daemonArgs, command)
	daemonArgs = append(daemonArgs, args...)

	cmd := exec.Command(exe, daemonArgs...)
	cmd.SysProcAttr = NewSysProcAttr()
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
