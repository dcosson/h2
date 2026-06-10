package client

import (
	"strings"
	"testing"

	"h2/internal/session/agent/monitor"
)

// newStatusBarTestClient builds a client whose status bar has every section
// populated: mode, status, working dir, tokens, help, and agent name.
func newStatusBarTestClient(t *testing.T, cols int) *Client {
	t.Helper()
	t.Setenv("H2_DIR", "/Users/x")
	o := newTestClient(10, cols)
	o.AgentName = "mild-star"
	o.WorkingDir = func() string { return "/Users/x/proj/sub" }
	o.OtelMetrics = func() (int64, int64, float64, bool, int) {
		return 1000, 2000, 0.12, true, 0
	}
	return o
}

// statusBarParts returns the expected text of each section, matching the
// values configured by newStatusBarTestClient.
func statusBarParts() (tokens, help, full, withoutTokens, withoutHelp, withoutMode, withoutWD, right string) {
	tokens = monitor.FormatTokens(1000) + "/" + monitor.FormatTokens(2000) + " " + monitor.FormatCost(0.12)
	help = keybindingHelpText[KeybindingsLegacy].NormalMode
	full = " Normal | Active | proj/sub | " + tokens + " | " + help
	withoutTokens = " Normal | Active | proj/sub | " + help
	withoutHelp = " Normal | Active | proj/sub"
	withoutMode = " Active | proj/sub"
	withoutWD = " Active"
	right = "mild-star "
	return
}

func TestFitStatusBarSections_AllFit(t *testing.T) {
	_, _, full, _, _, _, _, right := statusBarParts()
	o := newStatusBarTestClient(t, len(full)+len(right))
	label, gotRight := o.fitStatusBarSections()
	if label != full {
		t.Fatalf("label = %q, want %q", label, full)
	}
	if gotRight != right {
		t.Fatalf("right = %q, want %q", gotRight, right)
	}
}

func TestFitStatusBarSections_DropsTokensFirst(t *testing.T) {
	_, _, full, withoutTokens, _, _, _, right := statusBarParts()
	o := newStatusBarTestClient(t, len(full)+len(right)-1)
	label, gotRight := o.fitStatusBarSections()
	if label != withoutTokens {
		t.Fatalf("label = %q, want %q", label, withoutTokens)
	}
	if gotRight != right {
		t.Fatalf("right = %q, want %q", gotRight, right)
	}
}

func TestFitStatusBarSections_DropsHelpSecond(t *testing.T) {
	_, _, _, withoutTokens, withoutHelp, _, _, right := statusBarParts()
	o := newStatusBarTestClient(t, len(withoutTokens)+len(right)-1)
	label, gotRight := o.fitStatusBarSections()
	if label != withoutHelp {
		t.Fatalf("label = %q, want %q", label, withoutHelp)
	}
	if gotRight != right {
		t.Fatalf("right = %q, want %q", gotRight, right)
	}
}

func TestFitStatusBarSections_DropsModeThird(t *testing.T) {
	_, _, _, _, withoutHelp, withoutMode, _, right := statusBarParts()
	o := newStatusBarTestClient(t, len(withoutHelp)+len(right)-1)
	label, gotRight := o.fitStatusBarSections()
	if label != withoutMode {
		t.Fatalf("label = %q, want %q", label, withoutMode)
	}
	if gotRight != right {
		t.Fatalf("right = %q, want %q", gotRight, right)
	}
}

func TestFitStatusBarSections_DropsAgentNameFourth(t *testing.T) {
	_, _, _, _, _, withoutMode, _, right := statusBarParts()
	o := newStatusBarTestClient(t, len(withoutMode)+len(right)-1)
	label, gotRight := o.fitStatusBarSections()
	if label != withoutMode {
		t.Fatalf("label = %q, want %q", label, withoutMode)
	}
	if gotRight != "" {
		t.Fatalf("right = %q, want empty", gotRight)
	}
}

func TestFitStatusBarSections_DropsWorkingDirFifth(t *testing.T) {
	_, _, _, _, _, withoutMode, withoutWD, _ := statusBarParts()
	o := newStatusBarTestClient(t, len(withoutMode)-1)
	label, gotRight := o.fitStatusBarSections()
	if label != withoutWD {
		t.Fatalf("label = %q, want %q", label, withoutWD)
	}
	if gotRight != "" {
		t.Fatalf("right = %q, want empty", gotRight)
	}
}

func TestFitStatusBarSections_TruncatesStatusAsLastResort(t *testing.T) {
	o := newStatusBarTestClient(t, 3)
	label, gotRight := o.fitStatusBarSections()
	if label != " Ac" {
		t.Fatalf("label = %q, want %q", label, " Ac")
	}
	if gotRight != "" {
		t.Fatalf("right = %q, want empty", gotRight)
	}
}

// Sweep every width and check the invariants of the drop order: the bar
// always fits, and a section is only missing if every earlier-dropped
// section is missing too (drop order: tokens, help, mode, agent, dir).
func TestFitStatusBarSections_SweepInvariants(t *testing.T) {
	tokens, help, full, _, _, _, _, fullRight := statusBarParts()
	for cols := 1; cols <= len(full)+len(fullRight)+5; cols++ {
		o := newStatusBarTestClient(t, cols)
		label, right := o.fitStatusBarSections()
		if len(label)+len(right) > cols {
			t.Fatalf("cols=%d: label %q + right %q overflows", cols, label, right)
		}
		has := func(s string) bool { return strings.Contains(label, s) }
		if has(tokens) && !has(help) {
			t.Fatalf("cols=%d: tokens kept but help dropped: %q", cols, label)
		}
		if has(help) && !has("Normal") {
			t.Fatalf("cols=%d: help kept but mode dropped: %q", cols, label)
		}
		if has("Normal") && right == "" {
			t.Fatalf("cols=%d: mode kept but agent name dropped: %q", cols, label)
		}
		if right != "" && !has("proj/sub") {
			t.Fatalf("cols=%d: agent name kept but working dir dropped: %q", cols, label)
		}
		if has("proj/sub") && !has("Active") {
			t.Fatalf("cols=%d: working dir kept but status dropped: %q", cols, label)
		}
	}
}

func TestFitStatusBarSections_MenuModeDropsHelpAndAgentKeepsMenuItems(t *testing.T) {
	menuLabel := " Menu | p:passthrough | c:clear | r:redraw | q:quit"
	o := newStatusBarTestClient(t, len(menuLabel)+5)
	o.Mode = ModeMenu
	label, right := o.fitStatusBarSections()
	if label != menuLabel {
		t.Fatalf("label = %q, want %q", label, menuLabel)
	}
	if right != "" {
		t.Fatalf("right = %q, want empty", right)
	}
}

func TestFitStatusBarSections_MenuModeTruncatesMenuItemsAsLastResort(t *testing.T) {
	o := newStatusBarTestClient(t, 8)
	o.Mode = ModeMenu
	label, right := o.fitStatusBarSections()
	if label != " Menu | " {
		t.Fatalf("label = %q, want %q", label, " Menu | ")
	}
	if right != "" {
		t.Fatalf("right = %q, want empty", right)
	}
}
