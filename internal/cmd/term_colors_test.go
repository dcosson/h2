package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTerminalHints_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "terminal.json")

	original := terminalHints{
		OscFg:     "rgb:ffff/ffff/ffff",
		OscBg:     "rgb:2828/2c2c/3434",
		ColorFGBG: "15;0",
		Term:      "xterm-256color",
		ColorTerm: "truecolor",
	}

	// Persist.
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	// Load.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var loaded terminalHints
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatal(err)
	}

	if loaded.OscFg != original.OscFg {
		t.Errorf("OscFg: got %q, want %q", loaded.OscFg, original.OscFg)
	}
	if loaded.OscBg != original.OscBg {
		t.Errorf("OscBg: got %q, want %q", loaded.OscBg, original.OscBg)
	}
	if loaded.ColorFGBG != original.ColorFGBG {
		t.Errorf("ColorFGBG: got %q, want %q", loaded.ColorFGBG, original.ColorFGBG)
	}
	if loaded.Term != original.Term {
		t.Errorf("Term: got %q, want %q", loaded.Term, original.Term)
	}
	if loaded.ColorTerm != original.ColorTerm {
		t.Errorf("ColorTerm: got %q, want %q", loaded.ColorTerm, original.ColorTerm)
	}
}

func TestTerminalHints_BackwardCompat(t *testing.T) {
	// Old cache files without term/colorterm should load fine (empty strings).
	raw := `{"osc_fg":"rgb:ffff/ffff/ffff","osc_bg":"rgb:0000/0000/0000","colorfgbg":"15;0"}`
	var hints terminalHints
	if err := json.Unmarshal([]byte(raw), &hints); err != nil {
		t.Fatal(err)
	}
	if hints.Term != "" {
		t.Errorf("Term should be empty for old cache, got %q", hints.Term)
	}
	if hints.ColorTerm != "" {
		t.Errorf("ColorTerm should be empty for old cache, got %q", hints.ColorTerm)
	}
	if hints.OscFg != "rgb:ffff/ffff/ffff" {
		t.Errorf("OscFg: got %q", hints.OscFg)
	}
}

func TestTerminalHints_OmitEmpty(t *testing.T) {
	// Fields with empty values should be omitted from JSON.
	hints := terminalHints{
		OscBg:     "rgb:0000/0000/0000",
		ColorFGBG: "15;0",
	}
	data, err := json.Marshal(hints)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if contains(s, "osc_fg") {
		t.Errorf("empty OscFg should be omitted, got: %s", s)
	}
	if contains(s, "term") {
		t.Errorf("empty Term should be omitted, got: %s", s)
	}
	if contains(s, "colorterm") {
		t.Errorf("empty ColorTerm should be omitted, got: %s", s)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstring(s, sub))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
