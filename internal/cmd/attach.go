package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
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
	var resumeFromSessionID string

	cmd := &cobra.Command{
		Use:   "attach <name>",
		Short: "Attach to a running agent",
		Long: `Attach to a running agent's terminal session.

With --tile, open Ghostty splits for multiple agents at once.
Name can be a pod name, a single agent name, or a comma-separated list.
If a pod and agent share the same name, the pod takes priority.

With --resume-from-session-id <id>, identify the agent by its underlying
claude/codex session id instead of by name (no name argument needed).

With --dry-run (requires --tile), show the computed layout and script
without executing anything.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if resumeFromSessionID != "" {
				if len(args) > 0 {
					return fmt.Errorf("--resume-from-session-id does not take an agent name argument")
				}
				if tile {
					return fmt.Errorf("--tile cannot be used with --resume-from-session-id")
				}
				return doAttachBySessionID(resumeFromSessionID)
			}
			if len(args) != 1 {
				return fmt.Errorf("attach requires an agent name (or --resume-from-session-id <id>)")
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
	cmd.Flags().StringVar(&resumeFromSessionID, "resume-from-session-id", "", "Attach to the agent with this underlying claude/codex session id (no name needed)")
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

// doAttach connects to a running daemon and proxies terminal I/O.
func doAttach(name string) error {
	sockPath, findErr := socketdir.Find(name)
	if findErr != nil {
		return agentConnError(name, findErr)
	}
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return agentConnError(name, err)
	}
	defer conn.Close()

	fd := int(os.Stdin.Fd())
	cols, rows, err := term.GetSize(fd)
	if err != nil {
		return fmt.Errorf("get terminal size: %w", err)
	}
	colorHints := detectTerminalHints()

	// Send attach handshake.
	if err := message.SendRequest(conn, &message.Request{
		Type:      "attach",
		Cols:      cols,
		Rows:      rows,
		OscFg:     colorHints.OscFg,
		OscBg:     colorHints.OscBg,
		ColorFGBG: colorHints.ColorFGBG,
	}); err != nil {
		return fmt.Errorf("send attach request: %w", err)
	}

	resp, err := message.ReadResponse(conn)
	if err != nil {
		return fmt.Errorf("read attach response: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("attach failed: %s", resp.Error)
	}

	// Put terminal into raw mode.
	oldState, err := term.MakeRaw(fd)
	if err != nil {
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

	// Handle SIGWINCH for resizing.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go func() {
		for range sigCh {
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
		}
	}()

	done := make(chan struct{})
	var closeOnce sync.Once
	closeDone := func() { closeOnce.Do(func() { close(done) }) }

	// Goroutine: stdin → data frames to session.
	go func() {
		defer closeDone()
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				if err := message.WriteFrame(conn, message.FrameTypeData, buf[:n]); err != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Goroutine: read frames from daemon → write to stdout.
	go func() {
		defer closeDone()
		for {
			frameType, payload, err := message.ReadFrame(conn)
			if err != nil {
				return
			}
			if frameType == message.FrameTypeData {
				os.Stdout.Write(payload)
			}
		}
	}()

	<-done
	return nil
}
