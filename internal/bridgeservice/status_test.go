package bridgeservice

import (
	"strings"
	"testing"
)

func TestConciergeRouting(t *testing.T) {
	got := conciergeRouting("sage-vale")
	want := "The concierge agent sage-vale will reply to all messages."
	if got != want {
		t.Errorf("conciergeRouting(\"sage-vale\") =\n  %q\nwant:\n  %q", got, want)
	}
}

func TestNoConciergeRouting(t *testing.T) {
	t.Run("no agents", func(t *testing.T) {
		got := noConciergeRouting("")
		want := "No agents are running to receive messages. Create agents with h2 run."
		if got != want {
			t.Errorf("noConciergeRouting(\"\") =\n  %q\nwant:\n  %q", got, want)
		}
	})

	t.Run("with first agent", func(t *testing.T) {
		got := noConciergeRouting("coder-1")
		if got == "" {
			t.Fatal("expected non-empty string")
		}
		// Should mention the agent name and explain fallback routing.
		wantSubstrings := []string{"coder-1", "first agent in the list", "no concierge"}
		for _, sub := range wantSubstrings {
			if !strings.Contains(got, sub) {
				t.Errorf("noConciergeRouting(\"coder-1\") = %q\n  missing substring %q", got, sub)
			}
		}
	})
}

func TestDirectMessagingHint(t *testing.T) {
	got := directMessagingHint()
	if got == "" {
		t.Fatal("expected non-empty string")
	}
	wantSubstrings := []string{"agent name", "replying to their messages"}
	for _, sub := range wantSubstrings {
		if !strings.Contains(got, sub) {
			t.Errorf("directMessagingHint() = %q\n  missing substring %q", got, sub)
		}
	}
}

func TestAllowedCommandsHint(t *testing.T) {
	t.Run("no commands", func(t *testing.T) {
		got := allowedCommandsHint(nil)
		if !strings.Contains(got, "None are configured") {
			t.Errorf("allowedCommandsHint(nil) = %q\n  expected 'None are configured'", got)
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		got := allowedCommandsHint([]string{})
		if !strings.Contains(got, "None are configured") {
			t.Errorf("allowedCommandsHint([]) = %q\n  expected 'None are configured'", got)
		}
	})

	t.Run("single command", func(t *testing.T) {
		got := allowedCommandsHint([]string{"/status"})
		if !strings.Contains(got, "/status") {
			t.Errorf("allowedCommandsHint([\"/status\"]) = %q\n  missing '/status'", got)
		}
	})

	t.Run("multiple commands", func(t *testing.T) {
		got := allowedCommandsHint([]string{"/status", "/help", "/restart"})
		for _, cmd := range []string{"/status", "/help", "/restart"} {
			if !strings.Contains(got, cmd) {
				t.Errorf("allowedCommandsHint(...) = %q\n  missing %q", got, cmd)
			}
		}
		// Should be comma-separated.
		if !strings.Contains(got, "/status, /help, /restart") {
			t.Errorf("allowedCommandsHint(...) = %q\n  expected comma-separated list", got)
		}
	})
}

