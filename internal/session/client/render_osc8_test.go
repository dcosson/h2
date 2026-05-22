package client

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/vito/midterm"

	"h2/internal/session/virtualterminal"
)

// osc8 builds the OSC 8 open sequence for uri (empty closes the link).
func osc8(uri string) string {
	return fmt.Sprintf("\x1b]8;;%s\x1b\\", uri)
}

const osc8Close = "\x1b]8;;\x1b\\"

func TestRenderLineFrom_EmitsOSC8AroundHyperlink(t *testing.T) {
	c := newTestClient(2, 20)
	fmt.Fprintf(c.VT.Vt, "%shello%s world", osc8("https://example.com"), osc8(""))

	var buf bytes.Buffer
	c.RenderLineFrom(&buf, c.VT.Vt, 0)
	got := buf.String()

	openSeq := osc8("https://example.com")
	if !strings.Contains(got, openSeq) {
		t.Fatalf("missing OSC 8 open sequence: %q", got)
	}
	if !strings.Contains(got, osc8Close) {
		t.Fatalf("missing OSC 8 close sequence: %q", got)
	}
	if strings.Index(got, openSeq) > strings.Index(got, "hello") {
		t.Fatalf("OSC 8 open should precede 'hello': %q", got)
	}
	// The close must come after "hello" but before the trailing " world", so
	// that " world" is unlinked.
	closeIdx := strings.Index(got, osc8Close)
	helloIdx := strings.Index(got, "hello")
	worldIdx := strings.Index(got, " world")
	if !(helloIdx < closeIdx && closeIdx < worldIdx) {
		t.Fatalf("OSC 8 close should sit between hello and world: %q", got)
	}
}

func TestRenderLineFrom_NoOSC8ForUnlinkedRow(t *testing.T) {
	c := newTestClient(2, 20)
	fmt.Fprint(c.VT.Vt, "just text")

	var buf bytes.Buffer
	c.RenderLineFrom(&buf, c.VT.Vt, 0)
	got := buf.String()

	if strings.Contains(got, "\x1b]8;") {
		t.Fatalf("unexpected OSC 8 sequence in unlinked row: %q", got)
	}
}

func TestRenderLineFrom_ClosesBetweenAdjacentLinks(t *testing.T) {
	// Two back-to-back links with no gap: a close must precede the second
	// open so the outer terminal sees clean URL transitions.
	c := newTestClient(2, 20)
	fmt.Fprintf(c.VT.Vt, "%sa%s%sb%s",
		osc8("https://x"), osc8(""),
		osc8("https://y"), osc8(""))

	var buf bytes.Buffer
	c.RenderLineFrom(&buf, c.VT.Vt, 0)
	got := buf.String()

	openX := osc8("https://x")
	openY := osc8("https://y")
	idxOpenX := strings.Index(got, openX)
	idxOpenY := strings.Index(got, openY)
	if idxOpenX < 0 || idxOpenY < 0 {
		t.Fatalf("both opens should appear: %q", got)
	}
	// Between the two opens there must be at least one close.
	between := got[idxOpenX:idxOpenY]
	if !strings.Contains(between, osc8Close) {
		t.Fatalf("expected a close between adjacent links: %q", got)
	}
}

func TestRenderHistoryEntry_EmitsOSC8(t *testing.T) {
	c := newTestClient(2, 20)
	entry := virtualterminal.ScrollHistoryEntry{
		Content: []rune("foobar"),
		Runs: []virtualterminal.FormatRun{
			{Size: 3, Format: midterm.Format{}, URL: "https://example.com"},
			{Size: 3, Format: midterm.Format{}, URL: ""},
		},
	}
	var buf bytes.Buffer
	c.renderHistoryEntry(&buf, entry)
	got := buf.String()

	openSeq := osc8("https://example.com")
	if !strings.Contains(got, openSeq) {
		t.Fatalf("missing OSC 8 open: %q", got)
	}
	if !strings.Contains(got, osc8Close) {
		t.Fatalf("missing OSC 8 close: %q", got)
	}
	// Close must appear between "foo" (linked) and "bar" (unlinked).
	idxFoo := strings.Index(got, "foo")
	idxClose := strings.Index(got, osc8Close)
	idxBar := strings.Index(got, "bar")
	if !(idxFoo < idxClose && idxClose < idxBar) {
		t.Fatalf("close should sit between foo and bar: %q", got)
	}
}

// Note: midterm's OSC parser uses ESC and BEL as OSC terminators, so it
// truncates URIs at those bytes before they ever reach our URL table — making
// the live render path naturally safe against the most obvious injection. The
// renderer's own sanitizer is defense in depth against future parser changes,
// and the history path (which can carry arbitrary URL strings via FormatRun)
// is where we exercise it end-to-end below.

func TestRenderHistoryEntry_RejectsMaliciousURI(t *testing.T) {
	c := newTestClient(2, 20)
	bad := "https://evil\x07injected"
	entry := virtualterminal.ScrollHistoryEntry{
		Content: []rune("hi"),
		Runs: []virtualterminal.FormatRun{
			{Size: 2, Format: midterm.Format{}, URL: bad},
		},
	}
	var buf bytes.Buffer
	c.renderHistoryEntry(&buf, entry)
	got := buf.String()
	if strings.Contains(got, bad) || strings.Contains(got, "injected") {
		t.Fatalf("malicious URI leaked through history render: %q", got)
	}
	if strings.Contains(got, "\x1b]8;;https") {
		t.Fatalf("rejected URI must not produce an OSC 8 open: %q", got)
	}
}

func TestSanitizeOSC8URL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"https://example.com", "https://example.com"},
		{"https://example.com/a?b=c&d=e", "https://example.com/a?b=c&d=e"},
		{"https://example.com/\x1bevil", ""},               // ESC
		{"https://example.com/\x07bell", ""},               // BEL
		{"https://example.com/\nnewline", ""},              // LF
		{"https://example.com/\x00null", ""},               // NUL
		{"https://example.com/\x7fdel", ""},                // DEL
		{"https://example.com/π", "https://example.com/π"}, // UTF-8 OK
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := sanitizeOSC8URL(tc.in)
			if got != tc.want {
				t.Fatalf("sanitizeOSC8URL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRenderHistoryEntry_NoOSC8WhenAllRunsUnlinked(t *testing.T) {
	c := newTestClient(2, 20)
	entry := virtualterminal.ScrollHistoryEntry{
		Content: []rune("plain"),
		Runs: []virtualterminal.FormatRun{
			{Size: 5, Format: midterm.Format{}, URL: ""},
		},
	}
	var buf bytes.Buffer
	c.renderHistoryEntry(&buf, entry)
	got := buf.String()
	if strings.Contains(got, "\x1b]8;") {
		t.Fatalf("unexpected OSC 8 in unlinked entry: %q", got)
	}
}
