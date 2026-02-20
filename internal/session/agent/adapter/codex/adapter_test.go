package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"h2/internal/session/agent/adapter"
	"h2/internal/session/agent/monitor"
)

// Verify CodexAdapter implements AgentAdapter.
var _ adapter.AgentAdapter = (*CodexAdapter)(nil)

func TestCodexAdapter_Name(t *testing.T) {
	a := New(nil)
	if a.Name() != "codex" {
		t.Fatalf("Name() = %q, want %q", a.Name(), "codex")
	}
}

func TestCodexAdapter_HandleHookEvent_ReturnsFalse(t *testing.T) {
	a := New(nil)
	if a.HandleHookEvent("PreToolUse", json.RawMessage("{}")) {
		t.Fatal("HandleHookEvent should return false for Codex")
	}
}

func TestCodexAdapter_PrepareForLaunch(t *testing.T) {
	a := New(nil)
	cfg, err := a.PrepareForLaunch("test-agent", "")
	if err != nil {
		t.Fatalf("PrepareForLaunch error: %v", err)
	}

	if len(cfg.PrependArgs) != 2 {
		t.Fatalf("expected 2 PrependArgs, got %d: %v", len(cfg.PrependArgs), cfg.PrependArgs)
	}
	if cfg.PrependArgs[0] != "-c" {
		t.Errorf("PrependArgs[0] = %q, want %q", cfg.PrependArgs[0], "-c")
	}
	// The -c value should contain the OTEL trace exporter config.
	if cfg.PrependArgs[1] == "" {
		t.Error("PrependArgs[1] should not be empty")
	}

	// OtelPort should be set after PrepareForLaunch.
	if a.OtelPort() == 0 {
		t.Error("OtelPort should be non-zero after PrepareForLaunch")
	}

	a.Stop()
}

func TestCodexAdapter_StartForwardsEvents(t *testing.T) {
	a := New(nil)

	// Manually push an event into the internal channel.
	a.internalCh <- monitor.AgentEvent{
		Type:      monitor.EventSessionStarted,
		Timestamp: time.Now(),
		Data:      monitor.SessionStartedData{ThreadID: "t1", Model: "o3"},
	}

	events := make(chan monitor.AgentEvent, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		a.Start(ctx, events)
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

func TestCodexAdapter_StopBeforePrepare(t *testing.T) {
	a := New(nil)
	// Stop should be safe even without PrepareForLaunch.
	a.Stop()
}

func TestCodexAdapter_OtelPort_BeforePrepare(t *testing.T) {
	a := New(nil)
	if a.OtelPort() != 0 {
		t.Errorf("OtelPort before PrepareForLaunch should be 0, got %d", a.OtelPort())
	}
}
