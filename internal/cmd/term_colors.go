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

type terminalHints struct {
	OscFg     string `json:"osc_fg,omitempty"`
	OscBg     string `json:"osc_bg,omitempty"`
	ColorFGBG string `json:"colorfgbg,omitempty"`
	Term      string `json:"term,omitempty"`
	ColorTerm string `json:"colorterm,omitempty"`
}

// detectTerminalHints captures current terminal colors for OSC 10/11
// responses, a COLORFGBG hint for fallback palette selection, and TERM/COLORTERM
// for terminal capability detection.
func detectTerminalHints() terminalHints {
	var hints terminalHints

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

		_ = persistTerminalHints(hints)
	} else if cached, ok := loadTerminalHints(); ok {
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

// refreshTerminalHintsCache updates terminal hints on disk when this
// process has a TTY. Non-TTY invocations are a no-op.
func refreshTerminalHintsCache() {
	if term.IsTerminal(int(os.Stdout.Fd())) {
		detectTerminalHints()
	}
}

func terminalHintsPath() (string, error) {
	root, err := config.RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "terminal.json"), nil
}

func persistTerminalHints(h terminalHints) error {
	path, err := terminalHintsPath()
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

func loadTerminalHints() (terminalHints, bool) {
	path, err := terminalHintsPath()
	if err != nil {
		return terminalHints{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// Fall back to legacy file name.
		root, rootErr := config.RootDir()
		if rootErr != nil {
			return terminalHints{}, false
		}
		data, err = os.ReadFile(filepath.Join(root, "terminal-colors.json"))
		if err != nil {
			return terminalHints{}, false
		}
	}
	var h terminalHints
	if err := json.Unmarshal(data, &h); err != nil {
		return terminalHints{}, false
	}
	return h, true
}
