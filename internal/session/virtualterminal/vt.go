package virtualterminal

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"sort"
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

	// AltScrollEnabled is set when the child sends CSI ? 1007 h (enable
	// alternate scroll mode). When true, mouse scroll events are converted
	// to arrow key sequences instead of entering h2's scroll mode.
	AltScrollEnabled bool

	// SyncOutputActive is set when the child sends CSI ? 2026 h (begin
	// synchronized update). When true, render callbacks in PipeOutput are
	// suppressed until CSI ? 2026 l is received, so the screen is only
	// updated with the final state of the atomic update. This prevents
	// rendering intermediate states (e.g. cleared screen before redraw)
	// that cause flickering in apps like Claude Code.
	SyncOutputActive bool

	// ScrollHistory stores ANSI-formatted lines that scrolled off the top of
	// VT.Vt via midterm's OnScrollback callback. This captures scrollback from
	// apps that use scroll regions (e.g. codex inline viewport).
	ScrollHistory    []string
	scrollHistoryMax int

	// scanState tracks the ANSI parser state for ScanPTYOutput.
	scanState         int
	scanCSIPrivateNum int // accumulates mode number during CSI ? <num> h/l parsing
}

// SetupScrollCapture installs the OnScrollback callback on VT.Vt so that
// lines scrolling off the top of the visible screen are captured with ANSI
// formatting into ScrollHistory. Must be called after VT.Vt is created.
func (vt *VT) SetupScrollCapture() {
	if vt.scrollHistoryMax <= 0 {
		vt.scrollHistoryMax = 50000
	}
	vt.Vt.OnScrollback(func(line midterm.Line) {
		// Guard against Content/Format length mismatch in midterm: wide
		// characters or resize races can cause the two slices to diverge,
		// leading to an index-out-of-range panic in Line.Display().
		if len(line.Format) < len(line.Content) {
			padded := make([]midterm.Format, len(line.Content))
			copy(padded, line.Format)
			line.Format = padded
		} else if len(line.Format) > len(line.Content) {
			line.Format = line.Format[:len(line.Content)]
		}
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
		vt.Cmd.Env = MergeEnv(os.Environ(), extraEnv)
	}
	return vt.startPTY(childRows, cols)
}

// StartPTYWithEnvMap starts the child with exactly env plus command args. It
// does not inherit os.Environ and is used by gateway-managed sessions.
func (vt *VT) StartPTYWithEnvMap(command string, args []string, childRows, cols int, env map[string]string) error {
	vt.Cmd = exec.Command(command, args...)
	vt.Cmd.Env = EnvMapToList(env)
	return vt.startPTY(childRows, cols)
}

func (vt *VT) startPTY(childRows, cols int) error {
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

func MergeEnv(base []string, overrides map[string]string) []string {
	env := make(map[string]string, len(base)+len(overrides))
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			env[key] = value
		}
	}
	for key, value := range overrides {
		if key != "" {
			env[key] = value
		}
	}
	return EnvMapToList(env)
}

func EnvMapToList(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		if key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+env[key])
	}
	return result
}

// PipeOutput reads child PTY output into the virtual terminal and calls
// onData after each write so the caller can re-render.
func (vt *VT) PipeOutput(onData func()) {
	buf := make([]byte, 4096)
	for {
		n, err := vt.Ptm.Read(buf)
		if n > 0 {
			vt.pipeChunk(buf[:n], onData)
		}
		if err != nil {
			return
		}
	}
}

// pipeChunk processes a single chunk of PTY output. Panics are recovered
// per-chunk so the PipeOutput loop continues reading rather than dying.
// The mutex is released via defer so a panic mid-processing cannot deadlock
// the session.
func (vt *VT) pipeChunk(data []byte, onData func()) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic recovered in PipeOutput: %v\n%s\n", r, debug.Stack())
		}
	}()
	vt.Mu.Lock()
	defer vt.Mu.Unlock()
	vt.RespondTerminalQueries(data)
	vt.LastOut = time.Now()
	vt.Vt.Write(data)
	if vt.Scrollback != nil {
		vt.Scrollback.Write(data)
	}
	vt.ScanPTYOutput(data)
	if !vt.SyncOutputActive {
		onData()
	}
}

const (
	scanNormal = iota
	scanEsc
	scanCSI
	scanCSIPrivate // after ESC [ ?
	scanOSC
	scanOSCEsc
)

// ScanPTYOutput scans child output for escape sequences that affect rendering
// behavior. Detects DECSTBM (CSI...r) to set ScrollRegionUsed,
// DEC private mode 1007 (CSI?1007h/l) to toggle AltScrollEnabled, and
// DEC private mode 2026 (CSI?2026h/l) to toggle SyncOutputActive.
func (vt *VT) ScanPTYOutput(data []byte) {
	for _, b := range data {
		switch vt.scanState {
		case scanNormal:
			if b == 0x1B {
				vt.scanState = scanEsc
			}
		case scanEsc:
			if b == '[' {
				vt.scanState = scanCSI
			} else if b == ']' {
				vt.scanState = scanOSC
			} else {
				vt.scanState = scanNormal
			}
		case scanCSI:
			if b == '?' {
				vt.scanCSIPrivateNum = 0
				vt.scanState = scanCSIPrivate
			} else if b >= 0x40 && b <= 0x7E {
				if b == 'r' {
					vt.ScrollRegionUsed = true
				}
				vt.scanState = scanNormal
			}
			// Parameter/intermediate bytes (0x20-0x3F) stay in scanCSI.
		case scanCSIPrivate:
			if b >= '0' && b <= '9' {
				vt.scanCSIPrivateNum = vt.scanCSIPrivateNum*10 + int(b-'0')
			} else if b == 'h' {
				if vt.scanCSIPrivateNum == 1007 {
					vt.AltScrollEnabled = true
				} else if vt.scanCSIPrivateNum == 2026 {
					vt.SyncOutputActive = true
				}
				vt.scanState = scanNormal
			} else if b == 'l' {
				if vt.scanCSIPrivateNum == 1007 {
					vt.AltScrollEnabled = false
				} else if vt.scanCSIPrivateNum == 2026 {
					vt.SyncOutputActive = false
				}
				vt.scanState = scanNormal
			} else {
				// Semicolon or unexpected byte: bail.
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
		}
	}
}

// ResetScanState resets the ANSI parser state and detected flags for a new
// child process.
func (vt *VT) ResetScanState() {
	vt.scanState = scanNormal
	vt.scanCSIPrivateNum = 0
	vt.ScrollRegionUsed = false
	vt.AltScrollEnabled = false
	vt.SyncOutputActive = false
}

// RespondTerminalQueries responds to terminal capability queries from the
// child process. Scans raw PTY output and writes responses directly to the
// PTY before midterm parses the data.
//
// Handled queries:
//   - OSC 10 (foreground color): responds with cached/fallback X11 rgb value
//   - OSC 11 (background color): responds with cached/fallback X11 rgb value
//   - DA2 (CSI > c): responds as xterm v388 (CSI > 65 ; 388 ; 1 c)
//   - XTVERSION (CSI > 0 q): responds as xterm(388) (DCS > | xterm(388) ST)
//
// DA2 and XTVERSION are silently dropped by midterm, so without these
// responses the child times out and falls back to basic rendering.
func (vt *VT) RespondTerminalQueries(data []byte) {
	// OSC 10: foreground color query.
	if bytes.Contains(data, []byte("\033]10;?")) {
		fg := vt.OscFg
		if fg == "" {
			fg, _ = FallbackOSCPalette(os.Getenv("COLORFGBG"))
		}
		fmt.Fprintf(vt.Ptm, "\033]10;%s\033\\", fg)
	}
	// OSC 11: background color query.
	if bytes.Contains(data, []byte("\033]11;?")) {
		bg := vt.OscBg
		if bg == "" {
			_, bg = FallbackOSCPalette(os.Getenv("COLORFGBG"))
		}
		fmt.Fprintf(vt.Ptm, "\033]11;%s\033\\", bg)
	}
	// DA2: ESC [ > c  or  ESC [ > 0 c
	if bytes.Contains(data, []byte("\033[>c")) || bytes.Contains(data, []byte("\033[>0c")) {
		fmt.Fprintf(vt.Ptm, "\033[>65;388;1c")
	}
	// XTVERSION: ESC [ > 0 q  (some apps send ESC [ > q without 0)
	if bytes.Contains(data, []byte("\033[>0q")) || bytes.Contains(data, []byte("\033[>q")) {
		fmt.Fprintf(vt.Ptm, "\033P>|xterm(388)\033\\")
	}
}

// Resize updates dimensions and resizes the virtual terminal and PTY.
// Dimensions must be positive; invalid values are silently ignored.
func (vt *VT) Resize(totalRows, cols, childRows int) {
	if totalRows <= 0 || cols <= 0 || childRows <= 0 {
		return
	}
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
