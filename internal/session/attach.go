package session

import (
	"encoding/json"
	"io"
	"net"

	"h2/internal/session/client"
	"h2/internal/session/message"
)

// AttachSession represents an active attach client connection.
type AttachSession struct {
	conn   net.Conn
	client *client.Client
}

// Close terminates the attach session.
func (a *AttachSession) Close() {
	if a.conn != nil {
		a.conn.Close()
	}
}

// handleAttach handles an incoming attach request from a client.
func (d *Daemon) handleAttach(conn net.Conn, req *message.Request) {
	// Send OK response before switching to framed protocol.
	if err := message.SendResponse(conn, &message.Response{OK: true}); err != nil {
		conn.Close()
		return
	}

	s := d.Session

	// Create a new client for this connection.
	cl := s.NewClient()
	s.AddClient(cl)

	attach := &AttachSession{conn: conn, client: cl}

	vt := s.VT

	// Set up per-client output for this connection.
	vt.Mu.Lock()
	cl.Output = &frameWriter{conn: conn}

	// Resize PTY to client's terminal size, but only if dimensions actually
	// changed. Unnecessary resizes send SIGWINCH to the child, which can
	// cause a screen clear + redraw race that produces a blank screen.
	if req.Cols > 0 && req.Rows > 0 {
		childRows := req.Rows - cl.ReservedRows()
		if req.Rows != vt.Rows || req.Cols != vt.Cols || childRows != vt.ChildRows {
			vt.Resize(req.Rows, req.Cols, childRows)
		}
	}

	// Set detach callback to close the client connection.
	cl.OnDetach = func() { conn.Close() }

	// Enable mouse reporting and render the current screen.
	// RenderScreen clears each line individually (\033[2K), so a full
	// screen clear (\033[2J) is unnecessary and would cause a visible flash.
	cl.Output.Write([]byte("\033[?1000h\033[?1006h"))
	cl.RenderScreen()
	cl.RenderBar()
	vt.Mu.Unlock()

	// Read input frames from client until disconnect.
	d.readClientInput(conn, cl)

	// Client disconnected — detach. Disable mouse on this client's output.
	vt.Mu.Lock()
	cl.OnDetach = nil
	cl.Output.Write([]byte("\033[?1000l\033[?1006l"))

	// Release passthrough ownership if this client held it.
	if s.PassthroughOwner == cl {
		s.PassthroughOwner = nil
		s.Queue.Unpause()
	}

	// Remove this client from the session.
	s.RemoveClient(cl)
	vt.Mu.Unlock()

	_ = attach // keep reference alive for the duration
}

// readClientInput reads framed input from the attach client and dispatches
// it to the given client.
func (d *Daemon) readClientInput(conn net.Conn, cl *client.Client) {
	for {
		frameType, payload, err := message.ReadFrame(conn)
		if err != nil {
			return // client disconnected
		}

		s := d.Session
		switch frameType {
		case message.FrameTypeData:
			vt := s.VT
			vt.Mu.Lock()
			if cl.DebugKeys && len(payload) > 0 {
				cl.AppendDebugBytes(payload)
				cl.RenderBar()
			}
			for i := 0; i < len(payload); {
				switch cl.Mode {
				case client.ModePassthrough:
					i = cl.HandlePassthroughBytes(payload, i, len(payload))
				case client.ModeMenu:
					i = cl.HandleMenuBytes(payload, i, len(payload))
				case client.ModeScroll:
					i = cl.HandleScrollBytes(payload, i, len(payload))
				default:
					i = cl.HandleDefaultBytes(payload, i, len(payload))
				}
			}
			vt.Mu.Unlock()

		case message.FrameTypeControl:
			var ctrl message.ResizeControl
			if err := json.Unmarshal(payload, &ctrl); err != nil {
				continue
			}
			if ctrl.Type == "resize" {
				vt := s.VT
				vt.Mu.Lock()
				childRows := ctrl.Rows - cl.ReservedRows()
				vt.Resize(ctrl.Rows, ctrl.Cols, childRows)
				if cl.Mode == client.ModeScroll {
					cl.ClampScrollOffset()
				}
				cl.Output.Write([]byte("\033[2J"))
				cl.RenderScreen()
				cl.RenderBar()
				vt.Mu.Unlock()
			}
		}
	}
}

// frameWriter wraps a net.Conn for writing attach data frames.
type frameWriter struct {
	conn net.Conn
}

func (fw *frameWriter) Write(p []byte) (int, error) {
	if err := message.WriteFrame(fw.conn, message.FrameTypeData, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// frameInputReader reads data frames from the attach client. It is used
// by the client's ReadInput goroutine when in direct (non-daemon) mode
// where the VT reads from InputSrc directly. In attach mode, we
// instead read frames in readClientInput, so this reader blocks forever
// until the connection is closed.
type frameInputReader struct {
	conn net.Conn
}

func (fr *frameInputReader) Read(p []byte) (int, error) {
	// Block until the connection closes. Input is handled by readClientInput.
	buf := make([]byte, 1)
	_, err := fr.conn.Read(buf)
	return 0, err
}

// blockingReader blocks forever on Read. Used when no client is attached.
type blockingReader struct {
	ch chan struct{}
}

func (br *blockingReader) Read(p []byte) (int, error) {
	if br.ch == nil {
		br.ch = make(chan struct{})
	}
	<-br.ch // blocks forever
	return 0, io.EOF
}
