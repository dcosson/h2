package virtualterminal

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/vito/midterm"
	"golang.org/x/term"
)

// VT owns the PTY lifecycle, child process, virtual terminal buffer, and I/O streams.
type VT struct {
	Ptm        *os.File          // PTY master (connected to child process)
	Cmd        *exec.Cmd         // child process
	Mu         sync.Mutex        // guards all terminal writes (overlay accesses via o.VT.Mu)
	Vt         *midterm.Terminal // virtual terminal for child output
	Scrollback *midterm.Terminal // append-only terminal for scroll history (never loses lines)
	Rows       int               // terminal rows
	Cols       int               // terminal cols
	ChildRows  int               // number of rows reserved for the child PTY
	Output     io.Writer         // stdout or frame writer (swapped on attach)
	InputSrc   io.Reader         // stdin or frame reader (swapped on attach)
	OscFg      string            // cached OSC 10 response (foreground color)
	OscBg      string            // cached OSC 11 response (background color)
	LastOut    time.Time         // last time child output updated the screen
	Restore    *term.State       // original terminal state for cleanup

	// Child process lifecycle state.
	ChildExited bool
	ChildHung   bool
	ExitError   error

	// ScrollRegionUsed is set when the child process sends DECSTBM (CSI...r),
	// indicating it uses scroll regions. When true, ScrollHistory is preferred
	// over VT.Scrollback for scrollback rendering.
	ScrollRegionUsed bool

	// ScrollHistory stores ANSI-formatted lines that scrolled off the top of
	// VT.Vt via midterm's OnScrollback callback. This captures scrollback from
	// apps that use scroll regions (e.g. codex inline viewport).
	ScrollHistory    []string
	scrollHistoryMax int

	// scanState tracks the ANSI parser state for ScanPTYOutput.
	scanState int
}

// SetupScrollCapture installs the OnScrollback callback on VT.Vt so that
// lines scrolling off the top of the visible screen are captured with ANSI
// formatting into ScrollHistory. Must be called after VT.Vt is created.
func (vt *VT) SetupScrollCapture() {
	if vt.scrollHistoryMax <= 0 {
		vt.scrollHistoryMax = 50000
	}
	vt.Vt.OnScrollback(func(line midterm.Line) {
		rendered := line.Display() + "\033[0m"
		vt.ScrollHistory = append(vt.ScrollHistory, rendered)
		if len(vt.ScrollHistory) > vt.scrollHistoryMax {
			trim := len(vt.ScrollHistory) - vt.scrollHistoryMax
			vt.ScrollHistory = vt.ScrollHistory[trim:]
		}
	})
}

// ResetScrollHistory clears the captured scroll history.
func (vt *VT) ResetScrollHistory() {
	vt.ScrollHistory = nil
}

// KillChild sends SIGKILL to the child process. Used when the child is hung
// and not responding to normal signals.
func (vt *VT) KillChild() {
	if vt.Cmd != nil && vt.Cmd.Process != nil {
		vt.Cmd.Process.Kill()
	}
}

// StartPTY creates and starts the child process in a PTY with the given size.
// If extraEnv is non-nil, those environment variables are added to the child's environment,
// overriding any existing values.
func (vt *VT) StartPTY(command string, args []string, childRows, cols int, extraEnv map[string]string) error {
	vt.Cmd = exec.Command(command, args...)
	if len(extraEnv) > 0 {
		// Build new env, filtering out keys we're overriding
		env := make([]string, 0, len(os.Environ())+len(extraEnv))
		for _, e := range os.Environ() {
			key := e
			if idx := strings.Index(e, "="); idx >= 0 {
				key = e[:idx]
			}
			if _, override := extraEnv[key]; !override {
				env = append(env, e)
			}
		}
		// Add our overrides
		for k, v := range extraEnv {
			env = append(env, k+"="+v)
		}
		vt.Cmd.Env = env
	}
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
			if vt.Scrollback != nil {
				vt.Scrollback.Write(buf[:n])
			}
			vt.ScanPTYOutput(buf[:n])
			onData()
			vt.Mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

const (
	scanNormal = iota
	scanEsc
	scanCSI
	scanOSC
	scanOSCEsc
)

// ScanPTYOutput scans child output for escape sequences that affect scroll
// behavior. Currently detects DECSTBM (CSI...r) to set ScrollRegionUsed.
func (vt *VT) ScanPTYOutput(data []byte) {
	if vt.ScrollRegionUsed {
		return // already detected, no need to keep scanning
	}
	for _, b := range data {
		switch vt.scanState {
		case scanEsc:
			if b == '[' {
				vt.scanState = scanCSI
			} else if b == ']' {
				vt.scanState = scanOSC
			} else {
				vt.scanState = scanNormal
			}
		case scanCSI:
			if b >= 0x40 && b <= 0x7E {
				if b == 'r' {
					vt.ScrollRegionUsed = true
					return
				}
				vt.scanState = scanNormal
			}
		case scanOSC:
			if b == 0x07 {
				vt.scanState = scanNormal
			} else if b == 0x1B {
				vt.scanState = scanOSCEsc
			}
		case scanOSCEsc:
			if b == '\\' {
				vt.scanState = scanNormal
			} else if b == 0x1B {
				vt.scanState = scanOSCEsc
			} else {
				vt.scanState = scanOSC
			}
		default:
			if b == 0x1B {
				vt.scanState = scanEsc
			}
		}
	}
}

// RespondOSCColors responds to OSC 10/11 color queries from the child.
func (vt *VT) RespondOSCColors(data []byte) {
	fg := vt.OscFg
	bg := vt.OscBg
	if fg == "" || bg == "" {
		fallbackFg, fallbackBg := FallbackOSCPalette(os.Getenv("COLORFGBG"))
		if fg == "" {
			fg = fallbackFg
		}
		if bg == "" {
			bg = fallbackBg
		}
	}
	if bytes.Contains(data, []byte("\033]10;?")) {
		fmt.Fprintf(vt.Ptm, "\033]10;%s\033\\", fg)
	}
	if bytes.Contains(data, []byte("\033]11;?")) {
		fmt.Fprintf(vt.Ptm, "\033]11;%s\033\\", bg)
	}
}

// Resize updates dimensions and resizes the virtual terminal and PTY.
func (vt *VT) Resize(totalRows, cols, childRows int) {
	vt.Rows = totalRows
	vt.Cols = cols
	vt.ChildRows = childRows
	vt.Vt.Resize(childRows, cols)
	if vt.Scrollback != nil {
		vt.Scrollback.ResizeX(cols)
	}
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

// ErrPTYWriteTimeout is returned by WritePTY when the write does not complete
// within the given deadline. The child process is likely hung (not reading stdin).
var ErrPTYWriteTimeout = fmt.Errorf("pty write timed out")

// WritePTY writes to the child PTY with a timeout. If the child is not reading
// its stdin, the kernel PTY buffer fills up and Write blocks indefinitely.
// This method runs the write in a goroutine so the caller can give up after a
// deadline and release the VT mutex.
func (vt *VT) WritePTY(p []byte, timeout time.Duration) (int, error) {
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		n, err := vt.Ptm.Write(p)
		ch <- result{n, err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.n, r.err
	case <-timer.C:
		return 0, ErrPTYWriteTimeout
	}
}
