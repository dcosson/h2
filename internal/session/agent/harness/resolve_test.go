package harness_test

import (
	"testing"

	"h2/internal/session/agent/harness"

	// Register harness implementations.
	_ "h2/internal/session/agent/harness/claude"
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

func TestResolve_ClaudeLegacy(t *testing.T) {
	h, err := harness.Resolve(harness.HarnessConfig{HarnessType: "claude"}, nil)
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
