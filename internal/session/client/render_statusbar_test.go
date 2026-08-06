package client

import (
	"strings"
	"testing"

	"h2/internal/session/agent/monitor"
)

// newStatusBarTestClient builds a client whose status bar has every section
// populated: mode, status, working dir, role/profile, tokens, help, and agent
// name.
func newStatusBarTestClient(t *testing.T, cols int) *Client {
	t.Helper()
	t.Setenv("H2_DIR", "/Users/x")
	o := newTestClient(10, cols)
	o.AgentName = "mild-star"
	o.WorkingDir = func() string { return "/Users/x/proj/sub" }
	o.RoleProfile = func() (string, string) { return "coding", "alt1" }
	o.OtelMetrics = func() (int64, int64, float64, bool, int) {
		return 1000, 2000, 0.12, true, 0
	}
	return o
}

type statusBarExpected struct {
	tokens        string
	help          string
	roleProfile   string
	full          string
	withoutTokens string
	withoutHelp   string
	withoutAgent  string
	withoutMode   string
	withoutStatus string
	workingDir    string
	right         string
}

func statusBarParts() statusBarExpected {
	tokens := monitor.FormatTokens(1000) + "/" + monitor.FormatTokens(2000) + " " + monitor.FormatCost(0.12)
	help := keybindingHelpText[KeybindingsLegacy].NormalMode
	roleProfile := "coding [alt1]"
	withoutAgent := " Normal | Active | proj/sub | " + roleProfile
	return statusBarExpected{
		tokens:        tokens,
		help:          help,
		roleProfile:   roleProfile,
		full:          withoutAgent + " | " + tokens + " | " + help,
		withoutTokens: withoutAgent + " | " + help,
		withoutHelp:   withoutAgent,
		withoutAgent:  withoutAgent,
		withoutMode:   " Active | proj/sub | " + roleProfile,
		withoutStatus: " proj/sub | " + roleProfile,
		workingDir:    " proj/sub",
		right:         "mild-star ",
	}
}

func TestFitStatusBarSections_AllFitInVisualOrder(t *testing.T) {
	p := statusBarParts()
	o := newStatusBarTestClient(t, len(p.full)+len(p.right))
	label, right := o.fitStatusBarSections()
	if label != p.full || right != p.right {
		t.Fatalf("got (%q, %q), want (%q, %q)", label, right, p.full, p.right)
	}
}

func TestFitStatusBarSections_DropOrder(t *testing.T) {
	p := statusBarParts()
	tests := []struct {
		name      string
		cols      int
		wantLabel string
		wantRight string
	}{
		{"tokens first", len(p.full) + len(p.right) - 1, p.withoutTokens, p.right},
		{"help second", len(p.withoutTokens) + len(p.right) - 1, p.withoutHelp, p.right},
		{"agent name third", len(p.withoutHelp) + len(p.right) - 1, p.withoutAgent, ""},
		{"mode fourth", len(p.withoutAgent) - 1, p.withoutMode, ""},
		{"status fifth", len(p.withoutMode) - 1, p.withoutStatus, ""},
		{"role profile sixth", len(p.withoutStatus) - 1, p.workingDir, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := newStatusBarTestClient(t, tt.cols)
			label, right := o.fitStatusBarSections()
			if label != tt.wantLabel || right != tt.wantRight {
				t.Fatalf("got (%q, %q), want (%q, %q)", label, right, tt.wantLabel, tt.wantRight)
			}
		})
	}
}

func TestFitStatusBarSections_TruncatesWorkingDirAsLastResort(t *testing.T) {
	o := newStatusBarTestClient(t, 3)
	label, right := o.fitStatusBarSections()
	if label != " pr" || right != "" {
		t.Fatalf("got (%q, %q), want (%q, empty)", label, right, " pr")
	}
}

// Sweep every width and verify both fit and retention priority. Visual order
// remains mode, status, working dir, role/profile, tokens, help; retention
// priority is working dir, role/profile, status, mode, then everything else.
func TestFitStatusBarSections_SweepRetentionPriority(t *testing.T) {
	p := statusBarParts()
	for cols := 1; cols <= len(p.full)+len(p.right)+5; cols++ {
		o := newStatusBarTestClient(t, cols)
		label, right := o.fitStatusBarSections()
		if len(label)+len(right) > cols {
			t.Fatalf("cols=%d: label %q + right %q overflows", cols, label, right)
		}
		has := func(s string) bool { return strings.Contains(label, s) }
		if has(p.tokens) && (!has(p.help) || right == "") {
			t.Fatalf("cols=%d: tokens retained after another low-priority section was dropped: %q / %q", cols, label, right)
		}
		if has(p.help) && right == "" {
			t.Fatalf("cols=%d: help retained after agent name was dropped: %q", cols, label)
		}
		if right != "" && !has("Normal") {
			t.Fatalf("cols=%d: agent name retained after mode was dropped: %q", cols, label)
		}
		if has("Normal") && !has("Active") {
			t.Fatalf("cols=%d: mode retained after status was dropped: %q", cols, label)
		}
		if has("Active") && !has(p.roleProfile) {
			t.Fatalf("cols=%d: status retained after role/profile was dropped: %q", cols, label)
		}
		if has(p.roleProfile) && !has("proj/sub") {
			t.Fatalf("cols=%d: role/profile retained after working dir was dropped: %q", cols, label)
		}
	}
}

func TestFitStatusBarSections_OmitsEmptyRoleAndProfile(t *testing.T) {
	p := statusBarParts()
	o := newStatusBarTestClient(t, len(p.full)+len(p.right))
	o.RoleProfile = func() (string, string) { return "", "" }
	label, _ := o.fitStatusBarSections()
	if strings.Contains(label, "[]") || strings.Contains(label, "  |") {
		t.Fatalf("empty role/profile produced an empty section: %q", label)
	}
}

func TestFitStatusBarSections_MenuModeDropsHelpAndAgentKeepsMenuItems(t *testing.T) {
	menuLabel := " Menu | p:passthrough | c:clear | r:redraw | q:quit"
	o := newStatusBarTestClient(t, len(menuLabel)+5)
	o.Mode = ModeMenu
	label, right := o.fitStatusBarSections()
	if label != menuLabel || right != "" {
		t.Fatalf("got (%q, %q), want (%q, empty)", label, right, menuLabel)
	}
}

func TestFitStatusBarSections_MenuModeTruncatesMenuItemsAsLastResort(t *testing.T) {
	o := newStatusBarTestClient(t, 8)
	o.Mode = ModeMenu
	label, right := o.fitStatusBarSections()
	if label != " Menu | " || right != "" {
		t.Fatalf("got (%q, %q), want (%q, empty)", label, right, " Menu | ")
	}
}
