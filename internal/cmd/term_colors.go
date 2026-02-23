package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/muesli/termenv"
	"golang.org/x/term"

	"h2/internal/config"
	"h2/internal/session/virtualterminal"
)

type terminalColorHints struct {
	OscFg     string `json:"osc_fg,omitempty"`
	OscBg     string `json:"osc_bg,omitempty"`
	ColorFGBG string `json:"colorfgbg,omitempty"`
	Term      string `json:"term,omitempty"`
	ColorTerm string `json:"colorterm,omitempty"`
}

// detectTerminalColorHints captures current terminal colors for OSC 10/11
// responses, a COLORFGBG hint for fallback palette selection, and TERM/COLORTERM
// for terminal capability detection.
func detectTerminalColorHints() terminalColorHints {
	var hints terminalColorHints

	// Explicit overrides win (applied at the end).
	overrideFg := os.Getenv("H2_OSC_FG")
	overrideBg := os.Getenv("H2_OSC_BG")
	overrideColorFGBG := os.Getenv("H2_COLORFGBG")

	if term.IsTerminal(int(os.Stdout.Fd())) {
		output := termenv.NewOutput(os.Stdout)
		if fg := output.ForegroundColor(); fg != nil {
			hints.OscFg = virtualterminal.ColorToX11(fg)
		}
		if bg := output.BackgroundColor(); bg != nil {
			hints.OscBg = virtualterminal.ColorToX11(bg)
		}

		hints.ColorFGBG = os.Getenv("COLORFGBG")
		if hints.ColorFGBG == "" {
			// Keep a simple, widely used fallback format when COLORFGBG is unset.
			if output.HasDarkBackground() {
				hints.ColorFGBG = "15;0"
			} else {
				hints.ColorFGBG = "0;15"
			}
		}

		hints.Term = os.Getenv("TERM")
		hints.ColorTerm = os.Getenv("COLORTERM")

		_ = persistTerminalColorHints(hints)
	} else if cached, ok := loadTerminalColorHints(); ok {
		hints = cached
	}

	if hints.ColorFGBG == "" {
		hints.ColorFGBG = os.Getenv("COLORFGBG")
	}

	if overrideFg != "" {
		hints.OscFg = overrideFg
	}
	if overrideBg != "" {
		hints.OscBg = overrideBg
	}
	if overrideColorFGBG != "" {
		hints.ColorFGBG = overrideColorFGBG
	}

	return hints
}

// refreshTerminalColorHintsCache updates terminal color hints on disk when this
// process has a TTY. Non-TTY invocations are a no-op.
func refreshTerminalColorHintsCache() {
	if term.IsTerminal(int(os.Stdout.Fd())) {
		detectTerminalColorHints()
	}
}

func terminalColorHintsPath() (string, error) {
	root, err := config.RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "terminal-colors.json"), nil
}

func persistTerminalColorHints(h terminalColorHints) error {
	path, err := terminalColorHintsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func loadTerminalColorHints() (terminalColorHints, bool) {
	path, err := terminalColorHintsPath()
	if err != nil {
		return terminalColorHints{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return terminalColorHints{}, false
	}
	var h terminalColorHints
	if err := json.Unmarshal(data, &h); err != nil {
		return terminalColorHints{}, false
	}
	return h, true
}
