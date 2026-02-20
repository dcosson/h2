package claude

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"h2/internal/session/agent/adapter"
	"h2/internal/session/agent/monitor"
)

// Verify ClaudeCodeAdapter implements AgentAdapter.
var _ adapter.AgentAdapter = (*ClaudeCodeAdapter)(nil)

func TestName(t *testing.T) {
	a := New(nil)
	if a.Name() != "claude-code" {
		t.Errorf("Name() = %q, want %q", a.Name(), "claude-code")
	}
}

func TestPrepareForLaunch(t *testing.T) {
	a := New(nil)
	cfg, err := a.PrepareForLaunch("test-agent", "")
	if err != nil {
		t.Fatalf("PrepareForLaunch: %v", err)
	}
	defer a.Stop()

	// Should have a session ID.
	if a.SessionID() == "" {
		t.Error("expected non-empty session ID")
	}

	// PrependArgs should be empty (--session-id is now in BuildCommandArgs).
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
	if a.OtelPort() == 0 {
		t.Error("expected non-zero OtelPort after PrepareForLaunch")
	}
}

func TestHandleHookEvent_EmitsStateChange(t *testing.T) {
	a := New(nil)

	// Start the adapter in a goroutine so events are forwarded.
	events := make(chan monitor.AgentEvent, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Start(ctx, events)

	// Give Start a moment to begin forwarding.
	time.Sleep(10 * time.Millisecond)

	// Send a UserPromptSubmit hook event.
	payload, _ := json.Marshal(map[string]string{"session_id": "s1"})
	a.HandleHookEvent("UserPromptSubmit", payload)

	// Should receive EventTurnStarted + EventStateChange.
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

	if got[0].Type != monitor.EventTurnStarted {
		t.Errorf("event[0].Type = %v, want EventTurnStarted", got[0].Type)
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
	a := New(nil)
	events := make(chan monitor.AgentEvent, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Start(ctx, events)
	time.Sleep(10 * time.Millisecond)

	payload, _ := json.Marshal(map[string]string{"tool_name": "Bash", "session_id": "s1"})
	a.HandleHookEvent("PreToolUse", payload)

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
	a := New(nil)
	events := make(chan monitor.AgentEvent, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Start(ctx, events)
	time.Sleep(10 * time.Millisecond)

	a.HandleHookEvent("SessionEnd", nil)

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
	a := New(nil)
	events := make(chan monitor.AgentEvent, 64)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		a.Start(ctx, events)
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
