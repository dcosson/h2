package client

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/muesli/termenv"
	"golang.org/x/term"

	"h2/internal/session/message"
	"h2/internal/session/virtualterminal"
)

// newTermOutput wraps termenv.NewOutput for testability.
var newTermOutput = func(w *os.File) *termenv.Output {
	return termenv.NewOutput(w)
}

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
	OnSubmit func(text string, priority message.Priority) // called for non-normal input
	OnDetach func()                                       // called when user selects detach from menu

	// Child process lifecycle callbacks (set by Session).
	OnRelaunch func() // called when user presses Enter after child exits
	OnQuit     func() // called when user presses q after child exits or selects Quit from menu
}

// InitClient initializes per-client state. Called by Session after creating
// the Client and setting its VT reference.
func (c *Client) InitClient() {
	c.HistIdx = -1
	c.DebugKeys = virtualterminal.IsTruthyEnv("H2_DEBUG_KEYS")
	c.Mode = ModeDefault
	c.ScrollOffset = 0
	c.InputPriority = message.PriorityNormal
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

// SetupInteractiveTerminal prepares the local terminal for interactive use:
// detects colors, enters raw mode, enables mouse reporting, and starts
// SIGWINCH handling and periodic status ticks. Returns a cleanup function
// and a stop channel for the status ticker.
func (c *Client) SetupInteractiveTerminal() (cleanup func(), stopStatus chan struct{}, err error) {
	fd := int(os.Stdin.Fd())

	// Detect the real terminal's colors before entering raw mode.
	output := newTermOutput(os.Stdout)
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

	// Put our terminal into raw mode.
	c.VT.Restore, err = term.MakeRaw(fd)
	if err != nil {
		return nil, nil, err
	}

	// Enable SGR mouse reporting for scroll wheel support.
	os.Stdout.Write([]byte("\033[?1000h\033[?1006h"))

	cleanup = func() {
		os.Stdout.Write([]byte("\033[?1000l\033[?1006l"))
		term.Restore(fd, c.VT.Restore)
		os.Stdout.Write([]byte("\033[?25h\033[0m\r\n"))
	}

	// Handle terminal resize.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go c.WatchResize(sigCh)

	// Update status bar every second.
	stopStatus = make(chan struct{})
	go c.TickStatus(stopStatus)

	// Draw initial UI.
	c.VT.Mu.Lock()
	c.VT.Output.Write([]byte("\033[2J\033[H"))
	c.RenderScreen()
	c.RenderBar()
	c.VT.Mu.Unlock()

	// Process user keyboard input.
	go c.ReadInput()

	return cleanup, stopStatus, nil
}

// ReservedRows returns the number of rows reserved for the overlay UI.
func (c *Client) ReservedRows() int {
	if c.DebugKeys {
		return 3
	}
	return 2
}
