package eventstore

import (
	"context"
	"testing"
	"time"

	"h2/internal/session/agent/monitor"
)

func TestOpenCreatesFile(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// File should exist.
	if s.file == nil {
		t.Fatal("expected file to be non-nil")
	}
}

func TestOpenCreatesDir(t *testing.T) {
	dir := t.TempDir() + "/nested/sessions"
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
}

func TestAppendAndRead_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	now := time.Now().Truncate(time.Millisecond)

	events := []monitor.AgentEvent{
		{
			Type:      monitor.EventSessionStarted,
			Timestamp: now,
			Data:      monitor.SessionStartedData{ThreadID: "t1", Model: "claude-4"},
		},
		{
			Type:      monitor.EventTurnCompleted,
			Timestamp: now.Add(time.Second),
			Data: monitor.TurnCompletedData{
				TurnID:       "turn-1",
				InputTokens:  100,
				OutputTokens: 200,
				CachedTokens: 50,
				CostUSD:      0.01,
			},
		},
		{
			Type:      monitor.EventToolCompleted,
			Timestamp: now.Add(2 * time.Second),
			Data: monitor.ToolCompletedData{
				ToolName:   "Bash",
				CallID:     "call-1",
				DurationMs: 500,
				Success:    true,
			},
		},
		{
			Type:      monitor.EventStateChange,
			Timestamp: now.Add(3 * time.Second),
			Data: monitor.StateChangeData{
				State:    monitor.StateActive,
				SubState: monitor.SubStateThinking,
			},
		},
	}

	for _, ev := range events {
		if err := s.Append(ev); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, err := s.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if len(got) != len(events) {
		t.Fatalf("expected %d events, got %d", len(events), len(got))
	}

	// Check types and timestamps.
	for i, ev := range events {
		if got[i].Type != ev.Type {
			t.Errorf("event %d: type = %v, want %v", i, got[i].Type, ev.Type)
		}
		if !got[i].Timestamp.Equal(ev.Timestamp) {
			t.Errorf("event %d: timestamp = %v, want %v", i, got[i].Timestamp, ev.Timestamp)
		}
	}

	// Check specific payloads.
	sess := got[0].Data.(monitor.SessionStartedData)
	if sess.ThreadID != "t1" || sess.Model != "claude-4" {
		t.Errorf("SessionStartedData = %+v, want ThreadID=t1, Model=claude-4", sess)
	}

	turn := got[1].Data.(monitor.TurnCompletedData)
	if turn.InputTokens != 100 || turn.OutputTokens != 200 {
		t.Errorf("TurnCompletedData = %+v", turn)
	}

	tool := got[2].Data.(monitor.ToolCompletedData)
	if tool.ToolName != "Bash" || !tool.Success {
		t.Errorf("ToolCompletedData = %+v", tool)
	}

	state := got[3].Data.(monitor.StateChangeData)
	if state.State != monitor.StateActive || state.SubState != monitor.SubStateThinking {
		t.Errorf("StateChangeData = %+v", state)
	}
}

func TestAppendAndRead_NoData(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	ev := monitor.AgentEvent{
		Type:      monitor.EventSessionEnded,
		Timestamp: time.Now().Truncate(time.Millisecond),
	}
	if err := s.Append(ev); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := s.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Type != monitor.EventSessionEnded {
		t.Errorf("type = %v, want EventSessionEnded", got[0].Type)
	}
}

func TestReadEmpty(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	got, err := s.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 events, got %d", len(got))
	}
}

func TestTail_StreamsNewEvents(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Write an event before tailing — should NOT be received.
	pre := monitor.AgentEvent{
		Type:      monitor.EventSessionStarted,
		Timestamp: time.Now().Truncate(time.Millisecond),
		Data:      monitor.SessionStartedData{ThreadID: "pre", Model: "m"},
	}
	if err := s.Append(pre); err != nil {
		t.Fatalf("Append pre: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := s.Tail(ctx)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}

	// Write a new event after tailing starts.
	post := monitor.AgentEvent{
		Type:      monitor.EventTurnStarted,
		Timestamp: time.Now().Truncate(time.Millisecond),
	}
	// Small delay to let the tail goroutine start.
	time.Sleep(50 * time.Millisecond)
	if err := s.Append(post); err != nil {
		t.Fatalf("Append post: %v", err)
	}

	// Should receive the post event.
	select {
	case ev := <-ch:
		if ev.Type != monitor.EventTurnStarted {
			t.Errorf("type = %v, want EventTurnStarted", ev.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tail event")
	}

	// Cancel and verify channel closes.
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			// Might get one more event, drain it.
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel close")
	}
}

func TestTail_CancelStopsImmediately(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := s.Tail(ctx)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}

	cancel()

	// Channel should close promptly.
	select {
	case <-ch:
		// ok, closed
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel close after cancel")
	}
}

func TestClose(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Double close should return an error (file already closed).
	if err := s.Close(); err == nil {
		t.Error("expected error on double Close")
	}
}
