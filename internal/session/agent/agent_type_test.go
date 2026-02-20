package agent

import "testing"

func TestResolveAgentType_Claude(t *testing.T) {
	at := ResolveAgentType("claude")
	if at.Name() != "claude" {
		t.Fatalf("expected name 'claude', got %q", at.Name())
	}
	if at.Command() != "claude" {
		t.Fatalf("expected command 'claude', got %q", at.Command())
	}
	if at.NewAdapter(nil) == nil {
		t.Fatal("expected non-nil adapter for claude")
	}
}

func TestResolveAgentType_ClaudeFullPath(t *testing.T) {
	at := ResolveAgentType("/usr/local/bin/claude")
	if at.Name() != "claude" {
		t.Fatalf("expected name 'claude' for full path, got %q", at.Name())
	}
}

func TestResolveAgentType_Generic(t *testing.T) {
	at := ResolveAgentType("bash")
	if at.Name() != "generic" {
		t.Fatalf("expected name 'generic', got %q", at.Name())
	}
	if at.Command() != "bash" {
		t.Fatalf("expected command 'bash', got %q", at.Command())
	}
	if at.NewAdapter(nil) != nil {
		t.Fatal("expected nil adapter for generic type")
	}
}

func TestGenericType_DisplayCommand(t *testing.T) {
	gt := NewGenericType("/usr/bin/python3")
	if gt.DisplayCommand() != "/usr/bin/python3" {
		t.Fatalf("expected display command '/usr/bin/python3', got %q", gt.DisplayCommand())
	}
}

func TestResolveAgentType_Codex(t *testing.T) {
	at := ResolveAgentType("codex")
	if at.Name() != "codex" {
		t.Fatalf("expected name 'codex', got %q", at.Name())
	}
	if at.Command() != "codex" {
		t.Fatalf("expected command 'codex', got %q", at.Command())
	}
	if at.NewAdapter(nil) == nil {
		t.Fatal("expected non-nil adapter for codex")
	}
}

func TestResolveAgentType_CodexFullPath(t *testing.T) {
	at := ResolveAgentType("/usr/local/bin/codex")
	if at.Name() != "codex" {
		t.Fatalf("expected name 'codex' for full path, got %q", at.Name())
	}
}

// --- ClaudeCodeType.BuildCommandArgs tests ---

func TestClaudeCodeType_BuildCommandArgs_AllFields(t *testing.T) {
	ct := NewClaudeCodeType()
	args := ct.BuildCommandArgs(CommandArgsConfig{
		SystemPrompt:    "Custom prompt",
		Instructions:    "Extra instructions",
		Model:           "claude-opus-4-6",
		PermissionMode:  "plan",
		AllowedTools:    []string{"Bash", "Read"},
		DisallowedTools: []string{"Write"},
	})
	expected := []string{
		"--system-prompt", "Custom prompt",
		"--append-system-prompt", "Extra instructions",
		"--model", "claude-opus-4-6",
		"--permission-mode", "plan",
		"--allowedTools", "Bash,Read",
		"--disallowedTools", "Write",
	}
	if len(args) != len(expected) {
		t.Fatalf("expected %d args, got %d: %v", len(expected), len(args), args)
	}
	for i, want := range expected {
		if args[i] != want {
			t.Errorf("arg[%d] = %q, want %q", i, args[i], want)
		}
	}
}

func TestClaudeCodeType_BuildCommandArgs_Empty(t *testing.T) {
	ct := NewClaudeCodeType()
	args := ct.BuildCommandArgs(CommandArgsConfig{})
	if len(args) != 0 {
		t.Fatalf("expected no args for empty config, got %v", args)
	}
}

func TestClaudeCodeType_BuildCommandArgs_InstructionsOnly(t *testing.T) {
	ct := NewClaudeCodeType()
	args := ct.BuildCommandArgs(CommandArgsConfig{Instructions: "Do stuff"})
	if len(args) != 2 || args[0] != "--append-system-prompt" || args[1] != "Do stuff" {
		t.Fatalf("expected [--append-system-prompt 'Do stuff'], got %v", args)
	}
}

// --- CodexType.BuildCommandArgs tests ---

func TestCodexType_BuildCommandArgs_Model(t *testing.T) {
	ct := NewCodexType()
	args := ct.BuildCommandArgs(CommandArgsConfig{Model: "gpt-4o"})
	// Model + default full-auto
	if len(args) != 3 || args[0] != "--model" || args[1] != "gpt-4o" || args[2] != "--full-auto" {
		t.Fatalf("expected [--model gpt-4o --full-auto], got %v", args)
	}
}

func TestCodexType_BuildCommandArgs_FullAuto(t *testing.T) {
	ct := NewCodexType()
	args := ct.BuildCommandArgs(CommandArgsConfig{PermissionMode: "full-auto"})
	if len(args) != 1 || args[0] != "--full-auto" {
		t.Fatalf("expected [--full-auto], got %v", args)
	}
}

func TestCodexType_BuildCommandArgs_Suggest(t *testing.T) {
	ct := NewCodexType()
	args := ct.BuildCommandArgs(CommandArgsConfig{PermissionMode: "suggest"})
	if len(args) != 1 || args[0] != "--suggest" {
		t.Fatalf("expected [--suggest], got %v", args)
	}
}

func TestCodexType_BuildCommandArgs_Ask(t *testing.T) {
	ct := NewCodexType()
	args := ct.BuildCommandArgs(CommandArgsConfig{PermissionMode: "ask"})
	if len(args) != 0 {
		t.Fatalf("expected empty args for ask mode (default), got %v", args)
	}
}

func TestCodexType_BuildCommandArgs_DefaultFullAuto(t *testing.T) {
	ct := NewCodexType()
	args := ct.BuildCommandArgs(CommandArgsConfig{})
	if len(args) != 1 || args[0] != "--full-auto" {
		t.Fatalf("expected [--full-auto] for empty config, got %v", args)
	}
}

func TestCodexType_BuildCommandArgs_Instructions(t *testing.T) {
	ct := NewCodexType()
	args := ct.BuildCommandArgs(CommandArgsConfig{Instructions: "Do testing"})
	// -c, instructions="Do testing", --full-auto
	if len(args) != 3 || args[0] != "-c" || args[1] != `instructions="Do testing"` || args[2] != "--full-auto" {
		t.Fatalf(`expected [-c instructions="Do testing" --full-auto], got %v`, args)
	}
}

func TestCodexType_BuildCommandArgs_IgnoresUnsupported(t *testing.T) {
	ct := NewCodexType()
	args := ct.BuildCommandArgs(CommandArgsConfig{
		SystemPrompt:    "Should be ignored",
		AllowedTools:    []string{"Bash"},
		DisallowedTools: []string{"Write"},
		PermissionMode:  "ask",
	})
	// Only ask mode → no args (unsupported fields silently ignored)
	if len(args) != 0 {
		t.Fatalf("expected no args (unsupported fields ignored, ask=default), got %v", args)
	}
}

// --- GenericType.BuildCommandArgs tests ---

func TestGenericType_BuildCommandArgs_ReturnsNil(t *testing.T) {
	gt := NewGenericType("bash")
	args := gt.BuildCommandArgs(CommandArgsConfig{
		Instructions: "Should be ignored",
		Model:        "something",
	})
	if args != nil {
		t.Fatalf("expected nil for generic type, got %v", args)
	}
}

func TestCodexType_DisplayCommand(t *testing.T) {
	ct := NewCodexType()
	if ct.DisplayCommand() != "codex" {
		t.Fatalf("expected display command 'codex', got %q", ct.DisplayCommand())
	}
}

func TestClaudeCodeType_NewAdapter(t *testing.T) {
	ct := NewClaudeCodeType()
	a := ct.NewAdapter(nil)
	if a == nil {
		t.Fatal("expected non-nil adapter")
	}
	if a.Name() != "claude-code" {
		t.Fatalf("expected adapter name 'claude-code', got %q", a.Name())
	}
}

func TestCodexType_NewAdapter(t *testing.T) {
	ct := NewCodexType()
	a := ct.NewAdapter(nil)
	if a == nil {
		t.Fatal("expected non-nil adapter")
	}
	if a.Name() != "codex" {
		t.Fatalf("expected adapter name 'codex', got %q", a.Name())
	}
}

func TestGenericType_NewAdapter_ReturnsNil(t *testing.T) {
	gt := NewGenericType("bash")
	if gt.NewAdapter(nil) != nil {
		t.Fatal("expected nil adapter for generic type")
	}
}

// --- BuildCommandEnvVars tests ---

func TestClaudeCodeType_BuildCommandEnvVars(t *testing.T) {
	ct := NewClaudeCodeType()
	envVars := ct.BuildCommandEnvVars("/home/user/.h2", "my-role")
	if envVars == nil {
		t.Fatal("expected non-nil env vars")
	}
	want := "/home/user/.h2/claude-config/my-role"
	if envVars["CLAUDE_CONFIG_DIR"] != want {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want %q", envVars["CLAUDE_CONFIG_DIR"], want)
	}
}

func TestClaudeCodeType_BuildCommandEnvVars_EmptyRole(t *testing.T) {
	ct := NewClaudeCodeType()
	envVars := ct.BuildCommandEnvVars("/home/user/.h2", "")
	want := "/home/user/.h2/claude-config/default"
	if envVars["CLAUDE_CONFIG_DIR"] != want {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want %q (empty role should default to 'default')", envVars["CLAUDE_CONFIG_DIR"], want)
	}
}

func TestCodexType_BuildCommandEnvVars_ReturnsNil(t *testing.T) {
	ct := NewCodexType()
	envVars := ct.BuildCommandEnvVars("/home/user/.h2", "my-role")
	if envVars != nil {
		t.Fatalf("expected nil env vars for codex, got %v", envVars)
	}
}

func TestGenericType_BuildCommandEnvVars_ReturnsNil(t *testing.T) {
	gt := NewGenericType("bash")
	envVars := gt.BuildCommandEnvVars("/home/user/.h2", "my-role")
	if envVars != nil {
		t.Fatalf("expected nil env vars for generic, got %v", envVars)
	}
}
