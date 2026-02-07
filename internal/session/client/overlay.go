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

// Client owns all UI state and holds a pointer to the underlying VT.
type Client struct {
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
	relaunchCh      chan struct{}
	quitCh          chan struct{}
	OnChildExit     func()
	OnChildRelaunch func()

	// ExtraEnv holds additional environment variables to pass to the child process.
	ExtraEnv map[string]string
}

// Run starts the overlay in interactive mode: enters raw mode, starts the PTY,
// and processes I/O. This is used for interactive (non-daemon) mode.
func (c *Client) Run(command string, args ...string) error {
	fd := int(os.Stdin.Fd())

	cols, rows, err := term.GetSize(fd)
	if err != nil {
		return fmt.Errorf("get terminal size (is this a terminal?): %w", err)
	}
	c.DebugKeys = virtualterminal.IsTruthyEnv("H2_DEBUG_KEYS")
	minRows := 3
	if c.DebugKeys {
		minRows = 4
	}
	if rows < minRows {
		return fmt.Errorf("terminal too small (need at least %d rows, have %d)", minRows, rows)
	}
	c.VT.Rows = rows
	c.VT.Cols = cols
	c.HistIdx = -1
	c.VT.ChildRows = rows - c.ReservedRows()
	c.VT.Vt = midterm.NewTerminal(c.VT.ChildRows, cols)
	c.VT.Scrollback = midterm.NewTerminal(c.VT.ChildRows, cols)
	c.VT.Scrollback.AutoResizeY = true
	c.VT.Scrollback.AppendOnly = true
	c.VT.LastOut = time.Now()
	c.Mode = ModeDefault
	c.ScrollOffset = 0
	c.InputPriority = message.PriorityNormal

	if c.VT.Output == nil {
		c.VT.Output = os.Stdout
	}
	if c.VT.InputSrc == nil {
		c.VT.InputSrc = os.Stdin
	}

	// Detect the real terminal's colors before entering raw mode.
	output := termenv.NewOutput(os.Stdout)
	if fg := output.ForegroundColor(); fg != nil {
		c.VT.OscFg = virtualterminal.ColorToX11(fg)
	}
	if bg := output.BackgroundColor(); bg != nil {
		c.VT.OscBg = virtualterminal.ColorToX11(bg)
	}
	if os.Getenv("COLORFGBG") == "" {
		colorfgbg := "0;15"
		if output.HasDarkBackground() {
			colorfgbg = "15;0"
		}
		os.Setenv("COLORFGBG", colorfgbg)
	}

	// Start child in a PTY.
	if err := c.VT.StartPTY(command, args, c.VT.ChildRows, cols, c.ExtraEnv); err != nil {
		return err
	}

	c.VT.Vt.ForwardRequests = os.Stdout
	c.VT.Vt.ForwardResponses = c.VT.Ptm

	// Put our terminal into raw mode.
	c.VT.Restore, err = term.MakeRaw(fd)
	if err != nil {
		c.VT.Ptm.Close()
		return fmt.Errorf("set raw mode: %w", err)
	}
	// Enable SGR mouse reporting for scroll wheel support.
	c.VT.Output.(io.Writer).Write([]byte("\033[?1000h\033[?1006h"))
	defer func() {
		c.VT.Output.(io.Writer).Write([]byte("\033[?1000l\033[?1006l"))
		term.Restore(fd, c.VT.Restore)
		c.VT.Output.(io.Writer).Write([]byte("\033[?25h\033[0m\r\n"))
	}()

	// Handle terminal resize.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go c.WatchResize(sigCh)

	// Update status bar every second.
	stopStatus := make(chan struct{})
	go c.TickStatus(stopStatus)

	// Draw initial UI.
	c.VT.Mu.Lock()
	c.VT.Output.Write([]byte("\033[2J\033[H"))
	c.RenderScreen()
	c.RenderBar()
	c.VT.Mu.Unlock()

	// Pipe child output.
	go c.VT.PipeOutput(func() {
			if c.OnOutput != nil {
				c.OnOutput()
			}
			if c.Mode != ModeScroll {
				c.RenderScreen()
				c.RenderBar()
			}
		})

	// Process user keyboard input.
	go c.ReadInput()

	c.relaunchCh = make(chan struct{}, 1)
	c.quitCh = make(chan struct{}, 1)

	for {
		err = c.VT.Cmd.Wait()

		// If the user explicitly chose Quit from the menu, exit immediately.
		if c.Quit {
			c.VT.Ptm.Close()
			close(stopStatus)
			return err
		}

		c.VT.Mu.Lock()
		c.VT.ChildExited = true
		c.VT.ExitError = err
		c.RenderScreen()
		c.RenderBar()
		c.VT.Mu.Unlock()

		if c.OnChildExit != nil {
			c.OnChildExit()
		}

		select {
		case <-c.relaunchCh:
			c.VT.Ptm.Close()
			if err := c.VT.StartPTY(command, args, c.VT.ChildRows, c.VT.Cols, c.ExtraEnv); err != nil {
				close(stopStatus)
				return err
			}
			c.VT.Vt = midterm.NewTerminal(c.VT.ChildRows, c.VT.Cols)
			c.VT.Vt.ForwardRequests = os.Stdout
			c.VT.Vt.ForwardResponses = c.VT.Ptm
			c.VT.Scrollback = midterm.NewTerminal(c.VT.ChildRows, c.VT.Cols)
			c.VT.Scrollback.AutoResizeY = true
			c.VT.Scrollback.AppendOnly = true

			c.VT.Mu.Lock()
			c.VT.ChildExited = false
			c.VT.ChildHung = false
			c.VT.ExitError = nil
			c.ScrollOffset = 0
			c.VT.LastOut = time.Now()
			c.VT.Output.Write([]byte("\033[2J\033[H"))
			c.RenderScreen()
			c.RenderBar()
			c.VT.Mu.Unlock()

			go c.VT.PipeOutput(func() {
			if c.OnOutput != nil {
				c.OnOutput()
			}
			if c.Mode != ModeScroll {
				c.RenderScreen()
				c.RenderBar()
			}
		})

			if c.OnChildRelaunch != nil {
				c.OnChildRelaunch()
			}
			continue

		case <-c.quitCh:
			c.VT.Ptm.Close()
			close(stopStatus)
			return err
		}
	}
}

// RunDaemon starts the overlay in daemon mode: creates a PTY and child process
// but does not interact with the local terminal. Output goes to io.Discard and
// input blocks until a client attaches via the attach protocol.
func (c *Client) RunDaemon(command string, args ...string) error {
	// Default to 80x24 for the PTY. The first attach client will resize.
	c.VT.Rows = 24
	c.VT.Cols = 80
	c.HistIdx = -1
	c.DebugKeys = virtualterminal.IsTruthyEnv("H2_DEBUG_KEYS")
	c.VT.ChildRows = c.VT.Rows - c.ReservedRows()
	c.VT.Vt = midterm.NewTerminal(c.VT.ChildRows, c.VT.Cols)
	c.VT.Scrollback = midterm.NewTerminal(c.VT.ChildRows, c.VT.Cols)
	c.VT.Scrollback.AutoResizeY = true
	c.VT.Scrollback.AppendOnly = true
	c.VT.LastOut = time.Now()
	c.Mode = ModeDefault
	c.ScrollOffset = 0
	c.InputPriority = message.PriorityNormal

	if c.VT.Output == nil {
		c.VT.Output = io.Discard
	}

	// Start child in a PTY.
	if err := c.VT.StartPTY(command, args, c.VT.ChildRows, c.VT.Cols, c.ExtraEnv); err != nil {
		return err
	}

	// Don't forward requests to stdout in daemon mode - there's no terminal.
	c.VT.Vt.ForwardResponses = c.VT.Ptm

	// Update status bar every second.
	stopStatus := make(chan struct{})
	go c.TickStatus(stopStatus)

	// Pipe child output to virtual terminal.
	go c.VT.PipeOutput(func() {
			if c.OnOutput != nil {
				c.OnOutput()
			}
			if c.Mode != ModeScroll {
				c.RenderScreen()
				c.RenderBar()
			}
		})

	c.relaunchCh = make(chan struct{}, 1)
	c.quitCh = make(chan struct{}, 1)

	for {
		err := c.VT.Cmd.Wait()

		if c.Quit {
			c.VT.Ptm.Close()
			close(stopStatus)
			return err
		}

		c.VT.Mu.Lock()
		c.VT.ChildExited = true
		c.VT.ExitError = err
		c.RenderScreen()
		c.RenderBar()
		c.VT.Mu.Unlock()

		if c.OnChildExit != nil {
			c.OnChildExit()
		}

		select {
		case <-c.relaunchCh:
			c.VT.Ptm.Close()
			if err := c.VT.StartPTY(command, args, c.VT.ChildRows, c.VT.Cols, c.ExtraEnv); err != nil {
				close(stopStatus)
				return err
			}
			c.VT.Vt = midterm.NewTerminal(c.VT.ChildRows, c.VT.Cols)
			c.VT.Vt.ForwardResponses = c.VT.Ptm
			c.VT.Scrollback = midterm.NewTerminal(c.VT.ChildRows, c.VT.Cols)
			c.VT.Scrollback.AutoResizeY = true
			c.VT.Scrollback.AppendOnly = true

			c.VT.Mu.Lock()
			c.VT.ChildExited = false
			c.VT.ChildHung = false
			c.VT.ExitError = nil
			c.ScrollOffset = 0
			c.VT.LastOut = time.Now()
			c.VT.Output.Write([]byte("\033[2J\033[H"))
			c.RenderScreen()
			c.RenderBar()
			c.VT.Mu.Unlock()

			go c.VT.PipeOutput(func() {
			if c.OnOutput != nil {
				c.OnOutput()
			}
			if c.Mode != ModeScroll {
				c.RenderScreen()
				c.RenderBar()
			}
		})

			if c.OnChildRelaunch != nil {
				c.OnChildRelaunch()
			}
			continue

		case <-c.quitCh:
			c.VT.Ptm.Close()
			close(stopStatus)
			return err
		}
	}
}

// ReadInput reads keyboard input and dispatches to the current mode handler.
func (c *Client) ReadInput() {
	buf := make([]byte, 256)
	for {
		n, err := c.VT.InputSrc.Read(buf)
		if err != nil {
			return
		}

		c.VT.Mu.Lock()
		if c.DebugKeys && n > 0 {
			c.AppendDebugBytes(buf[:n])
			c.RenderBar()
		}
		for i := 0; i < n; {
			switch c.Mode {
			case ModePassthrough:
				i = c.HandlePassthroughBytes(buf, i, n)
			case ModeMenu:
				i = c.HandleMenuBytes(buf, i, n)
			case ModeScroll:
				i = c.HandleScrollBytes(buf, i, n)
			default:
				i = c.HandleDefaultBytes(buf, i, n)
			}
		}
		c.VT.Mu.Unlock()
	}
}

// TickStatus triggers periodic status bar renders.
func (c *Client) TickStatus(stop <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.VT.Mu.Lock()
			c.RenderBar()
			c.VT.Mu.Unlock()
		case <-stop:
			return
		}
	}
}

// WatchResize handles SIGWINCH.
func (c *Client) WatchResize(sigCh <-chan os.Signal) {
	for range sigCh {
		fd := int(os.Stdin.Fd())
		cols, rows, err := term.GetSize(fd)
		minRows := 3
		if c.DebugKeys {
			minRows = 4
		}
		if err != nil || rows < minRows {
			continue
		}

		c.VT.Mu.Lock()
		c.VT.Resize(rows, cols, rows-c.ReservedRows())
		if c.Mode == ModeScroll {
			c.ClampScrollOffset()
		}
		c.VT.Output.Write([]byte("\033[2J"))
		c.RenderScreen()
		c.RenderBar()
		c.VT.Mu.Unlock()
	}
}

// ReservedRows returns the number of rows reserved for the overlay UI.
func (c *Client) ReservedRows() int {
	if c.DebugKeys {
		return 3
	}
	return 2
}
