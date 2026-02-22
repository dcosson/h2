package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"h2/internal/session/agent/harness"
	"h2/internal/session/agent/monitor"
)

// Verify ClaudeCodeHarness implements harness.Harness.
var _ harness.Harness = (*ClaudeCodeHarness)(nil)

func TestNew(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	if h == nil {
		t.Fatal("expected non-nil harness")
	}
}

// --- Identity tests ---

func TestName(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	if h.Name() != "claude_code" {
		t.Errorf("Name() = %q, want %q", h.Name(), "claude_code")
	}
}

func TestCommand(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	if h.Command() != "claude" {
		t.Errorf("Command() = %q, want %q", h.Command(), "claude")
	}
}

func TestDisplayCommand(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	if h.DisplayCommand() != "claude" {
		t.Errorf("DisplayCommand() = %q, want %q", h.DisplayCommand(), "claude")
	}
}

// --- Config tests (from ClaudeCodeType) ---

func TestBuildCommandArgs_AllFields(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	args := h.BuildCommandArgs(harness.CommandArgsConfig{
		SessionID:       "test-uuid-123",
		SystemPrompt:    "Custom prompt",
		Instructions:    "Extra instructions",
		Model:           "claude-opus-4-6",
		PermissionMode:  "plan",
		AllowedTools:    []string{"Bash", "Read"},
		DisallowedTools: []string{"Write"},
	})
	expected := []string{
		"--session-id", "test-uuid-123",
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

func TestBuildCommandArgs_Empty(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	args := h.BuildCommandArgs(harness.CommandArgsConfig{})
	if len(args) != 0 {
		t.Fatalf("expected no args for empty config, got %v", args)
	}
}

func TestBuildCommandArgs_InstructionsOnly(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	args := h.BuildCommandArgs(harness.CommandArgsConfig{Instructions: "Do stuff"})
	if len(args) != 2 || args[0] != "--append-system-prompt" || args[1] != "Do stuff" {
		t.Fatalf("expected [--append-system-prompt 'Do stuff'], got %v", args)
	}
}

func TestBuildCommandArgs_SessionIDFirst(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	args := h.BuildCommandArgs(harness.CommandArgsConfig{
		SessionID:    "my-session",
		Instructions: "Do stuff",
	})
	if len(args) < 2 || args[0] != "--session-id" || args[1] != "my-session" {
		t.Fatalf("expected --session-id first, got %v", args)
	}
}

func TestBuildCommandArgs_NoSessionID(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	args := h.BuildCommandArgs(harness.CommandArgsConfig{Instructions: "Do stuff"})
	for _, arg := range args {
		if arg == "--session-id" {
			t.Fatal("--session-id should not appear when SessionID is empty")
		}
	}
}

func TestBuildCommandEnvVars_WithConfigDir(t *testing.T) {
	h := New(harness.HarnessConfig{ConfigDir: "/home/user/.h2/claude-config/my-role"}, nil)
	envVars := h.BuildCommandEnvVars("/home/user/.h2")
	if envVars == nil {
		t.Fatal("expected non-nil env vars")
	}
	want := "/home/user/.h2/claude-config/my-role"
	if envVars["CLAUDE_CONFIG_DIR"] != want {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want %q", envVars["CLAUDE_CONFIG_DIR"], want)
	}
}

func TestBuildCommandEnvVars_EmptyConfigDir(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	envVars := h.BuildCommandEnvVars("/home/user/.h2")
	if envVars != nil {
		t.Fatalf("expected nil env vars for empty configDir, got %v", envVars)
	}
}

func TestEnsureConfigDir_CreatesDir(t *testing.T) {
	h2Dir := t.TempDir()
	configDir := filepath.Join(h2Dir, "claude-config", "test-role")
	h := New(harness.HarnessConfig{ConfigDir: configDir}, nil)
	if err := h.EnsureConfigDir(h2Dir); err != nil {
		t.Fatalf("EnsureConfigDir: %v", err)
	}
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		t.Fatalf("expected config dir to exist: %s", configDir)
	}
	// Should have created settings.json.
	settingsPath := filepath.Join(configDir, "settings.json")
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		t.Fatalf("expected settings.json to exist: %s", settingsPath)
	}
}

func TestEnsureConfigDir_EmptyConfigDir(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	if err := h.EnsureConfigDir("/tmp/fake"); err != nil {
		t.Fatalf("EnsureConfigDir with empty configDir should be no-op, got: %v", err)
	}
}

// --- Launch tests (from ClaudeCodeAdapter) ---

func TestPrepareForLaunch(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	cfg, err := h.PrepareForLaunch("test-agent", "", false)
	if err != nil {
		t.Fatalf("PrepareForLaunch: %v", err)
	}
	defer h.Stop()

	// Should have a session ID.
	if h.SessionID() == "" {
		t.Error("expected non-empty session ID")
	}

	// PrependArgs should be empty.
	if len(cfg.PrependArgs) != 0 {
		t.Errorf("PrependArgs = %v, want empty", cfg.PrependArgs)
	}

	// Should have OTEL env vars.
	requiredEnvVars := []string{
		"CLAUDE_CODE_ENABLE_TELEMETRY",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_LOGS_EXPORTER",
		"OTEL_METRICS_EXPORTER",
	}
	for _, key := range requiredEnvVars {
		if _, ok := cfg.Env[key]; !ok {
			t.Errorf("missing env var %s", key)
		}
	}

	// Should have a valid OTEL port.
	if h.OtelPort() == 0 {
		t.Error("expected non-zero OtelPort after PrepareForLaunch")
	}
}

func TestPrepareForLaunch_WithSessionID(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	_, err := h.PrepareForLaunch("test-agent", "custom-session-id", false)
	if err != nil {
		t.Fatalf("PrepareForLaunch: %v", err)
	}
	defer h.Stop()

	if h.SessionID() != "custom-session-id" {
		t.Errorf("SessionID() = %q, want %q", h.SessionID(), "custom-session-id")
	}
}

// --- Runtime tests (from ClaudeCodeAdapter) ---

func TestHandleHookEvent_EmitsStateChange(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)

	events := make(chan monitor.AgentEvent, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Start(ctx, events)

	time.Sleep(10 * time.Millisecond)

	payload, _ := json.Marshal(map[string]string{"session_id": "s1"})
	h.HandleHookEvent("UserPromptSubmit", payload)

	var got []monitor.AgentEvent
	timeout := time.After(time.Second)
	for len(got) < 2 {
		select {
		case ev := <-events:
			got = append(got, ev)
		case <-timeout:
			t.Fatalf("timed out, got %d events, want 2", len(got))
		}
	}

	if got[0].Type != monitor.EventUserPrompt {
		t.Errorf("event[0].Type = %v, want EventUserPrompt", got[0].Type)
	}
	if got[1].Type != monitor.EventStateChange {
		t.Errorf("event[1].Type = %v, want EventStateChange", got[1].Type)
	}
	sc := got[1].Data.(monitor.StateChangeData)
	if sc.State != monitor.StateActive || sc.SubState != monitor.SubStateThinking {
		t.Errorf("StateChange = %+v, want Active/Thinking", sc)
	}
}

func TestHandleHookEvent_PreToolUse(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	events := make(chan monitor.AgentEvent, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Start(ctx, events)
	time.Sleep(10 * time.Millisecond)

	payload, _ := json.Marshal(map[string]string{"tool_name": "Bash", "session_id": "s1"})
	h.HandleHookEvent("PreToolUse", payload)

	var got []monitor.AgentEvent
	timeout := time.After(time.Second)
	for len(got) < 2 {
		select {
		case ev := <-events:
			got = append(got, ev)
		case <-timeout:
			t.Fatalf("timed out, got %d events", len(got))
		}
	}

	if got[0].Type != monitor.EventToolStarted {
		t.Errorf("event[0].Type = %v, want EventToolStarted", got[0].Type)
	}
	if got[1].Type != monitor.EventStateChange {
		t.Errorf("event[1].Type = %v, want EventStateChange", got[1].Type)
	}
	sc := got[1].Data.(monitor.StateChangeData)
	if sc.SubState != monitor.SubStateToolUse {
		t.Errorf("SubState = %v, want ToolUse", sc.SubState)
	}
}

func TestHandleHookEvent_SessionEnd(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	events := make(chan monitor.AgentEvent, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Start(ctx, events)
	time.Sleep(10 * time.Millisecond)

	h.HandleHookEvent("SessionEnd", nil)

	select {
	case ev := <-events:
		if ev.Type != monitor.EventSessionEnded {
			t.Errorf("Type = %v, want EventSessionEnded", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SessionEnd event")
	}
}

func TestStartBlocksUntilCancelled(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	events := make(chan monitor.AgentEvent, 64)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		h.Start(ctx, events)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// OK, Start returned.
	case <-time.After(time.Second):
		t.Fatal("Start didn't return after cancel")
	}
}

func TestHandleOutput_Noop(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	// Should not panic.
	h.HandleOutput()
}
