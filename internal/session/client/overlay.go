package client

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/muesli/termenv"
	"github.com/vito/midterm"
	"golang.org/x/term"

	"h2/internal/session/message"
	"h2/internal/session/virtualterminal"
)

// InputMode represents the current input mode of the overlay.
type InputMode int

const (
	ModeDefault InputMode = iota
	ModePassthrough
	ModeMenu
	ModeScroll
)

// Overlay owns all UI state and holds a pointer to the underlying VT.
type Overlay struct {
	VT          *virtualterminal.VT
	Input       []byte
	CursorPos   int // byte offset within Input
	History     []string
	HistIdx     int
	Saved       []byte
	Quit        bool
	Mode        InputMode
	PendingEsc     bool
	EscTimer       *time.Timer
	PassthroughEsc []byte
	ScrollOffset    int
	SelectHint      bool
	SelectHintTimer *time.Timer
	InputPriority   message.Priority
	DebugKeys     bool
	DebugKeyBuf  []string
	AgentName    string
	OnModeChange func(mode InputMode)
	QueueStatus  func() (int, bool)
	OtelMetrics  func() (totalTokens int64, totalCostUSD float64, connected bool, port int) // returns OTEL metrics for status bar
	OnSubmit     func(text string, priority message.Priority)     // called for non-normal input
	OnOutput     func()                                           // called after each child output
	OnDetach     func()                                           // called when user selects detach from menu

	// Child process lifecycle.
	ChildExited     bool
	ChildHung       bool
	ExitError       error
	relaunchCh      chan struct{}
	quitCh          chan struct{}
	OnChildExit     func()
	OnChildRelaunch func()

	// ExtraEnv holds additional environment variables to pass to the child process.
	ExtraEnv map[string]string
}

// Run starts the overlay in interactive mode: enters raw mode, starts the PTY,
// and processes I/O. This is used for interactive (non-daemon) mode.
func (o *Overlay) Run(command string, args ...string) error {
	fd := int(os.Stdin.Fd())

	cols, rows, err := term.GetSize(fd)
	if err != nil {
		return fmt.Errorf("get terminal size (is this a terminal?): %w", err)
	}
	o.DebugKeys = virtualterminal.IsTruthyEnv("H2_DEBUG_KEYS")
	minRows := 3
	if o.DebugKeys {
		minRows = 4
	}
	if rows < minRows {
		return fmt.Errorf("terminal too small (need at least %d rows, have %d)", minRows, rows)
	}
	o.VT.Rows = rows
	o.VT.Cols = cols
	o.HistIdx = -1
	o.VT.ChildRows = rows - o.ReservedRows()
	o.VT.Vt = midterm.NewTerminal(o.VT.ChildRows, cols)
	o.VT.Scrollback = midterm.NewTerminal(o.VT.ChildRows, cols)
	o.VT.Scrollback.AutoResizeY = true
	o.VT.Scrollback.AppendOnly = true
	o.VT.LastOut = time.Now()
	o.Mode = ModeDefault
	o.ScrollOffset = 0
	o.InputPriority = message.PriorityNormal

	if o.VT.Output == nil {
		o.VT.Output = os.Stdout
	}
	if o.VT.InputSrc == nil {
		o.VT.InputSrc = os.Stdin
	}

	// Detect the real terminal's colors before entering raw mode.
	output := termenv.NewOutput(os.Stdout)
	if fg := output.ForegroundColor(); fg != nil {
		o.VT.OscFg = virtualterminal.ColorToX11(fg)
	}
	if bg := output.BackgroundColor(); bg != nil {
		o.VT.OscBg = virtualterminal.ColorToX11(bg)
	}
	if os.Getenv("COLORFGBG") == "" {
		colorfgbg := "0;15"
		if output.HasDarkBackground() {
			colorfgbg = "15;0"
		}
		os.Setenv("COLORFGBG", colorfgbg)
	}

	// Start child in a PTY.
	if err := o.VT.StartPTY(command, args, o.VT.ChildRows, cols, o.ExtraEnv); err != nil {
		return err
	}

	o.VT.Vt.ForwardRequests = os.Stdout
	o.VT.Vt.ForwardResponses = o.VT.Ptm

	// Put our terminal into raw mode.
	o.VT.Restore, err = term.MakeRaw(fd)
	if err != nil {
		o.VT.Ptm.Close()
		return fmt.Errorf("set raw mode: %w", err)
	}
	// Enable SGR mouse reporting for scroll wheel support.
	o.VT.Output.(io.Writer).Write([]byte("\033[?1000h\033[?1006h"))
	defer func() {
		o.VT.Output.(io.Writer).Write([]byte("\033[?1000l\033[?1006l"))
		term.Restore(fd, o.VT.Restore)
		o.VT.Output.(io.Writer).Write([]byte("\033[?25h\033[0m\r\n"))
	}()

	// Handle terminal resize.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go o.WatchResize(sigCh)

	// Update status bar every second.
	stopStatus := make(chan struct{})
	go o.TickStatus(stopStatus)

	// Draw initial UI.
	o.VT.Mu.Lock()
	o.VT.Output.Write([]byte("\033[2J\033[H"))
	o.RenderScreen()
	o.RenderBar()
	o.VT.Mu.Unlock()

	// Pipe child output.
	go o.VT.PipeOutput(func() {
			if o.OnOutput != nil {
				o.OnOutput()
			}
			if o.Mode != ModeScroll {
				o.RenderScreen()
				o.RenderBar()
			}
		})

	// Process user keyboard input.
	go o.ReadInput()

	o.relaunchCh = make(chan struct{}, 1)
	o.quitCh = make(chan struct{}, 1)

	for {
		err = o.VT.Cmd.Wait()

		// If the user explicitly chose Quit from the menu, exit immediately.
		if o.Quit {
			o.VT.Ptm.Close()
			close(stopStatus)
			return err
		}

		o.VT.Mu.Lock()
		o.ChildExited = true
		o.ExitError = err
		o.RenderScreen()
		o.RenderBar()
		o.VT.Mu.Unlock()

		if o.OnChildExit != nil {
			o.OnChildExit()
		}

		select {
		case <-o.relaunchCh:
			o.VT.Ptm.Close()
			if err := o.VT.StartPTY(command, args, o.VT.ChildRows, o.VT.Cols, o.ExtraEnv); err != nil {
				close(stopStatus)
				return err
			}
			o.VT.Vt = midterm.NewTerminal(o.VT.ChildRows, o.VT.Cols)
			o.VT.Vt.ForwardRequests = os.Stdout
			o.VT.Vt.ForwardResponses = o.VT.Ptm
			o.VT.Scrollback = midterm.NewTerminal(o.VT.ChildRows, o.VT.Cols)
			o.VT.Scrollback.AutoResizeY = true
			o.VT.Scrollback.AppendOnly = true

			o.VT.Mu.Lock()
			o.ChildExited = false
			o.ChildHung = false
			o.ExitError = nil
			o.ScrollOffset = 0
			o.VT.LastOut = time.Now()
			o.VT.Output.Write([]byte("\033[2J\033[H"))
			o.RenderScreen()
			o.RenderBar()
			o.VT.Mu.Unlock()

			go o.VT.PipeOutput(func() {
			if o.OnOutput != nil {
				o.OnOutput()
			}
			if o.Mode != ModeScroll {
				o.RenderScreen()
				o.RenderBar()
			}
		})

			if o.OnChildRelaunch != nil {
				o.OnChildRelaunch()
			}
			continue

		case <-o.quitCh:
			o.VT.Ptm.Close()
			close(stopStatus)
			return err
		}
	}
}

// RunDaemon starts the overlay in daemon mode: creates a PTY and child process
// but does not interact with the local terminal. Output goes to io.Discard and
// input blocks until a client attaches via the attach protocol.
func (o *Overlay) RunDaemon(command string, args ...string) error {
	// Default to 80x24 for the PTY. The first attach client will resize.
	o.VT.Rows = 24
	o.VT.Cols = 80
	o.HistIdx = -1
	o.DebugKeys = virtualterminal.IsTruthyEnv("H2_DEBUG_KEYS")
	o.VT.ChildRows = o.VT.Rows - o.ReservedRows()
	o.VT.Vt = midterm.NewTerminal(o.VT.ChildRows, o.VT.Cols)
	o.VT.Scrollback = midterm.NewTerminal(o.VT.ChildRows, o.VT.Cols)
	o.VT.Scrollback.AutoResizeY = true
	o.VT.Scrollback.AppendOnly = true
	o.VT.LastOut = time.Now()
	o.Mode = ModeDefault
	o.ScrollOffset = 0
	o.InputPriority = message.PriorityNormal

	if o.VT.Output == nil {
		o.VT.Output = io.Discard
	}

	// Start child in a PTY.
	if err := o.VT.StartPTY(command, args, o.VT.ChildRows, o.VT.Cols, o.ExtraEnv); err != nil {
		return err
	}

	// Don't forward requests to stdout in daemon mode - there's no terminal.
	o.VT.Vt.ForwardResponses = o.VT.Ptm

	// Update status bar every second.
	stopStatus := make(chan struct{})
	go o.TickStatus(stopStatus)

	// Pipe child output to virtual terminal.
	go o.VT.PipeOutput(func() {
			if o.OnOutput != nil {
				o.OnOutput()
			}
			if o.Mode != ModeScroll {
				o.RenderScreen()
				o.RenderBar()
			}
		})

	o.relaunchCh = make(chan struct{}, 1)
	o.quitCh = make(chan struct{}, 1)

	for {
		err := o.VT.Cmd.Wait()

		if o.Quit {
			o.VT.Ptm.Close()
			close(stopStatus)
			return err
		}

		o.VT.Mu.Lock()
		o.ChildExited = true
		o.ExitError = err
		o.RenderScreen()
		o.RenderBar()
		o.VT.Mu.Unlock()

		if o.OnChildExit != nil {
			o.OnChildExit()
		}

		select {
		case <-o.relaunchCh:
			o.VT.Ptm.Close()
			if err := o.VT.StartPTY(command, args, o.VT.ChildRows, o.VT.Cols, o.ExtraEnv); err != nil {
				close(stopStatus)
				return err
			}
			o.VT.Vt = midterm.NewTerminal(o.VT.ChildRows, o.VT.Cols)
			o.VT.Vt.ForwardResponses = o.VT.Ptm
			o.VT.Scrollback = midterm.NewTerminal(o.VT.ChildRows, o.VT.Cols)
			o.VT.Scrollback.AutoResizeY = true
			o.VT.Scrollback.AppendOnly = true

			o.VT.Mu.Lock()
			o.ChildExited = false
			o.ChildHung = false
			o.ExitError = nil
			o.ScrollOffset = 0
			o.VT.LastOut = time.Now()
			o.VT.Output.Write([]byte("\033[2J\033[H"))
			o.RenderScreen()
			o.RenderBar()
			o.VT.Mu.Unlock()

			go o.VT.PipeOutput(func() {
			if o.OnOutput != nil {
				o.OnOutput()
			}
			if o.Mode != ModeScroll {
				o.RenderScreen()
				o.RenderBar()
			}
		})

			if o.OnChildRelaunch != nil {
				o.OnChildRelaunch()
			}
			continue

		case <-o.quitCh:
			o.VT.Ptm.Close()
			close(stopStatus)
			return err
		}
	}
}

// ReadInput reads keyboard input and dispatches to the current mode handler.
func (o *Overlay) ReadInput() {
	buf := make([]byte, 256)
	for {
		n, err := o.VT.InputSrc.Read(buf)
		if err != nil {
			return
		}

		o.VT.Mu.Lock()
		if o.DebugKeys && n > 0 {
			o.AppendDebugBytes(buf[:n])
			o.RenderBar()
		}
		for i := 0; i < n; {
			switch o.Mode {
			case ModePassthrough:
				i = o.HandlePassthroughBytes(buf, i, n)
			case ModeMenu:
				i = o.HandleMenuBytes(buf, i, n)
			case ModeScroll:
				i = o.HandleScrollBytes(buf, i, n)
			default:
				i = o.HandleDefaultBytes(buf, i, n)
			}
		}
		o.VT.Mu.Unlock()
	}
}

// TickStatus triggers periodic status bar renders.
func (o *Overlay) TickStatus(stop <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			o.VT.Mu.Lock()
			o.RenderBar()
			o.VT.Mu.Unlock()
		case <-stop:
			return
		}
	}
}

// WatchResize handles SIGWINCH.
func (o *Overlay) WatchResize(sigCh <-chan os.Signal) {
	for range sigCh {
		fd := int(os.Stdin.Fd())
		cols, rows, err := term.GetSize(fd)
		minRows := 3
		if o.DebugKeys {
			minRows = 4
		}
		if err != nil || rows < minRows {
			continue
		}

		o.VT.Mu.Lock()
		o.VT.Resize(rows, cols, rows-o.ReservedRows())
		if o.Mode == ModeScroll {
			o.ClampScrollOffset()
		}
		o.VT.Output.Write([]byte("\033[2J"))
		o.RenderScreen()
		o.RenderBar()
		o.VT.Mu.Unlock()
	}
}

// ReservedRows returns the number of rows reserved for the overlay UI.
func (o *Overlay) ReservedRows() int {
	if o.DebugKeys {
		return 3
	}
	return 2
}

// KillChild sends SIGKILL to the child process. Used when the child is hung
// and not responding to normal signals.
func (o *Overlay) KillChild() {
	if o.VT.Cmd != nil && o.VT.Cmd.Process != nil {
		o.VT.Cmd.Process.Kill()
	}
}
