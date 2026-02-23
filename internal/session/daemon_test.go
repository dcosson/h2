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
		Name:            "test-agent",
		SessionID:       "test-uuid",
		Command:         "claude",
		Instructions:    "Instructions here",
		SystemPrompt:    "Custom system prompt",
		Model:           "claude-opus-4-6",
		PermissionMode:  "plan",
		AllowedTools:    []string{"Bash", "Read"},
		DisallowedTools: []string{"Write"},
	}

	s := New(opts.Name, opts.Command, opts.Args)
	s.SessionID = opts.SessionID
	s.Instructions = opts.Instructions
	s.SystemPrompt = opts.SystemPrompt
	s.Model = opts.Model
	s.PermissionMode = opts.PermissionMode
	s.AllowedTools = opts.AllowedTools
	s.DisallowedTools = opts.DisallowedTools

	if s.SystemPrompt != "Custom system prompt" {
		t.Fatalf("SystemPrompt not stored: got %q", s.SystemPrompt)
	}
	if s.Model != "claude-opus-4-6" {
		t.Fatalf("Model not stored: got %q", s.Model)
	}
	if s.PermissionMode != "plan" {
		t.Fatalf("PermissionMode not stored: got %q", s.PermissionMode)
	}
	if len(s.AllowedTools) != 2 || s.AllowedTools[0] != "Bash" || s.AllowedTools[1] != "Read" {
		t.Fatalf("AllowedTools not stored: got %v", s.AllowedTools)
	}
	if len(s.DisallowedTools) != 1 || s.DisallowedTools[0] != "Write" {
		t.Fatalf("DisallowedTools not stored: got %v", s.DisallowedTools)
	}

	// Verify all fields appear in childArgs.
	args := s.childArgs()
	expectPairs := map[string]string{
		"--system-prompt":        "Custom system prompt",
		"--append-system-prompt": "Instructions here",
		"--model":                "claude-opus-4-6",
		"--permission-mode":      "plan",
		"--allowedTools":         "Bash,Read",
		"--disallowedTools":      "Write",
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
		Name:            "test-agent",
		SessionID:       "test-uuid",
		Command:         "claude",
		SystemPrompt:    "Custom prompt",
		Model:           "claude-sonnet-4-5-20250929",
		PermissionMode:  "bypassPermissions",
		AllowedTools:    []string{"Bash", "Read", "Write"},
		DisallowedTools: []string{"Edit"},
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
	if len(opts.AllowedTools) != 3 {
		t.Fatalf("AllowedTools not preserved: got %v", opts.AllowedTools)
	}
	if len(opts.DisallowedTools) != 1 || opts.DisallowedTools[0] != "Edit" {
		t.Fatalf("DisallowedTools not preserved: got %v", opts.DisallowedTools)
	}
}

func TestRunDaemonOpts_ApprovalPolicyFields(t *testing.T) {
	opts := RunDaemonOpts{
		Name:             "test-agent",
		SessionID:        "test-uuid",
		Command:          "codex",
		Instructions:     "Do work",
		ApprovalPolicy:   "auto",
		CodexSandboxMode: "danger-full-access",
	}

	s := New(opts.Name, opts.Command, opts.Args)
	s.SessionID = opts.SessionID
	s.Instructions = opts.Instructions
	s.ApprovalPolicy = opts.ApprovalPolicy
	s.CodexSandboxMode = opts.CodexSandboxMode

	if s.ApprovalPolicy != "auto" {
		t.Fatalf("ApprovalPolicy not stored: got %q", s.ApprovalPolicy)
	}
	if s.CodexSandboxMode != "danger-full-access" {
		t.Fatalf("CodexSandboxMode not stored: got %q", s.CodexSandboxMode)
	}
}

func TestForkDaemonOpts_ApprovalPolicyFields(t *testing.T) {
	opts := ForkDaemonOpts{
		Name:             "test-agent",
		SessionID:        "test-uuid",
		Command:          "codex",
		ApprovalPolicy:   "auto-edit",
		CodexSandboxMode: "workspace-write",
	}

	if opts.ApprovalPolicy != "auto-edit" {
		t.Fatalf("ApprovalPolicy not preserved: got %q", opts.ApprovalPolicy)
	}
	if opts.CodexSandboxMode != "workspace-write" {
		t.Fatalf("CodexSandboxMode not preserved: got %q", opts.CodexSandboxMode)
	}
}
