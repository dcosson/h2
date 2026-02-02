package terminal

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/creack/pty"
	"github.com/muesli/termenv"
	"github.com/vito/midterm"
	"golang.org/x/term"
)

// InputMode represents the current input mode of the wrapper.
type InputMode int

const (
	ModeDefault InputMode = iota
	ModePassthrough
	ModeMenu
)

// MenuItems are the built-in menu entries.
var MenuItems = []string{"Clear input", "Redraw", "Quit"}

// Wrapper manages a PTY-wrapped child process with a persistent input bar.
type Wrapper struct {
	Ptm     *os.File         // PTY master (connected to child process)
	Cmd     *exec.Cmd        // child process
	Mu      sync.Mutex       // guards all terminal writes
	Restore *term.State      // original terminal state for cleanup
	Vt      *midterm.Terminal // virtual terminal for child output
	Input   []byte           // current command line buffer
	Rows    int              // terminal rows
	Cols    int              // terminal cols
	History []string         // command history
	HistIdx int              // current position in history (-1 = typing new)
	Saved   []byte           // saved input when browsing history
	Quit    bool             // true when user selected Quit
	OscFg   string           // cached OSC 10 response (foreground color)
	OscBg   string           // cached OSC 11 response (background color)
	LastOut time.Time        // last time child output updated the screen
	Mode    InputMode        // current input mode
	MenuIdx int              // selected menu item

	PendingSlash bool        // awaiting second slash for menu
	SlashTimer   *time.Timer // timer to promote single slash to passthrough

	PendingEsc     bool        // awaiting more bytes to disambiguate ESC
	EscTimer       *time.Timer // timer to treat ESC as passthrough exit
	PassthroughEsc []byte      // buffered escape sequence bytes

	ChildRows   int      // number of rows reserved for the child PTY
	DebugKeys   bool     // show debug keystrokes bar
	DebugKeyBuf []string // most recent keystrokes

	// AgentName is shown in the status bar when set.
	AgentName string
	// Output is where terminal output is written. Defaults to os.Stdout.
	Output io.Writer
	// InputSrc is where keyboard input is read from. Defaults to os.Stdin.
	InputSrc io.Reader
	// OnModeChange is called when entering/exiting passthrough (for queue pause).
	OnModeChange func(mode InputMode)
	// QueueStatus returns (count, paused) for the status bar queue indicator.
	QueueStatus func() (int, bool)
}

// Run starts the wrapper: creates a PTY, enters raw mode, and processes I/O.
// This is used for interactive mode (not daemon mode).
func (w *Wrapper) Run(command string, args ...string) error {
	fd := int(os.Stdin.Fd())

	cols, rows, err := term.GetSize(fd)
	if err != nil {
		return fmt.Errorf("get terminal size (is this a terminal?): %w", err)
	}
	w.DebugKeys = IsTruthyEnv("H2_DEBUG_KEYS")
	minRows := 3
	if w.DebugKeys {
		minRows = 4
	}
	if rows < minRows {
		return fmt.Errorf("terminal too small (need at least %d rows, have %d)", minRows, rows)
	}
	w.Rows = rows
	w.Cols = cols
	w.HistIdx = -1
	w.ChildRows = rows - w.ReservedRows()
	w.Vt = midterm.NewTerminal(w.ChildRows, cols)
	w.LastOut = time.Now()
	w.Mode = ModeDefault

	if w.Output == nil {
		w.Output = os.Stdout
	}
	if w.InputSrc == nil {
		w.InputSrc = os.Stdin
	}

	// Detect the real terminal's colors before entering raw mode.
	output := termenv.NewOutput(os.Stdout)
	if fg := output.ForegroundColor(); fg != nil {
		w.OscFg = ColorToX11(fg)
	}
	if bg := output.BackgroundColor(); bg != nil {
		w.OscBg = ColorToX11(bg)
	}
	if os.Getenv("COLORFGBG") == "" {
		colorfgbg := "0;15"
		if output.HasDarkBackground() {
			colorfgbg = "15;0"
		}
		os.Setenv("COLORFGBG", colorfgbg)
	}

	// Start child in a PTY.
	w.Cmd = exec.Command(command, args...)
	w.Ptm, err = pty.StartWithSize(w.Cmd, &pty.Winsize{
		Rows: uint16(w.ChildRows),
		Cols: uint16(cols),
	})
	if err != nil {
		return fmt.Errorf("start command: %w", err)
	}
	defer w.Ptm.Close()

	w.Vt.ForwardRequests = os.Stdout
	w.Vt.ForwardResponses = w.Ptm

	// Put our terminal into raw mode.
	w.Restore, err = term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("set raw mode: %w", err)
	}
	defer func() {
		term.Restore(fd, w.Restore)
		w.Output.(io.Writer).Write([]byte("\033[?25h\033[0m\r\n"))
	}()

	// Handle terminal resize.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go w.WatchResize(sigCh)

	// Update status bar every second.
	stopStatus := make(chan struct{})
	go w.TickStatus(stopStatus)

	// Draw initial UI.
	w.Mu.Lock()
	w.Output.Write([]byte("\033[2J\033[H"))
	w.RenderScreen()
	w.RenderBar()
	w.Mu.Unlock()

	// Pipe child output.
	go w.PipeOutput()

	// Process user keyboard input.
	go w.ReadInput()

	err = w.Cmd.Wait()
	close(stopStatus)
	return err
}

// RunDaemon starts the wrapper in daemon mode: creates a PTY and child process
// but does not interact with the local terminal. Output goes to io.Discard and
// input blocks until a client attaches via the attach protocol.
func (w *Wrapper) RunDaemon(command string, args ...string) error {
	// Default to 80x24 for the PTY. The first attach client will resize.
	w.Rows = 24
	w.Cols = 80
	w.HistIdx = -1
	w.DebugKeys = IsTruthyEnv("H2_DEBUG_KEYS")
	w.ChildRows = w.Rows - w.ReservedRows()
	w.Vt = midterm.NewTerminal(w.ChildRows, w.Cols)
	w.LastOut = time.Now()
	w.Mode = ModeDefault

	if w.Output == nil {
		w.Output = io.Discard
	}

	// Start child in a PTY.
	var err error
	w.Cmd = exec.Command(command, args...)
	w.Ptm, err = pty.StartWithSize(w.Cmd, &pty.Winsize{
		Rows: uint16(w.ChildRows),
		Cols: uint16(w.Cols),
	})
	if err != nil {
		return fmt.Errorf("start command: %w", err)
	}
	defer w.Ptm.Close()

	// Don't forward requests to stdout in daemon mode - there's no terminal.
	w.Vt.ForwardResponses = w.Ptm

	// Update status bar every second.
	stopStatus := make(chan struct{})
	go w.TickStatus(stopStatus)

	// Pipe child output to virtual terminal.
	go w.PipeOutput()

	err = w.Cmd.Wait()
	close(stopStatus)
	return err
}

// PipeOutput reads child PTY output into the virtual terminal and re-renders.
func (w *Wrapper) PipeOutput() {
	buf := make([]byte, 4096)
	for {
		n, err := w.Ptm.Read(buf)
		if n > 0 {
			w.RespondOSCColors(buf[:n])

			w.Mu.Lock()
			w.LastOut = time.Now()
			w.Vt.Write(buf[:n])
			w.RenderScreen()
			w.RenderBar()
			w.Mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// RespondOSCColors responds to OSC 10/11 color queries from the child.
func (w *Wrapper) RespondOSCColors(data []byte) {
	if w.OscFg != "" && bytes.Contains(data, []byte("\033]10;?")) {
		fmt.Fprintf(w.Ptm, "\033]10;%s\033\\", w.OscFg)
	}
	if w.OscBg != "" && bytes.Contains(data, []byte("\033]11;?")) {
		fmt.Fprintf(w.Ptm, "\033]11;%s\033\\", w.OscBg)
	}
}

// ReadInput reads keyboard input and dispatches to the current mode handler.
func (w *Wrapper) ReadInput() {
	buf := make([]byte, 256)
	for {
		n, err := w.InputSrc.Read(buf)
		if err != nil {
			return
		}

		w.Mu.Lock()
		if w.DebugKeys && n > 0 {
			w.AppendDebugBytes(buf[:n])
			w.RenderBar()
		}
		for i := 0; i < n; {
			switch w.Mode {
			case ModePassthrough:
				i = w.HandlePassthroughBytes(buf, i, n)
			case ModeMenu:
				i = w.HandleMenuBytes(buf, i, n)
			default:
				i = w.HandleDefaultBytes(buf, i, n)
			}
		}
		w.Mu.Unlock()
	}
}

func (w *Wrapper) setMode(mode InputMode) {
	w.Mode = mode
	if w.OnModeChange != nil {
		w.OnModeChange(mode)
	}
}

func (w *Wrapper) StartPendingEsc() {
	w.PendingEsc = true
	if w.EscTimer != nil {
		w.EscTimer.Stop()
	}
	w.EscTimer = time.AfterFunc(50*time.Millisecond, func() {
		w.Mu.Lock()
		defer w.Mu.Unlock()
		if w.PendingEsc && w.Mode == ModePassthrough {
			w.PendingEsc = false
			w.PassthroughEsc = w.PassthroughEsc[:0]
			w.setMode(ModeDefault)
			w.RenderBar()
		}
	})
}

func (w *Wrapper) CancelPendingEsc() {
	if w.EscTimer != nil {
		w.EscTimer.Stop()
	}
	w.PendingEsc = false
}

func (w *Wrapper) HandlePassthroughBytes(buf []byte, start, n int) int {
	for i := start; i < n; {
		b := buf[i]
		if w.PendingEsc {
			if b != '[' && b != 'O' {
				w.CancelPendingEsc()
				w.PassthroughEsc = w.PassthroughEsc[:0]
				w.setMode(ModeDefault)
				w.RenderBar()
				return i
			}
			w.CancelPendingEsc()
			w.PassthroughEsc = append(w.PassthroughEsc[:0], 0x1B, b)
			if w.FlushPassthroughEscIfComplete() {
				i++
				continue
			}
			i++
			continue
		}
		if len(w.PassthroughEsc) > 0 {
			w.PassthroughEsc = append(w.PassthroughEsc, b)
			if w.FlushPassthroughEscIfComplete() {
				i++
				continue
			}
			i++
			continue
		}
		switch b {
		case 0x0D, 0x0A:
			w.CancelPendingEsc()
			w.PassthroughEsc = w.PassthroughEsc[:0]
			w.Ptm.Write([]byte{'\r'})
			w.setMode(ModeDefault)
			w.RenderBar()
			i++
		case 0x1B:
			w.StartPendingEsc()
			i++
		case 0x7F, 0x08:
			w.Ptm.Write([]byte{b})
			i++
		default:
			w.Ptm.Write([]byte{b})
			i++
		}
	}
	return n
}

func (w *Wrapper) HandleMenuBytes(buf []byte, start, n int) int {
	for i := start; i < n; {
		b := buf[i]
		i++
		if b == 0x1B {
			consumed, handled := w.HandleEscape(buf[i:n])
			i += consumed
			if handled {
				continue
			}
			if i == n {
				w.setMode(ModeDefault)
				w.RenderBar()
			}
			continue
		}
		switch b {
		case 0x0D, 0x0A:
			w.MenuSelect()
			w.setMode(ModeDefault)
			w.RenderBar()
		}
	}
	return n
}

func (w *Wrapper) HandleDefaultBytes(buf []byte, start, n int) int {
	for i := start; i < n; {
		b := buf[i]
		i++

		if w.PendingSlash {
			w.CancelPendingSlash()
			if b == '/' {
				w.setMode(ModeMenu)
				w.MenuIdx = 0
				w.RenderBar()
				continue
			}
			w.setMode(ModePassthrough)
			w.Ptm.Write([]byte{'/'})
			w.RenderBar()
			switch b {
			case 0x0D, 0x0A:
				w.Ptm.Write([]byte{'\r'})
				w.setMode(ModeDefault)
				w.RenderBar()
			case 0x1B:
				if i == n {
					w.setMode(ModeDefault)
					w.RenderBar()
				} else {
					w.Ptm.Write([]byte{0x1B})
				}
			default:
				w.Ptm.Write([]byte{b})
			}
			continue
		}

		if b == '/' && len(w.Input) == 0 {
			w.StartPendingSlash()
			w.RenderBar()
			continue
		}

		if b == 0x1B {
			consumed, handled := w.HandleEscape(buf[i:n])
			i += consumed
			if handled {
				continue
			}
			continue
		}

		switch b {
		case 0x09:
			w.Ptm.Write([]byte{'\t'})

		case 0x0D, 0x0A:
			if len(w.Input) > 0 {
				cmd := string(w.Input)
				w.Ptm.Write(w.Input)
				w.History = append(w.History, cmd)
				w.Input = w.Input[:0]
				go func() {
					time.Sleep(50 * time.Millisecond)
					w.Ptm.Write([]byte{'\r'})
				}()
			} else {
				w.Ptm.Write([]byte{'\r'})
			}
			w.HistIdx = -1
			w.Saved = nil
			w.RenderBar()

		case 0x7F, 0x08:
			if len(w.Input) > 0 {
				_, size := utf8.DecodeLastRune(w.Input)
				w.Input = w.Input[:len(w.Input)-size]
				w.RenderBar()
			}

		default:
			if b < 0x20 {
				w.Ptm.Write([]byte{b})
			} else {
				w.Input = append(w.Input, b)
				w.RenderBar()
			}
		}
	}
	return n
}

func (w *Wrapper) ReservedRows() int {
	if w.DebugKeys {
		return 3
	}
	return 2
}

func (w *Wrapper) FlushPassthroughEscIfComplete() bool {
	if len(w.PassthroughEsc) == 0 {
		return false
	}
	if !IsEscSequenceComplete(w.PassthroughEsc) {
		return false
	}
	if IsShiftEnterSequence(w.PassthroughEsc) {
		w.Ptm.Write([]byte{'\n'})
	} else {
		w.Ptm.Write(w.PassthroughEsc)
	}
	w.PassthroughEsc = w.PassthroughEsc[:0]
	return true
}

// HandleEscape processes bytes following an ESC (0x1B).
func (w *Wrapper) HandleEscape(remaining []byte) (consumed int, handled bool) {
	if len(remaining) == 0 {
		return 0, false
	}

	switch remaining[0] {
	case '[':
		return w.HandleCSI(remaining[1:])
	case 'O':
		if len(remaining) >= 2 {
			return 2, true
		}
		return 1, true
	}
	return 0, false
}

// HandleCSI processes a CSI sequence (after ESC [).
func (w *Wrapper) HandleCSI(remaining []byte) (consumed int, handled bool) {
	if len(remaining) == 0 {
		return 1, true
	}

	i := 0
	for i < len(remaining) && remaining[i] >= 0x30 && remaining[i] <= 0x3F {
		i++
	}
	for i < len(remaining) && remaining[i] >= 0x20 && remaining[i] <= 0x2F {
		i++
	}
	if i >= len(remaining) {
		return 1 + i, true
	}

	final := remaining[i]
	totalConsumed := 1 + i + 1

	switch final {
	case 'A', 'B':
		if w.Mode == ModePassthrough {
			w.Ptm.Write(append([]byte{0x1B, '['}, remaining[:i+1]...))
			break
		}
		if w.Mode == ModeMenu {
			if final == 'A' {
				w.MenuPrev()
			} else {
				w.MenuNext()
			}
			w.RenderBar()
			break
		}
		if final == 'A' {
			w.HistoryUp()
		} else {
			w.HistoryDown()
		}
		w.RenderBar()
	case 'C', 'D':
		if w.Mode == ModePassthrough {
			w.Ptm.Write(append([]byte{0x1B, '['}, remaining[:i+1]...))
			break
		}
		if w.Mode == ModeMenu {
			if final == 'D' {
				w.MenuPrev()
			} else {
				w.MenuNext()
			}
			w.RenderBar()
		}
	}

	return totalConsumed, true
}

func (w *Wrapper) HistoryUp() {
	if len(w.History) == 0 {
		return
	}
	if w.HistIdx == -1 {
		w.Saved = make([]byte, len(w.Input))
		copy(w.Saved, w.Input)
		w.HistIdx = len(w.History) - 1
	} else if w.HistIdx > 0 {
		w.HistIdx--
	} else {
		return
	}
	w.Input = []byte(w.History[w.HistIdx])
}

func (w *Wrapper) HistoryDown() {
	if w.HistIdx == -1 {
		return
	}
	if w.HistIdx < len(w.History)-1 {
		w.HistIdx++
		w.Input = []byte(w.History[w.HistIdx])
	} else {
		w.HistIdx = -1
		w.Input = w.Saved
		w.Saved = nil
	}
}

func (w *Wrapper) StartPendingSlash() {
	w.PendingSlash = true
	if w.SlashTimer != nil {
		w.SlashTimer.Stop()
	}
	w.SlashTimer = time.AfterFunc(250*time.Millisecond, func() {
		w.Mu.Lock()
		defer w.Mu.Unlock()
		if !w.PendingSlash || w.Mode != ModeDefault {
			return
		}
		w.PendingSlash = false
		w.setMode(ModePassthrough)
		w.Ptm.Write([]byte{'/'})
		w.RenderBar()
	})
}

func (w *Wrapper) CancelPendingSlash() {
	w.PendingSlash = false
	if w.SlashTimer != nil {
		w.SlashTimer.Stop()
	}
}

func (w *Wrapper) MenuSelect() {
	switch w.MenuIdx {
	case 0:
		w.Input = w.Input[:0]
	case 1:
		w.Output.Write([]byte("\033[2J\033[H"))
		w.RenderScreen()
	case 2:
		w.Quit = true
		w.Cmd.Process.Signal(syscall.SIGTERM)
	}
}

func (w *Wrapper) MenuPrev() {
	if len(MenuItems) == 0 {
		return
	}
	if w.MenuIdx == 0 {
		w.MenuIdx = len(MenuItems) - 1
	} else {
		w.MenuIdx--
	}
}

func (w *Wrapper) MenuNext() {
	if len(MenuItems) == 0 {
		return
	}
	if w.MenuIdx == len(MenuItems)-1 {
		w.MenuIdx = 0
	} else {
		w.MenuIdx++
	}
}

// TickStatus triggers periodic status bar renders.
func (w *Wrapper) TickStatus(stop <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w.Mu.Lock()
			w.RenderBar()
			w.Mu.Unlock()
		case <-stop:
			return
		}
	}
}

// WatchResize handles SIGWINCH.
func (w *Wrapper) WatchResize(sigCh <-chan os.Signal) {
	for range sigCh {
		fd := int(os.Stdin.Fd())
		cols, rows, err := term.GetSize(fd)
		minRows := 3
		if w.DebugKeys {
			minRows = 4
		}
		if err != nil || rows < minRows {
			continue
		}

		w.Mu.Lock()
		w.Rows = rows
		w.Cols = cols
		w.ChildRows = rows - w.ReservedRows()
		w.Vt.Resize(w.ChildRows, cols)
		pty.Setsize(w.Ptm, &pty.Winsize{
			Rows: uint16(w.ChildRows),
			Cols: uint16(cols),
		})
		w.Output.Write([]byte("\033[2J"))
		w.RenderScreen()
		w.RenderBar()
		w.Mu.Unlock()
	}
}

// RenderScreen renders the virtual terminal buffer to the output.
func (w *Wrapper) RenderScreen() {
	var buf bytes.Buffer
	buf.WriteString("\033[?25l")
	for row := 0; row < w.ChildRows; row++ {
		fmt.Fprintf(&buf, "\033[%d;1H\033[2K", row+1)
		w.RenderLine(&buf, row)
	}
	w.Output.Write(buf.Bytes())
}

// RenderLine writes one row of the virtual terminal to buf.
func (w *Wrapper) RenderLine(buf *bytes.Buffer, row int) {
	if row >= len(w.Vt.Content) {
		return
	}
	line := w.Vt.Content[row]
	var pos int
	var lastFormat midterm.Format
	for region := range w.Vt.Format.Regions(row) {
		f := region.F
		if f != lastFormat {
			buf.WriteString("\033[0m")
			buf.WriteString(f.Render())
			lastFormat = f
		}
		end := pos + region.Size

		if pos < len(line) {
			contentEnd := end
			if contentEnd > len(line) {
				contentEnd = len(line)
			}
			buf.WriteString(string(line[pos:contentEnd]))
		}

		padStart := len(line)
		if padStart < pos {
			padStart = pos
		}
		if padStart < end {
			buf.WriteString(strings.Repeat(" ", end-padStart))
		}

		pos = end
	}
	buf.WriteString("\033[0m")
}

// RenderBar draws the separator line and input bar.
func (w *Wrapper) RenderBar() {
	var buf bytes.Buffer

	sepRow := w.Rows - 1
	inputRow := w.Rows
	debugRow := 0
	if w.DebugKeys {
		sepRow = w.Rows - 2
		inputRow = w.Rows - 1
		debugRow = w.Rows
	}

	// --- Separator line ---
	fmt.Fprintf(&buf, "\033[%d;1H\033[2K", sepRow)
	buf.WriteString(w.ModeBarStyle())
	help := w.HelpLabel()
	status := w.StatusLabel()
	label := " " + w.ModeLabel() + " | " + status

	// Queue indicator
	if w.QueueStatus != nil {
		count, paused := w.QueueStatus()
		if count > 0 {
			if paused {
				label += fmt.Sprintf(" | [%d paused]", count)
			} else {
				label += fmt.Sprintf(" | [%d queued]", count)
			}
		}
	}

	if help != "" {
		label += " | " + help
	}

	right := ""
	if w.AgentName != "" {
		right = w.AgentName + " "
	}

	if len(label)+len(right) > w.Cols {
		// Tight on space — drop help first, then right-align.
		label = " " + w.ModeLabel() + " | " + status
		if len(label)+len(right) > w.Cols {
			label = label[:w.Cols]
			right = ""
		}
	}

	buf.WriteString(label)
	gap := w.Cols - len(label) - len(right)
	if gap > 0 {
		buf.WriteString(strings.Repeat(" ", gap))
	}
	buf.WriteString(right)
	buf.WriteString("\033[0m")

	// --- Input line ---
	prompt := "> "
	inputStr := string(w.Input)
	maxInput := w.Cols - len(prompt)

	displayInput := inputStr
	runeCount := utf8.RuneCountInString(displayInput)
	if runeCount > maxInput && maxInput > 0 {
		runes := []rune(displayInput)
		displayInput = string(runes[len(runes)-maxInput:])
		runeCount = maxInput
	}
	fmt.Fprintf(&buf, "\033[%d;1H\033[2K", inputRow)
	fmt.Fprintf(&buf, "\033[36m%s\033[0m%s", prompt, displayInput)

	cursorCol := len(prompt) + runeCount + 1
	if cursorCol > w.Cols {
		cursorCol = w.Cols
	}
	fmt.Fprintf(&buf, "\033[%d;%dH", inputRow, cursorCol)

	if w.DebugKeys {
		fmt.Fprintf(&buf, "\033[%d;1H\033[2K", debugRow)
		debugLabel := w.DebugLabel()
		if len(debugLabel) > w.Cols {
			debugLabel = TrimLeftToWidth(debugLabel, w.Cols)
		}
		buf.WriteString(debugLabel)
		if pad := w.Cols - len(debugLabel); pad > 0 {
			buf.WriteString(strings.Repeat(" ", pad))
		}
	}

	buf.WriteString("\033[?25h")
	w.Output.Write(buf.Bytes())
}

// ModeLabel returns the display name for the current mode.
func (w *Wrapper) ModeLabel() string {
	switch w.Mode {
	case ModePassthrough:
		return "Passthrough"
	case ModeMenu:
		return w.MenuLabel()
	default:
		return "Default"
	}
}

// ModeBarStyle returns the ANSI style for the current mode.
func (w *Wrapper) ModeBarStyle() string {
	switch w.Mode {
	case ModePassthrough:
		return "\033[7m\033[33m"
	case ModeMenu:
		return "\033[7m\033[34m"
	default:
		return "\033[7m\033[36m"
	}
}

// HelpLabel returns context-sensitive help text.
func (w *Wrapper) HelpLabel() string {
	switch w.Mode {
	case ModePassthrough:
		return "Enter/Esc exit"
	case ModeMenu:
		return "Left/Right move | Enter select | Esc exit"
	default:
		return "Up/Down history | Enter send | / passthrough | // menu"
	}
}

// StatusLabel returns the current activity status.
func (w *Wrapper) StatusLabel() string {
	const idleThreshold = 2 * time.Second
	if w.LastOut.IsZero() {
		return "Active"
	}
	idleFor := time.Since(w.LastOut)
	if idleFor <= idleThreshold {
		return "Active"
	}
	return "Idle " + FormatIdleDuration(idleFor)
}

// MenuLabel returns the formatted menu display.
func (w *Wrapper) MenuLabel() string {
	var parts []string
	for i, item := range MenuItems {
		if i == w.MenuIdx {
			parts = append(parts, ">"+item)
		} else {
			parts = append(parts, item)
		}
	}
	return "Menu: " + strings.Join(parts, " | ")
}

// DebugLabel returns the debug keystroke display.
func (w *Wrapper) DebugLabel() string {
	prefix := " debug keystrokes: "
	if len(w.DebugKeyBuf) == 0 {
		return prefix
	}
	keys := strings.Join(w.DebugKeyBuf, " ")
	available := w.Cols - len(prefix)
	if available <= 0 {
		if w.Cols > 0 {
			return prefix[:w.Cols]
		}
		return ""
	}
	if len(keys) > available {
		keys = keys[len(keys)-available:]
	}
	return prefix + keys
}

func (w *Wrapper) AppendDebugBytes(data []byte) {
	for _, b := range data {
		w.DebugKeyBuf = append(w.DebugKeyBuf, FormatDebugKey(b))
		if len(w.DebugKeyBuf) > 10 {
			w.DebugKeyBuf = w.DebugKeyBuf[len(w.DebugKeyBuf)-10:]
		}
	}
}

// IsIdle returns true if the child process has been idle for at least the threshold.
func (w *Wrapper) IsIdle() bool {
	const idleThreshold = 2 * time.Second
	w.Mu.Lock()
	defer w.Mu.Unlock()
	return !w.LastOut.IsZero() && time.Since(w.LastOut) > idleThreshold
}

// Utility functions

// ColorToX11 converts a termenv.Color to X11 rgb: format.
func ColorToX11(c termenv.Color) string {
	switch v := c.(type) {
	case termenv.RGBColor:
		hex := string(v)
		if len(hex) == 7 && hex[0] == '#' {
			r, _ := strconv.ParseUint(hex[1:3], 16, 8)
			g, _ := strconv.ParseUint(hex[3:5], 16, 8)
			b, _ := strconv.ParseUint(hex[5:7], 16, 8)
			return fmt.Sprintf("rgb:%04x/%04x/%04x", r*0x101, g*0x101, b*0x101)
		}
	}
	return ""
}

func IsEscSequenceComplete(seq []byte) bool {
	if len(seq) < 2 {
		return false
	}
	switch seq[1] {
	case '[':
		if len(seq) < 3 {
			return false
		}
		final := seq[len(seq)-1]
		return final >= 0x40 && final <= 0x7E
	case 'O':
		return len(seq) >= 3
	default:
		return true
	}
}

func IsShiftEnterSequence(seq []byte) bool {
	if len(seq) < 3 {
		return false
	}
	if seq[1] != '[' {
		return false
	}
	final := seq[len(seq)-1]
	params := string(seq[2 : len(seq)-1])
	switch final {
	case '~':
		return params == "27;2;13" || params == "13;2"
	case 'u':
		return params == "13;2"
	default:
		return false
	}
}

func IsTruthyEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func FormatDebugKey(b byte) string {
	switch b {
	case 0x1B:
		return "esc"
	case 0x0D:
		return "cr"
	case 0x0A:
		return "lf"
	case 0x09:
		return "tab"
	case 0x7F:
		return "del"
	}
	if b < 0x20 {
		return fmt.Sprintf("0x%02x", b)
	}
	if b >= 0x20 && b <= 0x7E {
		return string([]byte{b})
	}
	return fmt.Sprintf("0x%02x", b)
}

func TrimLeftToWidth(s string, width int) string {
	if width <= 0 || len(s) <= width {
		return s
	}
	start := len(s) - width
	return s[start:]
}

func FormatIdleDuration(d time.Duration) string {
	if d < time.Minute {
		secs := int(d.Seconds())
		if secs < 1 {
			secs = 1
		}
		return fmt.Sprintf("%ds", secs)
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		return fmt.Sprintf("%dm", mins)
	}
	if d < 24*time.Hour {
		hrs := int(d.Hours())
		return fmt.Sprintf("%dh", hrs)
	}
	days := int(d.Hours() / 24)
	return fmt.Sprintf("%dd", days)
}
