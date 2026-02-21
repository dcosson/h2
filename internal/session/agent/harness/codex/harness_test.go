package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"h2/internal/session/agent/harness"
	"h2/internal/session/agent/monitor"
)

// Verify CodexHarness implements harness.Harness.
var _ harness.Harness = (*CodexHarness)(nil)

func TestNew(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	if h == nil {
		t.Fatal("expected non-nil harness")
	}
}

// --- Identity tests ---

func TestName(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	if h.Name() != "codex" {
		t.Errorf("Name() = %q, want %q", h.Name(), "codex")
	}
}

func TestCommand(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	if h.Command() != "codex" {
		t.Errorf("Command() = %q, want %q", h.Command(), "codex")
	}
}

func TestDisplayCommand(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	if h.DisplayCommand() != "codex" {
		t.Errorf("DisplayCommand() = %q, want %q", h.DisplayCommand(), "codex")
	}
}

// --- Config tests (from CodexType) ---

func TestBuildCommandArgs_Instructions(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	args := h.BuildCommandArgs(harness.CommandArgsConfig{Instructions: "Do testing"})
	if len(args) != 3 || args[0] != "-c" || args[1] != `instructions="Do testing"` || args[2] != "--full-auto" {
		t.Fatalf(`expected [-c instructions="Do testing" --full-auto], got %v`, args)
	}
}

func TestBuildCommandArgs_InstructionsMultiline(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	args := h.BuildCommandArgs(harness.CommandArgsConfig{Instructions: "Line 1\nLine 2\nSay \"hello\""})
	// json.Marshal escapes newlines and quotes for Codex -c JSON parsing.
	want := `instructions="Line 1\nLine 2\nSay \"hello\""`
	if len(args) < 2 || args[1] != want {
		t.Fatalf("expected %s, got %v", want, args)
	}
}

func TestBuildCommandArgs_Model(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	args := h.BuildCommandArgs(harness.CommandArgsConfig{Model: "gpt-4o"})
	if len(args) != 3 || args[0] != "--model" || args[1] != "gpt-4o" || args[2] != "--full-auto" {
		t.Fatalf("expected [--model gpt-4o --full-auto], got %v", args)
	}
}

func TestBuildCommandArgs_FullAuto(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	args := h.BuildCommandArgs(harness.CommandArgsConfig{PermissionMode: "full-auto"})
	if len(args) != 1 || args[0] != "--full-auto" {
		t.Fatalf("expected [--full-auto], got %v", args)
	}
}

func TestBuildCommandArgs_Suggest(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	args := h.BuildCommandArgs(harness.CommandArgsConfig{PermissionMode: "suggest"})
	if len(args) != 1 || args[0] != "--suggest" {
		t.Fatalf("expected [--suggest], got %v", args)
	}
}

func TestBuildCommandArgs_Ask(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	args := h.BuildCommandArgs(harness.CommandArgsConfig{PermissionMode: "ask"})
	if len(args) != 0 {
		t.Fatalf("expected empty args for ask mode (default), got %v", args)
	}
}

func TestBuildCommandArgs_DefaultFullAuto(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	args := h.BuildCommandArgs(harness.CommandArgsConfig{})
	if len(args) != 1 || args[0] != "--full-auto" {
		t.Fatalf("expected [--full-auto] for empty config, got %v", args)
	}
}

func TestBuildCommandArgs_IgnoresSessionID(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	args := h.BuildCommandArgs(harness.CommandArgsConfig{SessionID: "some-uuid", PermissionMode: "ask"})
	for _, arg := range args {
		if arg == "--session-id" {
			t.Fatal("Codex should not include --session-id")
		}
	}
}

func TestBuildCommandArgs_IgnoresUnsupported(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	args := h.BuildCommandArgs(harness.CommandArgsConfig{
		SystemPrompt:    "Should be ignored",
		AllowedTools:    []string{"Bash"},
		DisallowedTools: []string{"Write"},
		PermissionMode:  "ask",
	})
	if len(args) != 0 {
		t.Fatalf("expected no args (unsupported fields ignored, ask=default), got %v", args)
	}
}

func TestBuildCommandEnvVars_ReturnsNil(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	envVars := h.BuildCommandEnvVars("/home/user/.h2")
	if envVars != nil {
		t.Fatalf("expected nil env vars for codex, got %v", envVars)
	}
}

func TestEnsureConfigDir_Noop(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	if err := h.EnsureConfigDir("/tmp/fake"); err != nil {
		t.Fatalf("EnsureConfigDir should be no-op, got: %v", err)
	}
}

// --- Launch tests (from CodexAdapter) ---

func TestPrepareForLaunch(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	cfg, err := h.PrepareForLaunch("test-agent", "")
	if err != nil {
		t.Fatalf("PrepareForLaunch error: %v", err)
	}
	defer h.Stop()

	if len(cfg.PrependArgs) != 2 {
		t.Fatalf("expected 2 PrependArgs, got %d: %v", len(cfg.PrependArgs), cfg.PrependArgs)
	}
	if cfg.PrependArgs[0] != "-c" {
		t.Errorf("PrependArgs[0] = %q, want %q", cfg.PrependArgs[0], "-c")
	}
	if cfg.PrependArgs[1] == "" {
		t.Error("PrependArgs[1] should not be empty")
	}

	if h.OtelPort() == 0 {
		t.Error("OtelPort should be non-zero after PrepareForLaunch")
	}
}

// --- Runtime tests ---

func TestHandleHookEvent_ReturnsFalse(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	if h.HandleHookEvent("PreToolUse", json.RawMessage("{}")) {
		t.Fatal("HandleHookEvent should return false for Codex")
	}
}

func TestStartForwardsEvents(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)

	// Manually push an event into the internal channel.
	h.internalCh <- monitor.AgentEvent{
		Type:      monitor.EventSessionStarted,
		Timestamp: time.Now(),
		Data:      monitor.SessionStartedData{ThreadID: "t1", Model: "o3"},
	}

	events := make(chan monitor.AgentEvent, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		h.Start(ctx, events)
		close(done)
	}()

	select {
	case ev := <-events:
		if ev.Type != monitor.EventSessionStarted {
			t.Errorf("Type = %v, want EventSessionStarted", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for forwarded event")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start didn't return after cancel")
	}
}

func TestStopBeforePrepare(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	// Stop should be safe even without PrepareForLaunch.
	h.Stop()
}

func TestOtelPort_BeforePrepare(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	if h.OtelPort() != 0 {
		t.Errorf("OtelPort before PrepareForLaunch should be 0, got %d", h.OtelPort())
	}
}

func TestHandleOutput_Noop(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	// Should not panic.
	h.HandleOutput()
}
