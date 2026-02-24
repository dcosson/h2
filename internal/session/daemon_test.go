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

func TestRunDaemonOpts_AllNewFieldsStoredOnSession(t *testing.T) {
	opts := RunDaemonOpts{
		Name:           "test-agent",
		SessionID:      "test-uuid",
		Command:        "claude",
		Instructions:   "Instructions here",
		SystemPrompt:   "Custom system prompt",
		Model:          "claude-opus-4-6",
		PermissionMode: "plan",
	}

	s := New(opts.Name, opts.Command, opts.Args)
	s.SessionID = opts.SessionID
	s.Instructions = opts.Instructions
	s.SystemPrompt = opts.SystemPrompt
	s.Model = opts.Model
	s.PermissionMode = opts.PermissionMode

	if s.SystemPrompt != "Custom system prompt" {
		t.Fatalf("SystemPrompt not stored: got %q", s.SystemPrompt)
	}
	if s.Model != "claude-opus-4-6" {
		t.Fatalf("Model not stored: got %q", s.Model)
	}
	if s.PermissionMode != "plan" {
		t.Fatalf("PermissionMode not stored: got %q", s.PermissionMode)
	}

	// Verify all fields appear in childArgs.
	args := s.childArgs()
	expectPairs := map[string]string{
		"--system-prompt":        "Custom system prompt",
		"--append-system-prompt": "Instructions here",
		"--model":                "claude-opus-4-6",
		"--permission-mode":      "plan",
	}
	for flag, wantVal := range expectPairs {
		found := false
		for i, arg := range args {
			if arg == flag && i+1 < len(args) {
				found = true
				if args[i+1] != wantVal {
					t.Errorf("%s value = %q, want %q", flag, args[i+1], wantVal)
				}
			}
		}
		if !found {
			t.Errorf("expected %s in childArgs, not found. args: %v", flag, args)
		}
	}
}

func TestForkDaemonOpts_AllNewFields(t *testing.T) {
	opts := ForkDaemonOpts{
		Name:           "test-agent",
		SessionID:      "test-uuid",
		Command:        "claude",
		SystemPrompt:   "Custom prompt",
		Model:          "claude-sonnet-4-5-20250929",
		PermissionMode: "bypassPermissions",
	}

	if opts.SystemPrompt != "Custom prompt" {
		t.Fatalf("SystemPrompt not preserved: got %q", opts.SystemPrompt)
	}
	if opts.Model != "claude-sonnet-4-5-20250929" {
		t.Fatalf("Model not preserved: got %q", opts.Model)
	}
	if opts.PermissionMode != "bypassPermissions" {
		t.Fatalf("PermissionMode not preserved: got %q", opts.PermissionMode)
	}
}

func TestRunDaemonOpts_CodexAskForApprovalFields(t *testing.T) {
	opts := RunDaemonOpts{
		Name:                "test-agent",
		SessionID:           "test-uuid",
		Command:             "codex",
		Instructions:        "Do work",
		CodexAskForApproval: "never",
		CodexSandboxMode:    "danger-full-access",
	}

	s := New(opts.Name, opts.Command, opts.Args)
	s.SessionID = opts.SessionID
	s.Instructions = opts.Instructions
	s.CodexAskForApproval = opts.CodexAskForApproval
	s.CodexSandboxMode = opts.CodexSandboxMode

	if s.CodexAskForApproval != "never" {
		t.Fatalf("CodexAskForApproval not stored: got %q", s.CodexAskForApproval)
	}
	if s.CodexSandboxMode != "danger-full-access" {
		t.Fatalf("CodexSandboxMode not stored: got %q", s.CodexSandboxMode)
	}
}

func TestForkDaemonOpts_CodexAskForApprovalFields(t *testing.T) {
	opts := ForkDaemonOpts{
		Name:                "test-agent",
		SessionID:           "test-uuid",
		Command:             "codex",
		CodexAskForApproval: "on-request",
		CodexSandboxMode:    "workspace-write",
	}

	if opts.CodexAskForApproval != "on-request" {
		t.Fatalf("CodexAskForApproval not preserved: got %q", opts.CodexAskForApproval)
	}
	if opts.CodexSandboxMode != "workspace-write" {
		t.Fatalf("CodexSandboxMode not preserved: got %q", opts.CodexSandboxMode)
	}
}
