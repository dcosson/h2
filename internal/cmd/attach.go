package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"h2/internal/config"
	"h2/internal/session/message"
	"h2/internal/socketdir"
)

func newAttachCmd() *cobra.Command {
	var tile bool
	var dryRun bool
	var sessionID string

	cmd := &cobra.Command{
		Use:   "attach <name>",
		Short: "Attach to a running agent",
		Long: `Attach to a running agent's terminal session.

With --tile, open Ghostty splits for multiple agents at once.
Name can be a pod name, a single agent name, or a comma-separated list.
If a pod and agent share the same name, the pod takes priority.

With --session-id <id>, identify the agent by its underlying claude/codex
session id instead of by name (no name argument needed).

With --dry-run (requires --tile), show the computed layout and script
without executing anything.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if sessionID != "" {
				if len(args) > 0 {
					return fmt.Errorf("--session-id does not take an agent name argument")
				}
				if tile {
					return fmt.Errorf("--tile cannot be used with --session-id")
				}
				return doAttachBySessionID(sessionID)
			}
			if len(args) != 1 {
				return fmt.Errorf("attach requires an agent name (or --session-id <id>)")
			}
			if dryRun && !tile {
				return fmt.Errorf("--dry-run requires --tile")
			}
			if tile {
				return doTileAttach(args[0], dryRun)
			}
			return doAttach(args[0])
		},
	}

	cmd.Flags().BoolVar(&tile, "tile", false, "Tile agents in Ghostty splits")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show layout and script without executing (requires --tile)")
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Attach to the agent with this underlying claude/codex session id (no name needed)")
	return cmd
}

// doAttachBySessionID resolves an agent by its underlying harness session id and
// attaches to it. It errors with a helpful message if no h2 session matches or
// if the matching session's daemon is not currently running.
func doAttachBySessionID(harnessSessionID string) error {
	sessionDir := config.FindSessionDirByHarnessSessionID(harnessSessionID)
	if sessionDir == "" {
		return fmt.Errorf("no running session with id %q", harnessSessionID)
	}
	rc, err := config.ReadRuntimeConfig(sessionDir)
	if err != nil {
		return fmt.Errorf("session config for harness session id %q is invalid: %w", harnessSessionID, err)
	}
	// ensureAgentSocketAvailable returns nil when no live daemon is running
	// (and prunes a stale socket); a non-nil error means the agent is alive.
	if err := ensureAgentSocketAvailable(rc.AgentName); err == nil {
		return fmt.Errorf("no running session with id %q (agent %q is not running); resume it with: h2 run --resume-from-session-id %s",
			harnessSessionID, rc.AgentName, harnessSessionID)
	}
	return doAttach(rc.AgentName)
}

// doAttach connects to a running daemon and proxies terminal I/O. When the
// daemon sends a switch control frame (session fork, agent navigator), the
// connection is dropped and this loop reattaches to the requested agent.
func doAttach(name string) error {
	fd := int(os.Stdin.Fd())

	// Dial the first agent before touching terminal state so connection
	// errors print normally.
	conn, err := dialAndAttach(name, fd)
	if err != nil {
		return err
	}

	// Put terminal into raw mode.
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		conn.Close()
		return fmt.Errorf("set raw mode: %w", err)
	}
	defer func() {
		os.Stdout.WriteString("\033[?1000l\033[?1003l\033[?1006l") // Disable mouse modes
		term.Restore(fd, oldState)
		os.Stdout.WriteString("\033[?25h\033[0m\r\n")
	}()

	// Ignore SIGQUIT (Ctrl+\) and SIGINT (Ctrl+C) — in raw mode these
	// keystrokes are forwarded as bytes to the remote process.  Trapping
	// them here prevents Go's default handler from dumping goroutines and
	// crashing the attach client.
	signal.Ignore(syscall.SIGQUIT, syscall.SIGINT)

	// SIGWINCH notifications, consumed by the per-connection proxy loop.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)

	// Persistent stdin pump. It outlives individual connections so no
	// keystrokes are lost across a switch; the active proxy loop consumes it.
	stdinCh := make(chan []byte, 16)
	go func() {
		defer close(stdinCh)
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				stdinCh <- chunk
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		switchTo := proxyAttachConn(conn, fd, stdinCh, sigCh)
		conn.Close()
		if switchTo == "" {
			return nil
		}
		conn, err = dialAndAttach(switchTo, fd)
		if err != nil {
			return err
		}
	}
}

// dialAndAttach dials an agent's socket and completes the attach handshake.
func dialAndAttach(name string, fd int) (net.Conn, error) {
	sockPath, findErr := socketdir.Find(name)
	if findErr != nil {
		return nil, agentConnError(name, findErr)
	}
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, agentConnError(name, err)
	}

	cols, rows, err := term.GetSize(fd)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("get terminal size: %w", err)
	}
	colorHints := detectTerminalHints()

	if err := message.SendRequest(conn, &message.Request{
		Type:      "attach",
		Cols:      cols,
		Rows:      rows,
		OscFg:     colorHints.OscFg,
		OscBg:     colorHints.OscBg,
		ColorFGBG: colorHints.ColorFGBG,
	}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("send attach request: %w", err)
	}

	resp, err := message.ReadResponse(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read attach response: %w", err)
	}
	if !resp.OK {
		conn.Close()
		return nil, fmt.Errorf("attach failed: %s", resp.Error)
	}
	return conn, nil
}

// proxyAttachConn proxies terminal I/O over one attach connection until it
// ends. Returns the agent name to switch to if the daemon sent a switch
// control frame, or "" for a normal detach/disconnect.
func proxyAttachConn(conn net.Conn, fd int, stdinCh <-chan []byte, sigCh <-chan os.Signal) (switchTo string) {
	done := make(chan struct{})
	var switchName string

	// Goroutine: read frames from daemon → write to stdout.
	go func() {
		defer close(done)
		for {
			frameType, payload, err := message.ReadFrame(conn)
			if err != nil {
				return
			}
			switch frameType {
			case message.FrameTypeData:
				os.Stdout.Write(payload)
			case message.FrameTypeControl:
				if name := parseSwitchControl(payload); name != "" {
					switchName = name
					return
				}
			}
		}
	}()

	// Main loop: stdin and resize events → frames to daemon.
	for {
		select {
		case chunk, ok := <-stdinCh:
			if !ok {
				return ""
			}
			if err := message.WriteFrame(conn, message.FrameTypeData, chunk); err != nil {
				<-done
				return switchName
			}
		case <-sigCh:
			cols, rows, err := term.GetSize(fd)
			if err != nil || rows < 3 || cols < 1 {
				continue
			}
			ctrl, _ := json.Marshal(message.ResizeControl{
				Type: "resize",
				Cols: cols,
				Rows: rows,
			})
			message.WriteFrame(conn, message.FrameTypeControl, ctrl)
		case <-done:
			return switchName
		}
	}
}

// parseSwitchControl extracts the target agent name from a switch control
// frame payload, or returns "" if the payload is not a switch directive.
func parseSwitchControl(payload []byte) string {
	var ctrl message.SwitchControl
	if err := json.Unmarshal(payload, &ctrl); err != nil {
		return ""
	}
	if ctrl.Type != "switch" {
		return ""
	}
	return ctrl.Name
}
