package harness_test

import (
	"testing"

	"h2/internal/session/agent/harness"

	// Register harness implementations.
	_ "h2/internal/session/agent/harness/claude"
	_ "h2/internal/session/agent/harness/codex"
	_ "h2/internal/session/agent/harness/generic"
)

func TestResolve_ClaudeCode(t *testing.T) {
	h, err := harness.Resolve(harness.HarnessConfig{HarnessType: "claude_code"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h == nil {
		t.Fatal("expected non-nil harness")
	}
	if h.Name() != "claude_code" {
		t.Errorf("Name() = %q, want %q", h.Name(), "claude_code")
	}
}

func TestResolve_Codex(t *testing.T) {
	h, err := harness.Resolve(harness.HarnessConfig{HarnessType: "codex"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h == nil {
		t.Fatal("expected non-nil harness")
	}
	if h.Name() != "codex" {
		t.Errorf("Name() = %q, want %q", h.Name(), "codex")
	}
	if h.Command() != "codex" {
		t.Errorf("Command() = %q, want %q", h.Command(), "codex")
	}
}

func TestResolve_Generic(t *testing.T) {
	h, err := harness.Resolve(harness.HarnessConfig{HarnessType: "generic", Command: "bash"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h == nil {
		t.Fatal("expected non-nil harness")
	}
	if h.Name() != "generic" {
		t.Errorf("Name() = %q, want %q", h.Name(), "generic")
	}
	if h.Command() != "bash" {
		t.Errorf("Command() = %q, want %q", h.Command(), "bash")
	}
}

func TestResolve_Generic_NoCommand(t *testing.T) {
	_, err := harness.Resolve(harness.HarnessConfig{HarnessType: "generic"}, nil)
	if err == nil {
		t.Fatal("expected error for generic without command")
	}
}

func TestResolve_ClaudeCode_ConfigPassthrough(t *testing.T) {
	cfg := harness.HarnessConfig{
		HarnessType: "claude_code",
		ConfigDir:   "/tmp/test-config",
		Model:       "opus",
	}
	h, err := harness.Resolve(cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.Command() != "claude" {
		t.Errorf("Command() = %q, want %q", h.Command(), "claude")
	}
	if h.DisplayCommand() != "claude" {
		t.Errorf("DisplayCommand() = %q, want %q", h.DisplayCommand(), "claude")
	}
	// Verify the config was passed through by checking BuildCommandEnvVars.
	envVars := h.BuildCommandEnvVars("/unused")
	if envVars["CLAUDE_CONFIG_DIR"] != "/tmp/test-config" {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want %q", envVars["CLAUDE_CONFIG_DIR"], "/tmp/test-config")
	}
}
