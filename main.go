package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/creack/pty"
	"github.com/vito/midterm"
	"golang.org/x/term"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: tui-wrapper <command> [args...]\n")
		fmt.Fprintf(os.Stderr, "\nWraps a TUI application with a persistent input bar.\n")
		fmt.Fprintf(os.Stderr, "All keyboard input goes to the bottom bar.\n")
		fmt.Fprintf(os.Stderr, "Press Enter to send the command to the wrapped application.\n")
		os.Exit(1)
	}

	w := &wrapper{}
	err := w.run(os.Args[1], os.Args[2:]...)
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

type wrapper struct {
	ptm     *os.File           // PTY master (connected to child process)
	cmd     *exec.Cmd          // child process
	mu      sync.Mutex         // guards all terminal writes
	restore *term.State        // original terminal state for cleanup
	vt      *midterm.Terminal   // virtual terminal for child output
	input   []byte             // current command line buffer
	rows    int                // terminal rows
	cols    int                // terminal cols
	history []string           // command history
	histIdx int                // current position in history (-1 = typing new)
	saved   []byte             // saved input when browsing history
}

func (w *wrapper) run(command string, args ...string) error {
	fd := int(os.Stdin.Fd())

	cols, rows, err := term.GetSize(fd)
	if err != nil {
		return fmt.Errorf("get terminal size (is this a terminal?): %w", err)
	}
	if rows < 3 {
		return fmt.Errorf("terminal too small (need at least 3 rows, have %d)", rows)
	}
	w.rows = rows
	w.cols = cols
	w.histIdx = -1
	w.vt = midterm.NewTerminal(rows-2, cols)

	// Start child in a PTY, reserving 2 rows for separator + input bar.
	w.cmd = exec.Command(command, args...)
	w.ptm, err = pty.StartWithSize(w.cmd, &pty.Winsize{
		Rows: uint16(rows - 2),
		Cols: uint16(cols),
	})
	if err != nil {
		return fmt.Errorf("start command: %w", err)
	}
	defer w.ptm.Close()

	// Put our terminal into raw mode so we get every keystroke.
	w.restore, err = term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("set raw mode: %w", err)
	}
	defer func() {
		term.Restore(fd, w.restore)
		os.Stdout.WriteString("\033[?25h\033[0m\r\n")
	}()

	// Handle terminal resize.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go w.watchResize(sigCh)

	// Draw initial UI.
	w.mu.Lock()
	os.Stdout.WriteString("\033[2J\033[H")
	w.renderScreen()
	w.renderBar()
	w.mu.Unlock()

	// Pipe child output to our terminal.
	go w.pipeOutput()

	// Process user keyboard input.
	go w.readInput()

	return w.cmd.Wait()
}

// pipeOutput reads the child's terminal output into the virtual terminal,
// then re-renders the screen from midterm's buffer state.
func (w *wrapper) pipeOutput() {
	buf := make([]byte, 4096)
	for {
		n, err := w.ptm.Read(buf)
		if n > 0 {
			w.mu.Lock()
			w.vt.Write(buf[:n])
			w.renderScreen()
			w.renderBar()
			w.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// readInput reads keyboard input and either buffers it into the command line
// or handles control characters. Enter sends the buffered text to the child.
func (w *wrapper) readInput() {
	buf := make([]byte, 256)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return
		}

		w.mu.Lock()
		for i := 0; i < n; {
			b := buf[i]
			i++

			// Detect and handle escape sequences.
			if b == 0x1B {
				consumed, handled := w.handleEscape(buf[i:n])
				i += consumed
				if handled {
					continue
				}
				// Lone Escape with no recognized sequence: ignore.
				continue
			}

			switch b {
			case 0x11: // Ctrl+Q: quit wrapper
				w.mu.Unlock()
				w.cmd.Process.Signal(syscall.SIGTERM)
				return

			case 0x03: // Ctrl+C: send interrupt to child
				w.ptm.Write([]byte{0x03})

			case 0x04: // Ctrl+D: send EOF to child
				w.ptm.Write([]byte{0x04})

			case 0x0C: // Ctrl+L: force full redraw
				os.Stdout.WriteString("\033[2J\033[H")
				w.renderScreen()
				w.renderBar()

			case 0x0E: // Ctrl+N: next command in history
				w.historyDown()
				w.renderBar()

			case 0x10: // Ctrl+P: previous command in history
				w.historyUp()
				w.renderBar()

			case 0x15: // Ctrl+U: clear input line
				w.input = w.input[:0]
				w.renderBar()

			case 0x17: // Ctrl+W: delete last word
				w.deleteWord()
				w.renderBar()

			case 0x09: // Tab: send to child (for completion)
				w.ptm.Write([]byte{'\t'})

			case 0x0D, 0x0A: // Enter: send buffered text to child
				if len(w.input) > 0 {
					cmd := string(w.input)
					// Send text first, then \r after a short delay. The
					// child's UI framework (React/Ink) batches state updates,
					// so the submit handler won't see the typed text unless
					// we let a render cycle complete before sending Enter.
					w.ptm.Write(w.input)
					w.history = append(w.history, cmd)
					w.input = w.input[:0]
					go func() {
						time.Sleep(50 * time.Millisecond)
						w.ptm.Write([]byte{'\r'})
					}()
				} else {
					// Bare enter still forwarded (for confirmation prompts, etc.)
					w.ptm.Write([]byte{'\r'})
				}
				w.histIdx = -1
				w.saved = nil
				w.renderBar()

			case 0x7F, 0x08: // Backspace
				if len(w.input) > 0 {
					_, size := utf8.DecodeLastRune(w.input)
					w.input = w.input[:len(w.input)-size]
					w.renderBar()
				}

			default:
				if b >= 0x20 { // Printable
					w.input = append(w.input, b)
					w.renderBar()
				}
			}
		}
		w.mu.Unlock()
	}
}

// handleEscape processes bytes following an ESC (0x1B). It returns how many
// bytes were consumed and whether the sequence was handled.
// Must be called with w.mu held.
func (w *wrapper) handleEscape(remaining []byte) (consumed int, handled bool) {
	if len(remaining) == 0 {
		return 0, false
	}

	switch remaining[0] {
	case '[': // CSI sequence
		return w.handleCSI(remaining[1:])
	case 'O': // SS3 sequence (e.g. some function keys)
		if len(remaining) >= 2 {
			return 2, true // skip 'O' + final byte
		}
		return 1, true
	}
	return 0, false
}

// handleCSI processes a CSI sequence (after ESC [).
// Must be called with w.mu held.
func (w *wrapper) handleCSI(remaining []byte) (consumed int, handled bool) {
	if len(remaining) == 0 {
		return 1, true // consumed the '['
	}

	// Find the end of the CSI sequence.
	// Parameter bytes: 0x30-0x3F
	// Intermediate bytes: 0x20-0x2F
	// Final byte: 0x40-0x7E
	i := 0
	for i < len(remaining) && remaining[i] >= 0x30 && remaining[i] <= 0x3F {
		i++ // parameter bytes
	}
	for i < len(remaining) && remaining[i] >= 0x20 && remaining[i] <= 0x2F {
		i++ // intermediate bytes
	}
	if i >= len(remaining) {
		return 1 + i, true // incomplete sequence, skip what we have
	}

	final := remaining[i]
	totalConsumed := 1 + i + 1 // '[' + params/intermediates + final byte

	switch final {
	case 'A', 'B': // Up/Down arrows: forward to child PTY
		w.ptm.Write(append([]byte{0x1B, '['}, remaining[:i+1]...))
	}
	// All other CSI sequences (left, right, etc.) are ignored for now.

	return totalConsumed, true
}

// historyUp navigates to the previous command in history.
func (w *wrapper) historyUp() {
	if len(w.history) == 0 {
		return
	}
	if w.histIdx == -1 {
		// Save current input before browsing history.
		w.saved = make([]byte, len(w.input))
		copy(w.saved, w.input)
		w.histIdx = len(w.history) - 1
	} else if w.histIdx > 0 {
		w.histIdx--
	} else {
		return
	}
	w.input = []byte(w.history[w.histIdx])
}

// historyDown navigates to the next command in history.
func (w *wrapper) historyDown() {
	if w.histIdx == -1 {
		return
	}
	if w.histIdx < len(w.history)-1 {
		w.histIdx++
		w.input = []byte(w.history[w.histIdx])
	} else {
		// Restore saved input.
		w.histIdx = -1
		w.input = w.saved
		w.saved = nil
	}
}

// deleteWord removes the last word from the input buffer.
func (w *wrapper) deleteWord() {
	// Trim trailing spaces.
	for len(w.input) > 0 && w.input[len(w.input)-1] == ' ' {
		w.input = w.input[:len(w.input)-1]
	}
	// Remove until next space or start.
	for len(w.input) > 0 && w.input[len(w.input)-1] != ' ' {
		w.input = w.input[:len(w.input)-1]
	}
}

// renderScreen renders the virtual terminal's screen buffer to stdout using
// absolute cursor positioning. Must be called with w.mu held.
func (w *wrapper) renderScreen() {
	var buf bytes.Buffer
	buf.WriteString("\033[?25l") // hide cursor during render
	childRows := w.rows - 2
	for row := 0; row < childRows; row++ {
		fmt.Fprintf(&buf, "\033[%d;1H\033[2K", row+1) // position + clear line
		w.vt.RenderLine(&buf, row)
	}
	os.Stdout.Write(buf.Bytes())
}

// renderBar draws the separator line and input bar at the bottom of the
// terminal, leaving the cursor at the input bar. Must be called with w.mu held.
func (w *wrapper) renderBar() {
	var buf bytes.Buffer

	// --- Separator line (second-to-last row) ---
	fmt.Fprintf(&buf, "\033[%d;1H\033[2K", w.rows-1)
	buf.WriteString("\033[7m") // reverse video
	label := " Ctrl+Q quit | Ctrl+U clear | Ctrl+P/N history | Enter send "
	if len(label) > w.cols {
		label = label[:w.cols]
	}
	buf.WriteString(label)
	if pad := w.cols - len(label); pad > 0 {
		buf.WriteString(strings.Repeat(" ", pad))
	}
	buf.WriteString("\033[0m")

	// --- Input line (last row) ---
	prompt := "> "
	inputStr := string(w.input)
	maxInput := w.cols - len(prompt)

	// If input overflows, show the rightmost portion.
	displayInput := inputStr
	runeCount := utf8.RuneCountInString(displayInput)
	if runeCount > maxInput && maxInput > 0 {
		runes := []rune(displayInput)
		displayInput = string(runes[len(runes)-maxInput:])
		runeCount = maxInput
	}
	fmt.Fprintf(&buf, "\033[%d;1H\033[2K", w.rows)
	fmt.Fprintf(&buf, "\033[36m%s\033[0m%s", prompt, displayInput)

	// Position cursor at end of input.
	cursorCol := len(prompt) + runeCount + 1
	if cursorCol > w.cols {
		cursorCol = w.cols
	}
	fmt.Fprintf(&buf, "\033[%d;%dH", w.rows, cursorCol)

	// Ensure cursor is visible.
	buf.WriteString("\033[?25h")

	os.Stdout.Write(buf.Bytes())
}

// watchResize handles SIGWINCH by updating the child PTY size and redrawing.
func (w *wrapper) watchResize(sigCh <-chan os.Signal) {
	for range sigCh {
		fd := int(os.Stdin.Fd())
		cols, rows, err := term.GetSize(fd)
		if err != nil || rows < 3 {
			continue
		}

		w.mu.Lock()
		w.rows = rows
		w.cols = cols
		w.vt.Resize(rows-2, cols)
		pty.Setsize(w.ptm, &pty.Winsize{
			Rows: uint16(rows - 2),
			Cols: uint16(cols),
		})
		os.Stdout.WriteString("\033[2J")
		w.renderScreen()
		w.renderBar()
		w.mu.Unlock()
	}
}
