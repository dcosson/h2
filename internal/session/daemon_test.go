package session

import (
	"testing"
)

func TestRunDaemonOpts_InstructionsStoredOnSession(t *testing.T) {
	// Verify that RunDaemon stores Instructions on the Session.
	// We can't call RunDaemon directly (it starts sockets/PTY), but we can
	// verify the field threading by constructing the same way RunDaemon does.
	opts := RunDaemonOpts{
		Name:         "test-agent",
		SessionID:    "test-uuid",
		Command:      "claude",
		Instructions: "You are a test agent.\nDo test things.",
	}

	s := New(opts.Name, opts.Command, opts.Args)
	s.SessionID = opts.SessionID
	s.Instructions = opts.Instructions

	if s.Instructions != "You are a test agent.\nDo test things." {
		t.Fatalf("Instructions not stored on session: got %q", s.Instructions)
	}

	// Verify childArgs includes --append-system-prompt.
	args := s.childArgs()
	found := false
	for i, arg := range args {
		if arg == "--append-system-prompt" && i+1 < len(args) {
			found = true
			if args[i+1] != opts.Instructions {
				t.Fatalf("--append-system-prompt value = %q, want %q", args[i+1], opts.Instructions)
			}
		}
	}
	if !found {
		t.Fatal("childArgs should include --append-system-prompt when Instructions is set")
	}
}

func TestRunDaemonOpts_EmptyInstructionsNotStoredOnSession(t *testing.T) {
	opts := RunDaemonOpts{
		Name:         "test-agent",
		SessionID:    "test-uuid",
		Command:      "claude",
		Instructions: "",
	}

	s := New(opts.Name, opts.Command, opts.Args)
	s.SessionID = opts.SessionID
	s.Instructions = opts.Instructions

	// Verify childArgs does NOT include --append-system-prompt.
	args := s.childArgs()
	for _, arg := range args {
		if arg == "--append-system-prompt" {
			t.Fatal("childArgs should NOT include --append-system-prompt when Instructions is empty")
		}
	}
}

func TestForkDaemonOpts_InstructionsField(t *testing.T) {
	// Verify ForkDaemonOpts can carry instructions to be passed as --instructions flag.
	opts := ForkDaemonOpts{
		Name:         "test-agent",
		SessionID:    "test-uuid",
		Command:      "claude",
		Instructions: "Multi-line\ninstructions\nwith special chars: $VAR `code`",
	}

	if opts.Instructions != "Multi-line\ninstructions\nwith special chars: $VAR `code`" {
		t.Fatalf("ForkDaemonOpts.Instructions not preserved: got %q", opts.Instructions)
	}
}
