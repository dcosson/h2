package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"h2/internal/config"
	"h2/internal/session/agent/monitor"
	"h2/internal/session/agent/shared/sessionlogcollector"
)

// rolloutLine builds one Codex rollout JSONL line with the given top-level
// type and payload.
func rolloutLine(t *testing.T, typ string, payload map[string]any) []byte {
	t.Helper()
	line, err := json.Marshal(map[string]any{
		"timestamp": "2026-07-07T16:31:01.642Z",
		"type":      typ,
		"payload":   payload,
	})
	if err != nil {
		t.Fatalf("marshal rollout line: %v", err)
	}
	return line
}

func TestEventHandler_OnSessionLogLine_AgentMessage(t *testing.T) {
	events := make(chan monitor.AgentEvent, 8)
	p := NewEventHandler(events)

	line := rolloutLine(t, "event_msg", map[string]any{
		"type":    "agent_message",
		"message": "Confirmed with concierge-leaf.",
		"phase":   "final_answer",
	})
	p.OnSessionLogLine(line)

	got := drainEvents(events, 1)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].Type != monitor.EventAgentMessage {
		t.Fatalf("Type = %v, want EventAgentMessage", got[0].Type)
	}
	if c := got[0].Data.(monitor.AgentMessageData).Content; c != "Confirmed with concierge-leaf." {
		t.Fatalf("Content = %q, want the assistant message text", c)
	}
}

// Commentary is interstitial assistant text (preamble before/between tools).
// It is visible conversation, so peek must show it — matching Claude, which
// emits every assistant text block.
func TestEventHandler_OnSessionLogLine_Commentary(t *testing.T) {
	events := make(chan monitor.AgentEvent, 8)
	p := NewEventHandler(events)

	line := rolloutLine(t, "event_msg", map[string]any{
		"type":    "agent_message",
		"message": "Let me check the logs first.",
		"phase":   "commentary",
	})
	p.OnSessionLogLine(line)

	got := drainEvents(events, 1)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if c := got[0].Data.(monitor.AgentMessageData).Content; c != "Let me check the logs first." {
		t.Fatalf("Content = %q, want the commentary text", c)
	}
}

// The user's own prompt (event_msg/user_message) must not be re-emitted as an
// agent message.
func TestEventHandler_OnSessionLogLine_UserMessage_NoEmit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 8)
	p := NewEventHandler(events)

	line := rolloutLine(t, "event_msg", map[string]any{
		"type":    "user_message",
		"message": "please run the tests",
	})
	p.OnSessionLogLine(line)

	if got := drainEvents(events, 1); len(got) != 0 {
		t.Fatalf("got %d events, want 0 for user_message", len(got))
	}
}

// The assistant text also appears as a response_item/message. We parse only the
// flat event_msg/agent_message form, so the response_item form must not emit —
// otherwise every message would be counted twice.
func TestEventHandler_OnSessionLogLine_ResponseItemMessage_NoEmit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 8)
	p := NewEventHandler(events)

	line := rolloutLine(t, "response_item", map[string]any{
		"type": "message",
		"role": "assistant",
		"content": []map[string]any{
			{"type": "output_text", "text": "Confirmed with concierge-leaf."},
		},
		"phase": "final_answer",
	})
	p.OnSessionLogLine(line)

	if got := drainEvents(events, 1); len(got) != 0 {
		t.Fatalf("got %d events, want 0 for response_item message", len(got))
	}
}

func TestEventHandler_OnSessionLogLine_NonMessageEvent_NoEmit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 8)
	p := NewEventHandler(events)

	for _, typ := range []string{"token_count", "task_started", "task_complete", "context_compacted"} {
		line := rolloutLine(t, "event_msg", map[string]any{"type": typ})
		p.OnSessionLogLine(line)
	}

	if got := drainEvents(events, 1); len(got) != 0 {
		t.Fatalf("got %d events, want 0 for non-message event_msgs", len(got))
	}
}

func TestEventHandler_OnSessionLogLine_EmptyMessage_NoEmit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 8)
	p := NewEventHandler(events)

	line := rolloutLine(t, "event_msg", map[string]any{
		"type":    "agent_message",
		"message": "",
		"phase":   "final_answer",
	})
	p.OnSessionLogLine(line)

	if got := drainEvents(events, 1); len(got) != 0 {
		t.Fatalf("got %d events, want 0 for empty message", len(got))
	}
}

func TestEventHandler_OnSessionLogLine_InvalidJSON_NoEmit(t *testing.T) {
	events := make(chan monitor.AgentEvent, 8)
	p := NewEventHandler(events)

	p.OnSessionLogLine([]byte("not json"))

	if got := drainEvents(events, 1); len(got) != 0 {
		t.Fatalf("got %d events, want 0 for invalid json", len(got))
	}
}

// End-to-end: the shared tailer reads a rollout file and the parser emits one
// EventAgentMessage per agent_message, in order, ignoring the surrounding
// session_meta / user_message / token_count noise.
func TestEventHandler_SessionLogCollector_EmitsAgentMessages(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "rollout.jsonl")

	lines := [][]byte{
		rolloutLine(t, "session_meta", map[string]any{"session_id": "conv-1"}),
		rolloutLine(t, "event_msg", map[string]any{"type": "user_message", "message": "hello"}),
		rolloutLine(t, "event_msg", map[string]any{"type": "agent_message", "message": "First reply.", "phase": "commentary"}),
		rolloutLine(t, "event_msg", map[string]any{"type": "token_count"}),
		rolloutLine(t, "event_msg", map[string]any{"type": "agent_message", "message": "Second reply.", "phase": "final_answer"}),
	}
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		f.Write(l)
		f.Write([]byte("\n"))
	}
	f.Close()

	events := make(chan monitor.AgentEvent, 64)
	p := NewEventHandler(events)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sessionlogcollector.New(logPath, p.OnSessionLogLine).Run(ctx)

	got := drainEventsTimeout(events, 2, 2*time.Second)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if c := got[0].Data.(monitor.AgentMessageData).Content; c != "First reply." {
		t.Fatalf("event[0].Content = %q, want 'First reply.'", c)
	}
	if c := got[1].Data.(monitor.AgentMessageData).Content; c != "Second reply." {
		t.Fatalf("event[1].Content = %q, want 'Second reply.'", c)
	}
}

// TestHarness_TailsSessionLogAfterConversationStarts is the full harness wiring
// test: PrepareForLaunch registers the conversation-started callback, a real
// codex.conversation_starts OTEL log arrives, the harness globs the rollout
// file, tails it, and emits the assistant message text as an EventAgentMessage.
func TestHarness_TailsSessionLogAfterConversationStarts(t *testing.T) {
	prefix := t.TempDir()
	configDir := filepath.Join(prefix, "default") // Profile "default"
	convID := "019f3d5f-3dc4-7f01-b42c-d19d98e1d13d"

	// Write the rollout file where the harness's glob will find it:
	//   <configDir>/sessions/<Y>/<M>/<D>/rollout-<ts>-<convID>.jsonl
	logDir := filepath.Join(configDir, "sessions", "2026", "07", "07")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(logDir, "rollout-2026-07-07T09-17-59-"+convID+".jsonl")
	rollout := [][]byte{
		rolloutLine(t, "session_meta", map[string]any{"session_id": convID}),
		rolloutLine(t, "event_msg", map[string]any{"type": "agent_message", "message": "Task done, all tests pass.", "phase": "final_answer"}),
	}
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range rollout {
		f.Write(l)
		f.Write([]byte("\n"))
	}
	f.Close()

	h := New(&config.RuntimeConfig{
		HarnessType:             "codex",
		Command:                 "codex",
		AgentName:               "test",
		CWD:                     "/tmp",
		StartedAt:               "2024-01-01T00:00:00Z",
		HarnessConfigPathPrefix: prefix,
		Profile:                 "default",
	}, nil)

	if _, err := h.PrepareForLaunch(false); err != nil {
		t.Fatalf("PrepareForLaunch: %v", err)
	}
	defer h.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan monitor.AgentEvent, 64)
	go h.Start(ctx, events)
	time.Sleep(20 * time.Millisecond)

	// Drive the conversation-started event through the real OTEL server.
	postLog(t, fmt.Sprintf("http://127.0.0.1:%d/v1/logs", h.OtelPort()),
		"codex.conversation_starts", []otelAttribute{
			{Key: "conversation.id", Value: otelAttrValue{StringValue: convID}},
			{Key: "model", Value: otelAttrValue{StringValue: "gpt-5-codex"}},
		})

	// The suffix should be discovered from the glob.
	waitFor(t, 2*time.Second, "native log path suffix", func() bool {
		return h.rc.NativeLogPathSuffix != ""
	})

	// The tailer should emit the agent message from the rollout file.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Type == monitor.EventAgentMessage {
				if c := ev.Data.(monitor.AgentMessageData).Content; c != "Task done, all tests pass." {
					t.Fatalf("Content = %q, want the assistant message text", c)
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for EventAgentMessage from the tailed rollout log")
		}
	}
}

// A Codex sub-agent inherits the top-level process's OTEL exporter and emits
// codex.conversation_starts to the same h2 collector. Those child events must
// not replace the top-level conversation ID or rollout path used by resume and
// profile rotation.
func TestHarness_ConversationStartsIgnoresSubagent(t *testing.T) {
	prefix := t.TempDir()
	configDir := filepath.Join(prefix, "default")
	logDir := filepath.Join(configDir, "sessions", "2026", "08", "06")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeSessionMeta := func(conversationID, threadSource string) string {
		t.Helper()
		path := filepath.Join(logDir, "rollout-2026-08-06T15-00-00-"+conversationID+".jsonl")
		line := rolloutLine(t, "session_meta", map[string]any{
			"session_id":    conversationID,
			"thread_source": threadSource,
		})
		if err := os.WriteFile(path, append(line, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	rootID := "019fd93b-fc6d-72e0-af81-da72a747ddd5"
	childID := "019fd93c-1111-7222-8333-da72a747ddd5"
	rootPath := writeSessionMeta(rootID, "user")
	writeSessionMeta(childID, "subagent")

	h := New(&config.RuntimeConfig{
		HarnessType:             "codex",
		Command:                 "codex",
		AgentName:               "test",
		SessionID:               "h2-session",
		HarnessConfigPathPrefix: prefix,
		Profile:                 "default",
	}, nil)
	if _, err := h.PrepareForLaunch(false); err != nil {
		t.Fatalf("PrepareForLaunch: %v", err)
	}
	defer h.Stop()

	h.eventHandler.processEvent("codex.conversation_starts", []otelAttribute{
		{Key: "conversation.id", Value: otelAttrValue{StringValue: rootID}},
	}, time.Now())
	h.eventHandler.processEvent("codex.conversation_starts", []otelAttribute{
		{Key: "conversation.id", Value: otelAttrValue{StringValue: childID}},
	}, time.Now())

	wantSuffix, err := filepath.Rel(configDir, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if h.rc.NativeLogPathSuffix != wantSuffix {
		t.Errorf("NativeLogPathSuffix = %q, want top-level rollout %q", h.rc.NativeLogPathSuffix, wantSuffix)
	}
	if got := len(h.internalCh); got != 2 {
		t.Errorf("emitted %d events, want only the top-level SessionStarted and Idle events", got)
	}
}

// `codex resume <id>` does not reopen the old conversation — it forks a new one
// with a new ID. h2 must adopt that fork as the session identity, or the next
// crash resumes from the pre-crash fork point and everything since is lost.
func TestHarness_ConversationStartsAdoptsResumeFork(t *testing.T) {
	prefix := t.TempDir()
	configDir := filepath.Join(prefix, "default")
	forkPath := writeRollout(t, configDir, "fork-1", map[string]any{
		"session_id":     "fork-1",
		"forked_from_id": "root-1",
		"thread_source":  "user",
	})
	writeRollout(t, configDir, "child-1", map[string]any{
		"session_id":       "fork-1",
		"parent_thread_id": "fork-1",
		"thread_source":    "subagent",
	})

	h := newTestHarness(t, prefix, &config.RuntimeConfig{
		SessionID:       "h2-session",
		ResumeSessionID: "root-1",
	})
	if _, err := h.PrepareForLaunch(false); err != nil {
		t.Fatalf("PrepareForLaunch: %v", err)
	}
	defer h.Stop()

	// The resumed conversation reports its own new ID.
	h.eventHandler.processEvent("codex.conversation_starts", []otelAttribute{
		{Key: "conversation.id", Value: otelAttrValue{StringValue: "fork-1"}},
	}, time.Now())
	// A sub-agent spawned inside it must still be ignored.
	h.eventHandler.processEvent("codex.conversation_starts", []otelAttribute{
		{Key: "conversation.id", Value: otelAttrValue{StringValue: "child-1"}},
	}, time.Now())

	if h.topLevelConversationID != "fork-1" {
		t.Errorf("topLevelConversationID = %q, want the resumed fork %q", h.topLevelConversationID, "fork-1")
	}
	wantSuffix, err := filepath.Rel(configDir, forkPath)
	if err != nil {
		t.Fatal(err)
	}
	if h.rc.NativeLogPathSuffix != wantSuffix {
		t.Errorf("NativeLogPathSuffix = %q, want the fork's rollout %q", h.rc.NativeLogPathSuffix, wantSuffix)
	}
	if got := len(h.internalCh); got != 2 {
		t.Errorf("emitted %d events, want only the fork's SessionStarted and Idle events", got)
	}
}

// A conversation with no rollout metadata to vouch for it must not take over an
// established session identity — that is how a sub-agent used to win.
func TestHarness_ConversationStartsIgnoresUnidentifiedChild(t *testing.T) {
	noRolloutRetries(t)
	prefix := t.TempDir()
	configDir := filepath.Join(prefix, "default")
	writeRollout(t, configDir, "root-1", map[string]any{"session_id": "root-1", "thread_source": "user"})

	h := newTestHarness(t, prefix, &config.RuntimeConfig{SessionID: "h2-session"})
	if _, err := h.PrepareForLaunch(false); err != nil {
		t.Fatalf("PrepareForLaunch: %v", err)
	}
	defer h.Stop()

	h.eventHandler.processEvent("codex.conversation_starts", []otelAttribute{
		{Key: "conversation.id", Value: otelAttrValue{StringValue: "root-1"}},
	}, time.Now())
	h.eventHandler.processEvent("codex.conversation_starts", []otelAttribute{
		{Key: "conversation.id", Value: otelAttrValue{StringValue: "no-rollout-1"}},
	}, time.Now())

	if h.topLevelConversationID != "root-1" {
		t.Errorf("topLevelConversationID = %q, want %q", h.topLevelConversationID, "root-1")
	}
}

// After a relaunch the tailer must follow the resumed conversation's new
// rollout file, not keep reading the pre-crash one.
func TestHarness_TailerFollowsRolloutAcrossRelaunch(t *testing.T) {
	prefix := t.TempDir()
	configDir := filepath.Join(prefix, "default")
	firstPath := writeRollout(t, configDir, "root-1", map[string]any{"session_id": "root-1", "thread_source": "user"})
	secondPath := writeRollout(t, configDir, "fork-1", map[string]any{
		"session_id":     "fork-1",
		"forked_from_id": "root-1",
		"thread_source":  "user",
	})

	h := newTestHarness(t, prefix, &config.RuntimeConfig{SessionID: "h2-session"})
	if _, err := h.PrepareForLaunch(false); err != nil {
		t.Fatalf("PrepareForLaunch: %v", err)
	}
	defer h.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan monitor.AgentEvent, 64)
	go h.Start(ctx, events)
	time.Sleep(20 * time.Millisecond)

	h.eventHandler.processEvent("codex.conversation_starts", []otelAttribute{
		{Key: "conversation.id", Value: otelAttrValue{StringValue: "root-1"}},
	}, time.Now())
	appendRolloutMessage(t, firstPath, "First conversation reply.")
	waitForAgentMessage(t, events, "First conversation reply.")

	// Relaunch: Codex forks a new conversation with its own rollout file.
	h.eventHandler.processEvent("codex.conversation_starts", []otelAttribute{
		{Key: "conversation.id", Value: otelAttrValue{StringValue: "fork-1"}},
	}, time.Now())
	time.Sleep(150 * time.Millisecond) // let the new tailer attach

	// The pre-crash rollout must no longer be tailed; the new one must be.
	appendRolloutMessage(t, firstPath, "Stale conversation reply.")
	appendRolloutMessage(t, secondPath, "Reply after relaunch.")
	waitForAgentMessage(t, events, "Reply after relaunch.", "Stale conversation reply.")
}

// If the rollout file is still missing when the conversation is adopted, a
// later event for the same conversation must retry discovery — otherwise the
// session runs without a tailer for its whole life.
func TestHarness_ConversationStartsRetriesRolloutDiscovery(t *testing.T) {
	noRolloutRetries(t)
	prefix := t.TempDir()
	configDir := filepath.Join(prefix, "default")

	h := newTestHarness(t, prefix, &config.RuntimeConfig{SessionID: "h2-session"})
	if _, err := h.PrepareForLaunch(false); err != nil {
		t.Fatalf("PrepareForLaunch: %v", err)
	}
	defer h.Stop()

	h.eventHandler.processEvent("codex.conversation_starts", []otelAttribute{
		{Key: "conversation.id", Value: otelAttrValue{StringValue: "root-1"}},
	}, time.Now())
	if h.topLevelConversationID != "root-1" {
		t.Fatalf("topLevelConversationID = %q, want the first conversation adopted", h.topLevelConversationID)
	}
	if h.rc.NativeLogPathSuffix != "" {
		t.Fatalf("NativeLogPathSuffix = %q, want empty while the rollout is missing", h.rc.NativeLogPathSuffix)
	}

	path := writeRollout(t, configDir, "root-1", map[string]any{"session_id": "root-1", "thread_source": "user"})
	h.eventHandler.processEvent("codex.conversation_starts", []otelAttribute{
		{Key: "conversation.id", Value: otelAttrValue{StringValue: "root-1"}},
	}, time.Now())

	wantSuffix, err := filepath.Rel(configDir, path)
	if err != nil {
		t.Fatal(err)
	}
	if h.rc.NativeLogPathSuffix != wantSuffix {
		t.Errorf("NativeLogPathSuffix = %q, want %q after the rollout appeared", h.rc.NativeLogPathSuffix, wantSuffix)
	}
}

func appendRolloutMessage(t *testing.T, path, message string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	line := rolloutLine(t, "event_msg", map[string]any{
		"type": "agent_message", "message": message, "phase": "final_answer",
	})
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
}

// waitForAgentMessage waits for an agent message with the wanted content,
// failing if any of the forbidden messages arrives first.
func waitForAgentMessage(t *testing.T, events chan monitor.AgentEvent, want string, forbidden ...string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Type != monitor.EventAgentMessage {
				continue
			}
			c := ev.Data.(monitor.AgentMessageData).Content
			if c == want {
				return
			}
			for _, bad := range forbidden {
				if c == bad {
					t.Fatalf("got agent message %q from a rollout that should no longer be tailed", c)
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for agent message %q", want)
		}
	}
}

func drainEventsTimeout(ch chan monitor.AgentEvent, n int, d time.Duration) []monitor.AgentEvent {
	var events []monitor.AgentEvent
	timeout := time.After(d)
	for len(events) < n {
		select {
		case ev := <-ch:
			events = append(events, ev)
		case <-timeout:
			return events
		}
	}
	return events
}
