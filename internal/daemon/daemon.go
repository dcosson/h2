package daemon

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"h2/internal/message"
	"h2/internal/overlay"
	"h2/internal/virtualterminal"
)

// Daemon manages the lifecycle of a wrapped child process, its Unix socket
// listener, and message delivery.
type Daemon struct {
	Name      string
	Command   string
	Args      []string
	VT        *virtualterminal.VT
	Overlay   *overlay.Overlay
	Queue     *message.MessageQueue
	Listener  net.Listener
	StartTime time.Time

	stopDelivery chan struct{}
	attachClient *AttachSession
}

// SocketDir returns the directory for Unix domain sockets.
func SocketDir() string {
	return filepath.Join(os.Getenv("HOME"), ".h2", "sockets")
}

// SocketPath returns the socket path for a given agent name.
func SocketPath(name string) string {
	return filepath.Join(SocketDir(), name+".sock")
}

// Run starts the daemon: creates the VT, overlay, socket, delivery loop, and
// waits for the child to exit.
func (d *Daemon) Run() error {
	d.StartTime = time.Now()
	d.Queue = message.NewMessageQueue()
	d.stopDelivery = make(chan struct{})

	// Create socket directory.
	if err := os.MkdirAll(SocketDir(), 0o700); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}

	sockPath := SocketPath(d.Name)

	// Check if socket already exists.
	if _, err := os.Stat(sockPath); err == nil {
		// Try to connect to see if it's a live daemon.
		conn, err := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return fmt.Errorf("agent %q is already running", d.Name)
		}
		// Stale socket, remove it.
		os.Remove(sockPath)
	}

	// Create Unix socket.
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("listen on socket: %w", err)
	}
	d.Listener = ln
	defer func() {
		ln.Close()
		os.Remove(sockPath)
	}()

	// Set up VT and overlay.
	d.VT = &virtualterminal.VT{}
	d.Overlay = &overlay.Overlay{
		VT:        d.VT,
		AgentName: d.Name,
	}
	d.Overlay.OnModeChange = func(mode overlay.InputMode) {
		if mode == overlay.ModePassthrough {
			d.Queue.Pause()
		} else {
			d.Queue.Unpause()
		}
	}
	d.Overlay.QueueStatus = func() (int, bool) {
		return d.Queue.PendingCount(), d.Queue.IsPaused()
	}

	// Wire up child lifecycle callbacks.
	d.Overlay.OnChildExit = func() {
		d.Queue.Pause()
	}
	d.Overlay.OnChildRelaunch = func() {
		d.Queue.Unpause()
	}

	// Start socket listener.
	go d.acceptLoop()

	// Start message delivery.
	go message.RunDelivery(message.DeliveryConfig{
		Queue:     d.Queue,
		AgentName: d.Name,
		PtyWriter: &daemonPtyWriter{d: d},
		IsIdle:    d.VT.IsIdle,
		OnDeliver: func() {
			d.VT.Mu.Lock()
			d.Overlay.RenderBar()
			d.VT.Mu.Unlock()
		},
		Stop: d.stopDelivery,
	})

	// Run the overlay in daemon mode (blocks until user quits).
	err = d.Overlay.RunDaemon(d.Command, d.Args...)

	// Clean up.
	close(d.stopDelivery)
	if d.attachClient != nil {
		d.attachClient.Close()
	}

	return err
}

// daemonPtyWriter writes to the child PTY while holding the VT mutex.
// Uses WritePTY with a timeout to avoid blocking forever if the child
// stops reading.
type daemonPtyWriter struct {
	d *Daemon
}

func (pw *daemonPtyWriter) Write(p []byte) (int, error) {
	pw.d.VT.Mu.Lock()
	defer pw.d.VT.Mu.Unlock()
	if pw.d.Overlay.ChildExited || pw.d.Overlay.ChildHung {
		return 0, io.ErrClosedPipe
	}
	n, err := pw.d.VT.WritePTY(p, 3*time.Second)
	if err == virtualterminal.ErrPTYWriteTimeout {
		pw.d.Overlay.ChildHung = true
		pw.d.Overlay.KillChild()
		pw.d.Overlay.RenderBar()
		return 0, io.ErrClosedPipe
	}
	return n, err
}

// AgentInfo returns status information about this daemon.
func (d *Daemon) AgentInfo() *message.AgentInfo {
	uptime := time.Since(d.StartTime)
	return &message.AgentInfo{
		Name:        d.Name,
		Command:     d.Command,
		Uptime:      virtualterminal.FormatIdleDuration(uptime),
		QueuedCount: d.Queue.PendingCount(),
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
