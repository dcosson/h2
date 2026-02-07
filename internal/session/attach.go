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
	conn net.Conn
}

// Close terminates the attach session.
func (s *AttachSession) Close() {
	if s.conn != nil {
		s.conn.Close()
	}
}

// handleAttach handles an incoming attach request from a client.
func (d *Daemon) handleAttach(conn net.Conn, req *message.Request) {
	// Only one client at a time (v1).
	if d.attachClient != nil {
		message.SendResponse(conn, &message.Response{
			Error: "another client is already attached",
		})
		conn.Close()
		return
	}

	// Send OK response before switching to framed protocol.
	if err := message.SendResponse(conn, &message.Response{OK: true}); err != nil {
		conn.Close()
		return
	}

	session := &AttachSession{conn: conn}
	d.attachClient = session

	vt := d.VT
	ov := d.Overlay

	// Swap VT I/O to use the attach connection.
	vt.Mu.Lock()
	vt.Output = &frameWriter{conn: conn}
	vt.InputSrc = &frameInputReader{conn: conn}

	// Resize PTY to client's terminal size.
	if req.Cols > 0 && req.Rows > 0 {
		childRows := req.Rows - ov.ReservedRows()
		vt.Resize(req.Rows, req.Cols, childRows)
	}

	// Set detach callback to close the client connection.
	ov.OnDetach = func() { conn.Close() }

	// Send full screen redraw and enable mouse reporting.
	vt.Output.Write([]byte("\033[2J\033[H"))
	vt.Output.Write([]byte("\033[?1000h\033[?1006h"))
	ov.RenderScreen()
	ov.RenderBar()
	vt.Mu.Unlock()

	// Read input frames from client until disconnect.
	d.readClientInput(conn)

	// Client disconnected — detach. Disable mouse before swapping output.
	vt.Mu.Lock()
	ov.OnDetach = nil
	vt.Output.Write([]byte("\033[?1000l\033[?1006l"))
	vt.Output = io.Discard
	vt.InputSrc = &blockingReader{}
	vt.Mu.Unlock()

	d.attachClient = nil
}

// readClientInput reads framed input from the attach client and dispatches
// it to the overlay.
func (d *Daemon) readClientInput(conn net.Conn) {
	for {
		frameType, payload, err := message.ReadFrame(conn)
		if err != nil {
			return // client disconnected
		}

		switch frameType {
		case message.FrameTypeData:
			vt := d.VT
			ov := d.Overlay
			vt.Mu.Lock()
			if ov.DebugKeys && len(payload) > 0 {
				ov.AppendDebugBytes(payload)
				ov.RenderBar()
			}
			for i := 0; i < len(payload); {
				switch ov.Mode {
				case client.ModePassthrough:
					i = ov.HandlePassthroughBytes(payload, i, len(payload))
				case client.ModeMenu:
					i = ov.HandleMenuBytes(payload, i, len(payload))
				case client.ModeScroll:
					i = ov.HandleScrollBytes(payload, i, len(payload))
				default:
					i = ov.HandleDefaultBytes(payload, i, len(payload))
				}
			}
			vt.Mu.Unlock()

		case message.FrameTypeControl:
			var ctrl message.ResizeControl
			if err := json.Unmarshal(payload, &ctrl); err != nil {
				continue
			}
			if ctrl.Type == "resize" {
				vt := d.VT
				ov := d.Overlay
				vt.Mu.Lock()
				childRows := ctrl.Rows - ov.ReservedRows()
				vt.Resize(ctrl.Rows, ctrl.Cols, childRows)
				if ov.Mode == client.ModeScroll {
					ov.ClampScrollOffset()
				}
				vt.Output.Write([]byte("\033[2J"))
				ov.RenderScreen()
				ov.RenderBar()
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
// by the overlay's ReadInput goroutine when in direct (non-daemon) mode
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
