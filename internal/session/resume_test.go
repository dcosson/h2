package session

import (
	"errors"
	"strings"
	"testing"

	"h2/internal/config"
)

func TestResumeSession_NoSession(t *testing.T) {
	setupForkTestH2Dir(t)
	err := ResumeSession("no-such-agent", TerminalHints{}, func(string, TerminalHints, bool) error {
		t.Fatal("forkDaemon should not be called")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "no session found") {
		t.Errorf("err = %v, want 'no session found'", err)
	}
}

func TestResumeSession_Success(t *testing.T) {
	h2Dir := setupForkTestH2Dir(t)
	rc := writeForkParentSession(t, h2Dir, "resume-me")
	sessionDir := config.SessionDir("resume-me")

	var gotDir string
	var gotResume bool
	err := ResumeSession("resume-me", TerminalHints{}, func(sd string, _ TerminalHints, resume bool) error {
		gotDir = sd
		gotResume = resume
		return nil
	})
	if err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}
	if gotDir != sessionDir || !gotResume {
		t.Errorf("forkDaemon(%q, resume=%v), want (%q, resume=true)", gotDir, gotResume, sessionDir)
	}

	// StartedAt must be updated for the new daemon instance.
	updated, err := config.ReadRuntimeConfig(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if updated.StartedAt == rc.StartedAt {
		t.Error("StartedAt not updated on resume")
	}
}

func TestResumeSession_ForkFailureRestoresStartedAt(t *testing.T) {
	h2Dir := setupForkTestH2Dir(t)
	rc := writeForkParentSession(t, h2Dir, "resume-fail")
	sessionDir := config.SessionDir("resume-fail")

	err := ResumeSession("resume-fail", TerminalHints{}, func(string, TerminalHints, bool) error {
		return errors.New("boom")
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want boom", err)
	}

	restored, err := config.ReadRuntimeConfig(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if restored.StartedAt != rc.StartedAt {
		t.Errorf("StartedAt = %q, want restored to %q", restored.StartedAt, rc.StartedAt)
	}
}

func TestResumeSession_NoHarnessSessionID(t *testing.T) {
	h2Dir := setupForkTestH2Dir(t)
	rc := writeForkParentSession(t, h2Dir, "resume-no-hsid")
	rc.HarnessSessionID = ""
	if err := config.WriteRuntimeConfig(config.SessionDir("resume-no-hsid"), rc); err != nil {
		t.Fatal(err)
	}

	err := ResumeSession("resume-no-hsid", TerminalHints{}, func(string, TerminalHints, bool) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "harness_session_id") {
		t.Errorf("err = %v, want 'harness_session_id'", err)
	}
}
