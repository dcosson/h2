package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"h2/internal/session/agent/monitor"
)

func TestSessionLogCollector_EmitsAssistantMessages(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.jsonl")

	// Create the session log file with some entries.
	entries := []map[string]any{
		{"type": "user", "message": map[string]string{"role": "user", "content": "hello"}},
		{"type": "assistant", "message": map[string]string{"role": "assistant", "content": "Hi there!"}},
		{"type": "assistant", "message": map[string]string{"role": "assistant", "content": "How can I help?"}},
	}
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, _ := json.Marshal(e)
		f.Write(data)
		f.Write([]byte("\n"))
	}
	f.Close()

	// Run the collector.
	events := make(chan monitor.AgentEvent, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := NewSessionLogCollector(logPath)
	go c.Run(ctx, events)

	// Should get 2 assistant message events.
	var got []monitor.AgentEvent
	timeout := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case ev := <-events:
			got = append(got, ev)
		case <-timeout:
			t.Fatalf("timed out, got %d events, want 2", len(got))
		}
	}

	if got[0].Type != monitor.EventAgentMessage {
		t.Errorf("event[0].Type = %v, want EventAgentMessage", got[0].Type)
	}
	if got[0].Data.(string) != "Hi there!" {
		t.Errorf("event[0].Data = %v, want 'Hi there!'", got[0].Data)
	}
	if got[1].Data.(string) != "How can I help?" {
		t.Errorf("event[1].Data = %v, want 'How can I help?'", got[1].Data)
	}
}

func TestSessionLogCollector_WaitsForFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.jsonl")

	events := make(chan monitor.AgentEvent, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := NewSessionLogCollector(logPath)
	go c.Run(ctx, events)

	// File doesn't exist yet — no events should arrive.
	select {
	case ev := <-events:
		t.Errorf("unexpected event before file exists: %+v", ev)
	case <-time.After(100 * time.Millisecond):
		// OK
	}

	// Create the file with an assistant message.
	entry, _ := json.Marshal(map[string]any{
		"type":    "assistant",
		"message": map[string]string{"role": "assistant", "content": "Found it!"},
	})
	os.WriteFile(logPath, append(entry, '\n'), 0o644)

	// Should receive the event.
	select {
	case ev := <-events:
		if ev.Data.(string) != "Found it!" {
			t.Errorf("Data = %v, want 'Found it!'", ev.Data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event after file creation")
	}
}

func TestSessionLogCollector_CancelStops(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.jsonl")

	events := make(chan monitor.AgentEvent, 64)
	ctx, cancel := context.WithCancel(context.Background())

	c := NewSessionLogCollector(logPath)
	done := make(chan struct{})
	go func() {
		c.Run(ctx, events)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("Run didn't return after cancel")
	}
}
