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

	"h2/internal/session/message"
	"h2/internal/socketdir"
)

func newAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach <name>",
		Short: "Attach to a running agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doAttach(args[0])
		},
	}
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
	oscFg, oscBg, colorfgbg := detectTerminalColorHints()

	// Send attach handshake.
	if err := message.SendRequest(conn, &message.Request{
		Type:      "attach",
		Cols:      cols,
		Rows:      rows,
		OscFg:     oscFg,
		OscBg:     oscBg,
		ColorFGBG: colorfgbg,
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
		os.Stdout.WriteString("\033[?1000l\033[?1006l") // Disable mouse mode
		term.Restore(fd, oldState)
		os.Stdout.WriteString("\033[?25h\033[0m\r\n")
	}()

	// Handle SIGWINCH for resizing.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go func() {
		for range sigCh {
			cols, rows, err := term.GetSize(fd)
			if err != nil {
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
