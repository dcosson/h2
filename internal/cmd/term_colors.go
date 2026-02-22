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
}

// detectTerminalColorHints captures current terminal colors for OSC 10/11
// responses and a COLORFGBG hint for fallback palette selection.
func detectTerminalColorHints() (oscFg, oscBg, colorfgbg string) {
	// Explicit overrides win.
	overrideFg := os.Getenv("H2_OSC_FG")
	overrideBg := os.Getenv("H2_OSC_BG")
	overrideColorFGBG := os.Getenv("H2_COLORFGBG")

	if term.IsTerminal(int(os.Stdout.Fd())) {
		output := termenv.NewOutput(os.Stdout)
		if fg := output.ForegroundColor(); fg != nil {
			oscFg = virtualterminal.ColorToX11(fg)
		}
		if bg := output.BackgroundColor(); bg != nil {
			oscBg = virtualterminal.ColorToX11(bg)
		}

		colorfgbg = os.Getenv("COLORFGBG")
		if colorfgbg == "" {
			// Keep a simple, widely used fallback format when COLORFGBG is unset.
			if output.HasDarkBackground() {
				colorfgbg = "15;0"
			} else {
				colorfgbg = "0;15"
			}
		}

		_ = persistTerminalColorHints(terminalColorHints{
			OscFg:     oscFg,
			OscBg:     oscBg,
			ColorFGBG: colorfgbg,
		})
	} else if cached, ok := loadTerminalColorHints(); ok {
		oscFg = cached.OscFg
		oscBg = cached.OscBg
		colorfgbg = cached.ColorFGBG
	}

	if colorfgbg == "" {
		colorfgbg = os.Getenv("COLORFGBG")
	}

	if overrideFg != "" {
		oscFg = overrideFg
	}
	if overrideBg != "" {
		oscBg = overrideBg
	}
	if overrideColorFGBG != "" {
		colorfgbg = overrideColorFGBG
	}

	return oscFg, oscBg, colorfgbg
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
