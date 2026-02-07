package cmd

import (
	"testing"
)

func TestSendCmd_SelfSendBlocked(t *testing.T) {
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

func TestSendCmd_SelfSendAllowedWithFlag(t *testing.T) {
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
