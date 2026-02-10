package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSendCmd_SelfSendBlocked(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, ".h2", "sockets"), 0o700)
	t.Setenv("HOME", tmpDir)
	t.Setenv("H2_ACTOR", "test-agent")

	cmd := newSendCmd()
	cmd.SetArgs([]string{"test-agent", "hello"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when sending to self, got nil")
	}
	if got := err.Error(); got != "cannot send a message to yourself (test-agent); use --allow-self to override" {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestCleanLLMEscapes(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`Hello\!`, `Hello!`},
		{`What\?`, `What?`},
		{`Done\! This is great\!`, `Done! This is great!`},
		{`no escapes here`, `no escapes here`},
		{`keep \\n newline`, `keep \\n newline`},
		{`keep \\t tab`, `keep \\t tab`},
		{`trailing backslash\`, `trailing backslash\`},
		{`\(parens\)`, `(parens)`},
		{`price is \$10`, `price is $10`},
		{`mixed \! and \\n`, `mixed ! and \\n`},
		// Double-escaped (Bash tool doubles backslashes)
		{`Hello\\!`, `Hello!`},
		{`Done\\! Great\\!`, `Done! Great!`},
		// Triple backslash
		{`Hello\\\!`, `Hello!`},
		{``, ``},
	}
	for _, tt := range tests {
		got := cleanLLMEscapes(tt.input)
		if got != tt.want {
			t.Errorf("cleanLLMEscapes(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSendCmd_SelfSendAllowedWithFlag(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, ".h2", "sockets"), 0o700)
	t.Setenv("HOME", tmpDir)
	t.Setenv("H2_ACTOR", "test-agent")

	cmd := newSendCmd()
	cmd.SetArgs([]string{"test-agent", "--allow-self", "hello"})

	err := cmd.Execute()
	// With --allow-self, it should get past the self-check and fail on
	// socket lookup instead (no agent running in test).
	if err == nil {
		t.Fatal("expected socket error, got nil")
	}
	// Should NOT be the self-send error
	if got := err.Error(); got == "cannot send a message to yourself (test-agent); use --allow-self to override" {
		t.Fatal("--allow-self flag did not bypass self-send check")
	}
}
