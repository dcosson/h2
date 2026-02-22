package virtualterminal

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/muesli/termenv"
)

// ColorToX11 converts a termenv.Color to X11 rgb: format.
func ColorToX11(c termenv.Color) string {
	if c == nil {
		return ""
	}
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
	rgb := termenv.ConvertToRGB(c)
	r := uint8(rgb.R*255 + 0.5)
	g := uint8(rgb.G*255 + 0.5)
	b := uint8(rgb.B*255 + 0.5)
	return fmt.Sprintf("rgb:%04x/%04x/%04x", uint16(r)*0x101, uint16(g)*0x101, uint16(b)*0x101)
}

// IsEscSequenceComplete reports whether the given escape sequence is complete.
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

// IsShiftEnterSequence reports whether the escape sequence represents Shift+Enter.
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

// IsCtrlEnterSequence reports whether the escape sequence represents Ctrl+Enter.
// Matches kitty format (ESC[13;5u) and xterm format (ESC[27;5;13~).
func IsCtrlEnterSequence(seq []byte) bool {
	if len(seq) < 3 || seq[1] != '[' {
		return false
	}
	final := seq[len(seq)-1]
	params := string(seq[2 : len(seq)-1])
	switch final {
	case 'u':
		return params == "13;5"
	case '~':
		return params == "27;5;13"
	default:
		return false
	}
}

// IsCtrlEscapeSequence reports whether the escape sequence represents Ctrl+Escape.
// Matches kitty format (ESC[27;5u) and xterm format (ESC[27;5;27~).
func IsCtrlEscapeSequence(seq []byte) bool {
	if len(seq) < 3 || seq[1] != '[' {
		return false
	}
	final := seq[len(seq)-1]
	params := string(seq[2 : len(seq)-1])
	switch final {
	case 'u':
		return params == "27;5"
	case '~':
		return params == "27;5;27"
	default:
		return false
	}
}

// IsTruthyEnv reports whether the environment variable is set to a truthy value.
func IsTruthyEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

// FormatDebugKey formats a single byte for debug display.
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

// TrimLeftToWidth trims a string from the left to fit within the given width.
func TrimLeftToWidth(s string, width int) string {
	if width <= 0 || len(s) <= width {
		return s
	}
	start := len(s) - width
	return s[start:]
}

// FormatIdleDuration formats a duration into a compact human-readable string.
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

// FallbackOSCPalette returns OSC 10/11-compatible X11 rgb values derived from
// COLORFGBG. When parsing fails, it defaults to a dark terminal palette.
func FallbackOSCPalette(colorfgbg string) (fg, bg string) {
	// Most shells/terminals encode COLORFGBG as "<fg>;<bg>" and may append
	// extra fields. Prefer the second field as background when available.
	parts := strings.Split(strings.TrimSpace(colorfgbg), ";")
	bgDark := true
	bgField := ""
	if len(parts) >= 2 {
		bgField = strings.TrimSpace(parts[1])
	} else if len(parts) == 1 {
		bgField = strings.TrimSpace(parts[0])
	}
	if bgField != "" {
		if idx, err := strconv.Atoi(bgField); err == nil {
			// xterm 16-color convention: 0-7 are dark colors, 8-15 are bright.
			bgDark = idx < 8
		}
	}

	if bgDark {
		return "rgb:ffff/ffff/ffff", "rgb:0000/0000/0000"
	}
	return "rgb:0000/0000/0000", "rgb:ffff/ffff/ffff"
}
