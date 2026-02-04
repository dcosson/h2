package virtualterminal

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/vito/midterm"
	"golang.org/x/term"
)

// VT owns the PTY lifecycle, child process, virtual terminal buffer, and I/O streams.
type VT struct {
	Ptm       *os.File         // PTY master (connected to child process)
	Cmd       *exec.Cmd        // child process
	Mu        sync.Mutex       // guards all terminal writes (overlay accesses via o.VT.Mu)
	Vt        *midterm.Terminal // virtual terminal for child output
	Rows      int              // terminal rows
	Cols      int              // terminal cols
	ChildRows int              // number of rows reserved for the child PTY
	Output    io.Writer        // stdout or frame writer (swapped on attach)
	InputSrc  io.Reader        // stdin or frame reader (swapped on attach)
	OscFg     string           // cached OSC 10 response (foreground color)
	OscBg     string           // cached OSC 11 response (background color)
	LastOut   time.Time        // last time child output updated the screen
	Restore   *term.State      // original terminal state for cleanup
}

// StartPTY creates and starts the child process in a PTY with the given size.
func (vt *VT) StartPTY(command string, args []string, childRows, cols int) error {
	vt.Cmd = exec.Command(command, args...)
	var err error
	vt.Ptm, err = pty.StartWithSize(vt.Cmd, &pty.Winsize{
		Rows: uint16(childRows),
		Cols: uint16(cols),
	})
	if err != nil {
		return fmt.Errorf("start command: %w", err)
	}
	return nil
}

// PipeOutput reads child PTY output into the virtual terminal and calls
// onData after each write so the caller can re-render.
func (vt *VT) PipeOutput(onData func()) {
	buf := make([]byte, 4096)
	for {
		n, err := vt.Ptm.Read(buf)
		if n > 0 {
			vt.RespondOSCColors(buf[:n])

			vt.Mu.Lock()
			vt.LastOut = time.Now()
			vt.Vt.Write(buf[:n])
			onData()
			vt.Mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// RespondOSCColors responds to OSC 10/11 color queries from the child.
func (vt *VT) RespondOSCColors(data []byte) {
	if vt.OscFg != "" && bytes.Contains(data, []byte("\033]10;?")) {
		fmt.Fprintf(vt.Ptm, "\033]10;%s\033\\", vt.OscFg)
	}
	if vt.OscBg != "" && bytes.Contains(data, []byte("\033]11;?")) {
		fmt.Fprintf(vt.Ptm, "\033]11;%s\033\\", vt.OscBg)
	}
}

// Resize updates dimensions and resizes the virtual terminal and PTY.
func (vt *VT) Resize(totalRows, cols, childRows int) {
	vt.Rows = totalRows
	vt.Cols = cols
	vt.ChildRows = childRows
	vt.Vt.Resize(childRows, cols)
	pty.Setsize(vt.Ptm, &pty.Winsize{
		Rows: uint16(childRows),
		Cols: uint16(cols),
	})
}

// IsIdle returns true if the child process has been idle for at least the threshold.
func (vt *VT) IsIdle() bool {
	const idleThreshold = 2 * time.Second
	vt.Mu.Lock()
	defer vt.Mu.Unlock()
	return !vt.LastOut.IsZero() && time.Since(vt.LastOut) > idleThreshold
}
