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

func TestCodexType_RoleArgs_Model(t *testing.T) {
	ct := NewCodexType()
	args := ct.RoleArgs("gpt-4o", "")
	if len(args) != 3 || args[0] != "--model" || args[1] != "gpt-4o" || args[2] != "--full-auto" {
		t.Fatalf("expected [--model gpt-4o --full-auto], got %v", args)
	}
}

func TestCodexType_RoleArgs_FullAuto(t *testing.T) {
	ct := NewCodexType()
	args := ct.RoleArgs("", "full-auto")
	if len(args) != 1 || args[0] != "--full-auto" {
		t.Fatalf("expected [--full-auto], got %v", args)
	}
}

func TestCodexType_RoleArgs_Suggest(t *testing.T) {
	ct := NewCodexType()
	args := ct.RoleArgs("", "suggest")
	if len(args) != 1 || args[0] != "--suggest" {
		t.Fatalf("expected [--suggest], got %v", args)
	}
}

func TestCodexType_RoleArgs_Ask(t *testing.T) {
	ct := NewCodexType()
	args := ct.RoleArgs("", "ask")
	if len(args) != 0 {
		t.Fatalf("expected empty args for ask mode (default), got %v", args)
	}
}

func TestCodexType_RoleArgs_DefaultFullAuto(t *testing.T) {
	ct := NewCodexType()
	args := ct.RoleArgs("", "")
	if len(args) != 1 || args[0] != "--full-auto" {
		t.Fatalf("expected [--full-auto] for empty permission mode, got %v", args)
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
