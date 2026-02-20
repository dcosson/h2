package harness

import (
	"strings"
	"testing"
)

func TestResolve_UnknownType(t *testing.T) {
	_, err := Resolve(HarnessConfig{HarnessType: "unknown"}, nil)
	if err == nil {
		t.Fatal("expected error for unknown harness type")
	}
	if !strings.Contains(err.Error(), "unknown harness type") {
		t.Errorf("error = %q, want it to contain 'unknown harness type'", err.Error())
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Errorf("error = %q, want it to contain the type name", err.Error())
	}
}

func TestResolve_GenericWithoutCommand(t *testing.T) {
	_, err := Resolve(HarnessConfig{HarnessType: "generic"}, nil)
	if err == nil {
		t.Fatal("expected error for generic harness without command")
	}
	if !strings.Contains(err.Error(), "requires a command") {
		t.Errorf("error = %q, want it to contain 'requires a command'", err.Error())
	}
}

func TestResolve_GenericWithCommand_Placeholder(t *testing.T) {
	_, err := Resolve(HarnessConfig{HarnessType: "generic", Command: "bash"}, nil)
	if err == nil {
		t.Fatal("expected placeholder error for generic harness (not yet implemented)")
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("error = %q, want placeholder error", err.Error())
	}
}

func TestResolve_ClaudeCode_NotRegistered(t *testing.T) {
	// Without importing harness/claude, the factory is not registered.
	// This tests the fallback error path. Integration tests that verify
	// the full Resolve → claude.New path live in resolve_test.go (external test package).
	//
	// Skip if already registered (e.g. if run alongside integration tests).
	h, err := Resolve(HarnessConfig{HarnessType: "claude_code"}, nil)
	if h != nil {
		t.Skip("claude harness already registered (running with integration tests)")
	}
	if err == nil {
		t.Fatal("expected error for unregistered claude_code harness")
	}
	if !strings.Contains(err.Error(), "not registered") {
		t.Errorf("error = %q, want it to contain 'not registered'", err.Error())
	}
}

func TestResolve_ClaudeLegacy_NotRegistered(t *testing.T) {
	// Same as above but for "claude" legacy name.
	h, err := Resolve(HarnessConfig{HarnessType: "claude"}, nil)
	if h != nil {
		t.Skip("claude harness already registered")
	}
	if err == nil {
		t.Fatal("expected error for unregistered claude harness")
	}
	if !strings.Contains(err.Error(), "not registered") {
		t.Errorf("error = %q, want it to contain 'not registered'", err.Error())
	}
}

func TestResolve_Codex_Placeholder(t *testing.T) {
	_, err := Resolve(HarnessConfig{HarnessType: "codex"}, nil)
	if err == nil {
		t.Fatal("expected placeholder error for codex harness")
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("error = %q, want placeholder error", err.Error())
	}
}

func TestResolve_EmptyType(t *testing.T) {
	_, err := Resolve(HarnessConfig{HarnessType: ""}, nil)
	if err == nil {
		t.Fatal("expected error for empty harness type")
	}
	if !strings.Contains(err.Error(), "unknown harness type") {
		t.Errorf("error = %q, want 'unknown harness type'", err.Error())
	}
}

func TestPTYInputSender(t *testing.T) {
	// Verify PTYInputSender satisfies InputSender interface.
	var _ InputSender = (*PTYInputSender)(nil)
}
