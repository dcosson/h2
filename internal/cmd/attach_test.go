package cmd

import (
	"strings"
	"testing"

	"h2/internal/config"
)

func TestAttachResumeFromSessionID_NoSession(t *testing.T) {
	setupFakeHomeForResume(t)

	cmd := newAttachCmd()
	cmd.SetArgs([]string{"--resume-from-session-id", "unknown-id"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown harness session id")
	}
	if !strings.Contains(err.Error(), `no running session with id "unknown-id"`) {
		t.Errorf("error = %q, want containing 'no running session with id \"unknown-id\"'", err.Error())
	}
}

func TestAttachResumeFromSessionID_SessionExistsButNotRunning(t *testing.T) {
	setupFakeHomeForResume(t)

	name := "dead-codex-agent"
	writeTestRuntimeConfig(t, name, &config.RuntimeConfig{
		AgentName:        name,
		SessionID:        "internal-id",
		HarnessSessionID: "codex-conv-dead",
		Command:          "codex",
		HarnessType:      "codex",
		CWD:              t.TempDir(),
		StartedAt:        "2024-01-01T00:00:00Z",
	})

	cmd := newAttachCmd()
	cmd.SetArgs([]string{"--resume-from-session-id", "codex-conv-dead"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error attaching to a session whose daemon is not running")
	}
	// Points the user at the resume command.
	if !strings.Contains(err.Error(), "is not running") {
		t.Errorf("error = %q, want containing 'is not running'", err.Error())
	}
	if !strings.Contains(err.Error(), "h2 run --resume-from-session-id codex-conv-dead") {
		t.Errorf("error = %q, want containing resume hint command", err.Error())
	}
}

func TestAttachResumeFromSessionID_RejectsNameArg(t *testing.T) {
	setupFakeHomeForResume(t)

	cmd := newAttachCmd()
	cmd.SetArgs([]string{"some-agent", "--resume-from-session-id", "abc-123"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for name arg with --resume-from-session-id")
	}
	if !strings.Contains(err.Error(), "does not take an agent name argument") {
		t.Errorf("error = %q, want containing 'does not take an agent name argument'", err.Error())
	}
}

func TestAttach_RequiresNameWithoutFlag(t *testing.T) {
	setupFakeHomeForResume(t)

	cmd := newAttachCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for attach with no name and no flag")
	}
	if !strings.Contains(err.Error(), "requires an agent name") {
		t.Errorf("error = %q, want containing 'requires an agent name'", err.Error())
	}
}
