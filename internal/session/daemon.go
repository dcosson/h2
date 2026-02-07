package session

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"h2/internal/session/message"
	"h2/internal/session/virtualterminal"
)

// Daemon manages the Unix socket listener and attach protocol for a Session.
type Daemon struct {
	Session   *Session
	Listener  net.Listener
	StartTime time.Time
}

// SocketDir returns the directory for Unix domain sockets.
func SocketDir() string {
	return filepath.Join(os.Getenv("HOME"), ".h2", "sockets")
}

// SocketPath returns the socket path for a given agent name.
func SocketPath(name string) string {
	return filepath.Join(SocketDir(), name+".sock")
}

// RunDaemon creates a Session and Daemon, sets up the socket, and runs
// the session in daemon mode. This is the main entry point for the _daemon command.
func RunDaemon(name, command string, args []string) error {
	s := New(name, command, args)
	s.StartTime = time.Now()

	// Create socket directory.
	if err := os.MkdirAll(SocketDir(), 0o700); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}

	sockPath := SocketPath(name)

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
	return &message.AgentInfo{
		Name:          s.Name,
		Command:       s.Command,
		Uptime:        virtualterminal.FormatIdleDuration(uptime),
		State:         s.State().String(),
		StateDuration: virtualterminal.FormatIdleDuration(s.StateDuration()),
		QueuedCount:   s.Queue.PendingCount(),
	}
}

// ListAgents scans the socket directory for running agents.
func ListAgents() ([]string, error) {
	dir := SocketDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) == ".sock" {
			names = append(names, name[:len(name)-5])
		}
	}
	return names, nil
}

// ForkDaemon starts a daemon in a background process by re-execing with
// the hidden _daemon subcommand.
func ForkDaemon(name string, command string, args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}

	daemonArgs := []string{"_daemon", "--name", name, "--"}
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
	sockPath := SocketPath(name)
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if _, err := os.Stat(sockPath); err == nil {
			return nil
		}
	}

	return fmt.Errorf("daemon did not start (socket %s not found)", sockPath)
}
