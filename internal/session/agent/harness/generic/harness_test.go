package generic

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"h2/internal/session/agent/harness"
	"h2/internal/session/agent/monitor"
)

// Verify GenericHarness implements harness.Harness.
var _ harness.Harness = (*GenericHarness)(nil)

func TestNew(t *testing.T) {
	g := New(harness.HarnessConfig{Command: "bash"})
	if g == nil {
		t.Fatal("expected non-nil harness")
	}
}

// --- Identity tests ---

func TestName(t *testing.T) {
	g := New(harness.HarnessConfig{Command: "bash"})
	if g.Name() != "generic" {
		t.Errorf("Name() = %q, want %q", g.Name(), "generic")
	}
}

func TestCommand(t *testing.T) {
	g := New(harness.HarnessConfig{Command: "bash"})
	if g.Command() != "bash" {
		t.Errorf("Command() = %q, want %q", g.Command(), "bash")
	}
}

func TestDisplayCommand(t *testing.T) {
	g := New(harness.HarnessConfig{Command: "/usr/bin/python3"})
	if g.DisplayCommand() != "/usr/bin/python3" {
		t.Errorf("DisplayCommand() = %q, want %q", g.DisplayCommand(), "/usr/bin/python3")
	}
}

// --- Config no-ops ---

func TestBuildCommandArgs_ReturnsNil(t *testing.T) {
	g := New(harness.HarnessConfig{Command: "bash"})
	args := g.BuildCommandArgs(harness.CommandArgsConfig{
		Instructions: "Should be ignored",
		Model:        "something",
	})
	if args != nil {
		t.Fatalf("expected nil for generic type, got %v", args)
	}
}

func TestBuildCommandEnvVars_ReturnsNil(t *testing.T) {
	g := New(harness.HarnessConfig{Command: "bash"})
	envVars := g.BuildCommandEnvVars("/home/user/.h2")
	if envVars != nil {
		t.Fatalf("expected nil env vars for generic, got %v", envVars)
	}
}

func TestEnsureConfigDir_Noop(t *testing.T) {
	g := New(harness.HarnessConfig{Command: "bash"})
	if err := g.EnsureConfigDir("/tmp/fake"); err != nil {
		t.Fatalf("EnsureConfigDir should be no-op, got: %v", err)
	}
}

// --- Launch ---

func TestPrepareForLaunch(t *testing.T) {
	g := New(harness.HarnessConfig{Command: "bash"})
	cfg, err := g.PrepareForLaunch("test-agent", "")
	if err != nil {
		t.Fatalf("PrepareForLaunch: %v", err)
	}
	if len(cfg.Env) != 0 {
		t.Errorf("Env = %v, want empty", cfg.Env)
	}
	if len(cfg.PrependArgs) != 0 {
		t.Errorf("PrependArgs = %v, want empty", cfg.PrependArgs)
	}
}

func TestPrepareForLaunch_EmptyCommand(t *testing.T) {
	g := New(harness.HarnessConfig{})
	_, err := g.PrepareForLaunch("test-agent", "")
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

// --- Runtime ---

func TestHandleHookEvent_ReturnsFalse(t *testing.T) {
	g := New(harness.HarnessConfig{Command: "bash"})
	if g.HandleHookEvent("PreToolUse", json.RawMessage("{}")) {
		t.Fatal("HandleHookEvent should return false for generic")
	}
}

func TestHandleOutput_BeforeStart(t *testing.T) {
	g := New(harness.HarnessConfig{Command: "bash"})
	// Should not panic when collector is nil.
	g.HandleOutput()
}

func TestStop_BeforeStart(t *testing.T) {
	g := New(harness.HarnessConfig{Command: "bash"})
	// Should not panic when collector is nil.
	g.Stop()
}

func TestStart_BridgesOutputToEvents(t *testing.T) {
	// Lower idle threshold for faster test.
	origThreshold := monitor.IdleThreshold
	monitor.IdleThreshold = 20 * time.Millisecond
	defer func() { monitor.IdleThreshold = origThreshold }()

	g := New(harness.HarnessConfig{Command: "bash"})
	events := make(chan monitor.AgentEvent, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		g.Start(ctx, events)
		close(done)
	}()

	// Give Start time to create the collector.
	time.Sleep(10 * time.Millisecond)

	// Simulate output → should get active state.
	g.HandleOutput()

	var gotActive bool
	timeout := time.After(2 * time.Second)
	for !gotActive {
		select {
		case ev := <-events:
			if ev.Type == monitor.EventStateChange {
				sc := ev.Data.(monitor.StateChangeData)
				if sc.State == monitor.StateActive {
					gotActive = true
				}
			}
		case <-timeout:
			t.Fatal("timed out waiting for active state event")
		}
	}

	// Wait for idle (no more output → idle after threshold).
	var gotIdle bool
	timeout = time.After(2 * time.Second)
	for !gotIdle {
		select {
		case ev := <-events:
			if ev.Type == monitor.EventStateChange {
				sc := ev.Data.(monitor.StateChangeData)
				if sc.State == monitor.StateIdle {
					gotIdle = true
				}
			}
		case <-timeout:
			t.Fatal("timed out waiting for idle state event")
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start didn't return after cancel")
	}
}

func TestStart_CancelReturns(t *testing.T) {
	g := New(harness.HarnessConfig{Command: "bash"})
	events := make(chan monitor.AgentEvent, 64)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		g.Start(ctx, events)
		close(done)
	}()

	// Give Start time to initialize.
	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start didn't return after cancel")
	}
}
