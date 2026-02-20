package bridgeservice

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"h2/internal/bridge"
	"h2/internal/session/message"
	"h2/internal/socketdir"
)

// --- Mock bridges ---

// mockSender records messages sent through it.
type mockSender struct {
	name     string
	messages []string
	mu       sync.Mutex
}

func (m *mockSender) Name() string { return m.name }
func (m *mockSender) Close() error { return nil }
func (m *mockSender) Send(_ context.Context, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, text)
	return nil
}
func (m *mockSender) Messages() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.messages...)
}

// mockTypingBridge implements Bridge, Sender, and TypingIndicator.
type mockTypingBridge struct {
	name        string
	typingCalls int
	mu          sync.Mutex
}

func (m *mockTypingBridge) Name() string { return m.name }
func (m *mockTypingBridge) Close() error { return nil }
func (m *mockTypingBridge) Send(_ context.Context, text string) error {
	return nil
}
func (m *mockTypingBridge) SendTyping(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.typingCalls++
	return nil
}
func (m *mockTypingBridge) TypingCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.typingCalls
}

// mockReceiver exposes its handler so tests can simulate inbound messages.
type mockReceiver struct {
	name    string
	handler bridge.InboundHandler
	started bool
	stopped bool
}

func (m *mockReceiver) Name() string { return m.name }
func (m *mockReceiver) Close() error { return nil }
func (m *mockReceiver) Start(_ context.Context, h bridge.InboundHandler) error {
	m.handler = h
	m.started = true
	return nil
}
func (m *mockReceiver) Stop() { m.stopped = true }

// --- Mock agent socket ---

// mockAgent creates a Unix socket that mimics an agent, recording received requests.
type mockAgent struct {
	listener net.Listener
	received []message.Request
	mu       sync.Mutex
	wg       sync.WaitGroup
}

func newMockAgent(t *testing.T, socketDir, name string) *mockAgent {
	t.Helper()
	sockPath := filepath.Join(socketDir, socketdir.Format(socketdir.TypeAgent, name))
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	a := &mockAgent{listener: ln}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			a.wg.Add(1)
			go func() {
				defer a.wg.Done()
				defer conn.Close()
				req, err := message.ReadRequest(conn)
				if err != nil {
					return
				}
				a.mu.Lock()
				a.received = append(a.received, *req)
				a.mu.Unlock()
				message.SendResponse(conn, &message.Response{OK: true, MessageID: "test-id"})
			}()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		a.wg.Wait()
	})
	return a
}

func (a *mockAgent) Received() []message.Request {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]message.Request(nil), a.received...)
}

// mockStatusAgent creates a Unix socket that responds to "status" requests
// with a configurable state. It also handles "send" like mockAgent.
type mockStatusAgent struct {
	listener net.Listener
	state    string
	mu       sync.Mutex
	wg       sync.WaitGroup
}

func newMockStatusAgent(t *testing.T, socketDir, name, state string) *mockStatusAgent {
	t.Helper()
	sockPath := filepath.Join(socketDir, socketdir.Format(socketdir.TypeAgent, name))
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	a := &mockStatusAgent{listener: ln, state: state}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			a.wg.Add(1)
			go func() {
				defer a.wg.Done()
				defer conn.Close()
				req, err := message.ReadRequest(conn)
				if err != nil {
					return
				}
				switch req.Type {
				case "status":
					a.mu.Lock()
					st := a.state
					a.mu.Unlock()
					message.SendResponse(conn, &message.Response{
						OK: true,
						Agent: &message.AgentInfo{
							Name:  name,
							State: st,
						},
					})
				case "send":
					message.SendResponse(conn, &message.Response{OK: true, MessageID: "test-id"})
				}
			}()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		a.wg.Wait()
	})
	return a
}

func (a *mockStatusAgent) SetState(state string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = state
}

// --- Helpers ---

// shortTempDir creates a temp directory with a short path suitable for Unix sockets
// (macOS has a ~104 byte path limit for socket addresses).
// Includes a truncated test name for debuggability.
func shortTempDir(t *testing.T) string {
	t.Helper()
	name := t.Name()
	if len(name) > 20 {
		name = name[:20]
	}
	dir, err := os.MkdirTemp("/tmp", "h2t-"+name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("socket %s did not appear", path)
}

// --- Inbound routing tests ---

func TestHandleInbound_AddressedMessage(t *testing.T) {
	tmpDir := shortTempDir(t)
	agent := newMockAgent(t, tmpDir, "myagent")
	svc := New(nil, "concierge", tmpDir, "alice", nil)

	svc.handleInbound("myagent", "hello agent")

	reqs := agent.Received()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	if reqs[0].Type != "send" {
		t.Errorf("expected type=send, got %q", reqs[0].Type)
	}
	if reqs[0].From != "alice" {
		t.Errorf("expected from=alice, got %q", reqs[0].From)
	}
	if reqs[0].Body != "hello agent" {
		t.Errorf("expected body='hello agent', got %q", reqs[0].Body)
	}
}

func TestHandleInbound_UnaddressedWithConcierge(t *testing.T) {
	tmpDir := shortTempDir(t)
	concierge := newMockAgent(t, tmpDir, "concierge")
	svc := New(nil, "concierge", tmpDir, "alice", nil)

	svc.handleInbound("", "unaddressed message")

	reqs := concierge.Received()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request to concierge, got %d", len(reqs))
	}
	if reqs[0].Body != "unaddressed message" {
		t.Errorf("expected body='unaddressed message', got %q", reqs[0].Body)
	}
}

func TestHandleInbound_UnaddressedNoConciergeLastSender(t *testing.T) {
	tmpDir := shortTempDir(t)
	agent := newMockAgent(t, tmpDir, "agent1")
	svc := New(nil, "", tmpDir, "alice", nil) // no concierge
	svc.lastSender = "agent1"

	svc.handleInbound("", "reply to last sender")

	reqs := agent.Received()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request to agent1, got %d", len(reqs))
	}
	if reqs[0].Body != "reply to last sender" {
		t.Errorf("expected body='reply to last sender', got %q", reqs[0].Body)
	}
}

func TestHandleInbound_UnaddressedNoConciergeFirstAgent(t *testing.T) {
	tmpDir := shortTempDir(t)
	// Create two agents — "alpha" should be picked (alphabetically first via os.ReadDir).
	alpha := newMockAgent(t, tmpDir, "alpha")
	_ = newMockAgent(t, tmpDir, "beta")
	svc := New(nil, "", tmpDir, "alice", nil) // no concierge, no lastSender

	svc.handleInbound("", "fallback message")

	reqs := alpha.Received()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request to alpha, got %d", len(reqs))
	}
	if reqs[0].Body != "fallback message" {
		t.Errorf("expected body='fallback message', got %q", reqs[0].Body)
	}
}

// --- Error reply tests ---

func TestHandleInbound_DeadAgentRepliesWithError(t *testing.T) {
	tmpDir := shortTempDir(t)
	sender := &mockSender{name: "telegram"}
	svc := New([]bridge.Bridge{sender}, "concierge", tmpDir, "alice", nil)

	// No concierge agent socket exists — send should fail.
	svc.handleInbound("", "hello?")

	msgs := sender.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 error reply, got %d", len(msgs))
	}
	if msgs[0] != "concierge agent is not running, unable to deliver message." {
		t.Errorf("unexpected reply: %q", msgs[0])
	}
}

func TestHandleInbound_ExplicitDeadAgentRepliesWithError(t *testing.T) {
	tmpDir := shortTempDir(t)
	sender := &mockSender{name: "telegram"}
	svc := New([]bridge.Bridge{sender}, "", tmpDir, "alice", nil)

	// Explicitly target a non-existent agent.
	svc.handleInbound("foo", "hello foo")

	msgs := sender.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 error reply, got %d", len(msgs))
	}
	if msgs[0] != "foo agent is not running, unable to deliver message." {
		t.Errorf("unexpected reply: %q", msgs[0])
	}
}

func TestHandleInbound_NoAgentsRepliesWithError(t *testing.T) {
	tmpDir := shortTempDir(t)
	sender := &mockSender{name: "telegram"}
	svc := New([]bridge.Bridge{sender}, "", tmpDir, "alice", nil) // no concierge

	// No agents at all.
	svc.handleInbound("", "anyone there?")

	msgs := sender.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 error reply, got %d", len(msgs))
	}
	if msgs[0] != "No agents are running, unable to deliver message." {
		t.Errorf("unexpected reply: %q", msgs[0])
	}
}

// --- Outbound tests ---

func TestHandleOutbound(t *testing.T) {
	sender1 := &mockSender{name: "telegram"}
	sender2 := &mockSender{name: "macos"}
	recv := &mockReceiver{name: "recv-only"} // should not receive sends
	svc := New(
		[]bridge.Bridge{sender1, sender2, recv},
		"", t.TempDir(), "alice", nil,
	)

	svc.sendOutbound("myagent", "build complete")

	// Both senders should have received the tagged message (non-concierge agent).
	want := "[myagent] build complete"
	msgs1 := sender1.Messages()
	if len(msgs1) != 1 || msgs1[0] != want {
		t.Errorf("sender1: expected [%s], got %v", want, msgs1)
	}
	msgs2 := sender2.Messages()
	if len(msgs2) != 1 || msgs2[0] != want {
		t.Errorf("sender2: expected [%s], got %v", want, msgs2)
	}

	// lastSender should be updated.
	svc.mu.Lock()
	last := svc.lastSender
	svc.mu.Unlock()
	if last != "myagent" {
		t.Errorf("expected lastSender=myagent, got %q", last)
	}
}

func TestHandleOutbound_TagsNonConcierge(t *testing.T) {
	sender := &mockSender{name: "telegram"}
	svc := New([]bridge.Bridge{sender}, "concierge", t.TempDir(), "alice", nil)

	svc.sendOutbound("researcher", "here are the results")

	msgs := sender.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	want := "[researcher] here are the results"
	if msgs[0] != want {
		t.Errorf("got %q, want %q", msgs[0], want)
	}
}

func TestHandleOutbound_NoConciergeTag(t *testing.T) {
	sender := &mockSender{name: "telegram"}
	svc := New([]bridge.Bridge{sender}, "concierge", t.TempDir(), "alice", nil)

	svc.sendOutbound("concierge", "build complete")

	msgs := sender.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	// Concierge messages should NOT be tagged.
	if msgs[0] != "build complete" {
		t.Errorf("got %q, want %q", msgs[0], "build complete")
	}
}

// --- Socket listener test ---

func TestSocketListener(t *testing.T) {
	tmpDir := shortTempDir(t)
	sender := &mockSender{name: "test"}
	svc := New([]bridge.Bridge{sender}, "", tmpDir, "alice", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()

	sockPath := filepath.Join(tmpDir, socketdir.Format(socketdir.TypeBridge, "alice"))
	waitForSocket(t, sockPath)

	// Connect to the bridge socket and send a message.
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := message.SendRequest(conn, &message.Request{
		Type: "send",
		From: "agent1",
		Body: "hello human",
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := message.ReadResponse(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Errorf("expected OK response, got error: %s", resp.Error)
	}

	// Give sendOutbound a moment to complete (it runs synchronously in handleConn,
	// but the response is sent after sendOutbound returns, so by now it's done).
	// Non-concierge agents get tagged with [agent-name].
	// First message is the startup status; second is the outbound message.
	msgs := sender.Messages()
	wantMsg := "[agent1] hello human"
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages (startup + outbound), got %d: %v", len(msgs), msgs)
	}
	lastMsg := msgs[len(msgs)-1]
	if lastMsg != wantMsg {
		t.Errorf("expected last message to be %q, got %q", wantMsg, lastMsg)
	}

	svc.mu.Lock()
	last := svc.lastSender
	svc.mu.Unlock()
	if last != "agent1" {
		t.Errorf("expected lastSender=agent1, got %q", last)
	}

	cancel()
	<-errCh
}

// --- Stop request test ---

func TestStopRequest_ShutdownService(t *testing.T) {
	tmpDir := shortTempDir(t)
	sender := &mockSender{name: "test"}
	svc := New([]bridge.Bridge{sender}, "", tmpDir, "alice", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()

	sockPath := filepath.Join(tmpDir, socketdir.Format(socketdir.TypeBridge, "alice"))
	waitForSocket(t, sockPath)

	// Send stop request.
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := message.SendRequest(conn, &message.Request{Type: "stop"}); err != nil {
		t.Fatal(err)
	}

	resp, err := message.ReadResponse(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Errorf("expected OK response, got error: %s", resp.Error)
	}

	// Run should return because the stop cancelled the context.
	if err := <-errCh; err != nil {
		t.Errorf("unexpected error from Run: %v", err)
	}

	// Socket should be cleaned up by Run's deferred cleanup.
	if _, err := os.Stat(sockPath); err == nil {
		t.Error("expected socket to be removed after stop")
	}
}

// --- Run lifecycle test ---

func TestRunStartsAndStopsReceivers(t *testing.T) {
	tmpDir := shortTempDir(t)
	recv := &mockReceiver{name: "test-recv"}
	svc := New([]bridge.Bridge{recv}, "concierge", tmpDir, "alice", nil)

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()

	sockPath := filepath.Join(tmpDir, socketdir.Format(socketdir.TypeBridge, "alice"))
	waitForSocket(t, sockPath)

	if !recv.started {
		t.Error("receiver was not started")
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !recv.stopped {
		t.Error("receiver was not stopped")
	}
}

// --- resolveDefaultTarget tests ---

func TestResolveDefaultTarget_Concierge(t *testing.T) {
	svc := New(nil, "concierge", t.TempDir(), "alice", nil)
	if got := svc.resolveDefaultTarget(); got != "concierge" {
		t.Errorf("expected concierge, got %q", got)
	}
}

func TestResolveDefaultTarget_LastSender(t *testing.T) {
	svc := New(nil, "", t.TempDir(), "alice", nil)
	svc.lastSender = "agent1"
	if got := svc.resolveDefaultTarget(); got != "agent1" {
		t.Errorf("expected agent1, got %q", got)
	}
}

func TestResolveDefaultTarget_FirstAgent(t *testing.T) {
	tmpDir := t.TempDir()
	// Create fake socket files (don't need real listeners for this test).
	os.WriteFile(filepath.Join(tmpDir, socketdir.Format(socketdir.TypeAgent, "alpha")), nil, 0o600)
	os.WriteFile(filepath.Join(tmpDir, socketdir.Format(socketdir.TypeAgent, "beta")), nil, 0o600)
	os.WriteFile(filepath.Join(tmpDir, socketdir.Format(socketdir.TypeBridge, "alice")), nil, 0o600)

	svc := New(nil, "", tmpDir, "alice", nil)
	if got := svc.resolveDefaultTarget(); got != "alpha" {
		t.Errorf("expected alpha, got %q", got)
	}
}

func TestResolveDefaultTarget_NoAgents(t *testing.T) {
	svc := New(nil, "", t.TempDir(), "alice", nil)
	if got := svc.resolveDefaultTarget(); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// --- Typing loop tests ---

func TestTypingLoop_SendsWhenActive(t *testing.T) {
	typingTickInterval = 50 * time.Millisecond
	defer func() { typingTickInterval = 4 * time.Second }()

	tmpDir := shortTempDir(t)
	_ = newMockStatusAgent(t, tmpDir, "concierge", "active")

	tb := &mockTypingBridge{name: "telegram"}
	svc := New([]bridge.Bridge{tb}, "concierge", tmpDir, "alice", nil)

	ctx, cancel := context.WithCancel(context.Background())
	go svc.runTypingLoop(ctx)

	time.Sleep(200 * time.Millisecond)
	cancel()

	calls := tb.TypingCalls()
	if calls < 2 {
		t.Errorf("expected >= 2 typing calls when active, got %d", calls)
	}
}

func TestTypingLoop_SkipsWhenIdle(t *testing.T) {
	typingTickInterval = 50 * time.Millisecond
	defer func() { typingTickInterval = 4 * time.Second }()

	tmpDir := shortTempDir(t)
	_ = newMockStatusAgent(t, tmpDir, "concierge", "idle")

	tb := &mockTypingBridge{name: "telegram"}
	svc := New([]bridge.Bridge{tb}, "concierge", tmpDir, "alice", nil)

	ctx, cancel := context.WithCancel(context.Background())
	go svc.runTypingLoop(ctx)

	time.Sleep(200 * time.Millisecond)
	cancel()

	calls := tb.TypingCalls()
	if calls != 0 {
		t.Errorf("expected 0 typing calls when idle, got %d", calls)
	}
}

func TestTypingLoop_SkipsWhenNoAgent(t *testing.T) {
	typingTickInterval = 50 * time.Millisecond
	defer func() { typingTickInterval = 4 * time.Second }()

	tmpDir := shortTempDir(t)
	// No agent socket exists.

	tb := &mockTypingBridge{name: "telegram"}
	svc := New([]bridge.Bridge{tb}, "concierge", tmpDir, "alice", nil)

	ctx, cancel := context.WithCancel(context.Background())
	go svc.runTypingLoop(ctx)

	time.Sleep(200 * time.Millisecond)
	cancel()

	calls := tb.TypingCalls()
	if calls != 0 {
		t.Errorf("expected 0 typing calls when no agent, got %d", calls)
	}
}

func TestTypingLoop_WorksWithoutConcierge(t *testing.T) {
	typingTickInterval = 50 * time.Millisecond
	defer func() { typingTickInterval = 4 * time.Second }()

	tmpDir := shortTempDir(t)
	_ = newMockStatusAgent(t, tmpDir, "myagent", "active")

	tb := &mockTypingBridge{name: "telegram"}
	svc := New([]bridge.Bridge{tb}, "", tmpDir, "alice", nil) // no concierge
	svc.lastSender = "myagent"                           // fallback target

	ctx, cancel := context.WithCancel(context.Background())
	go svc.runTypingLoop(ctx)

	time.Sleep(200 * time.Millisecond)
	cancel()

	calls := tb.TypingCalls()
	if calls < 2 {
		t.Errorf("expected >= 2 typing calls via fallback target, got %d", calls)
	}
}

// --- Status request tests ---

func TestStatusRequest(t *testing.T) {
	tmpDir := shortTempDir(t)
	sender := &mockSender{name: "telegram"}
	svc := New([]bridge.Bridge{sender}, "concierge", tmpDir, "alice", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()

	sockPath := filepath.Join(tmpDir, socketdir.Format(socketdir.TypeBridge, "alice"))
	waitForSocket(t, sockPath)

	// Send a message first to bump counters.
	conn1, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	message.SendRequest(conn1, &message.Request{Type: "send", From: "agent1", Body: "hello"})
	message.ReadResponse(conn1)
	conn1.Close()

	// Query status.
	conn2, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()

	if err := message.SendRequest(conn2, &message.Request{Type: "status"}); err != nil {
		t.Fatal(err)
	}

	resp, err := message.ReadResponse(conn2)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Errorf("expected OK, got error: %s", resp.Error)
	}
	if resp.Bridge == nil {
		t.Fatal("expected bridge info, got nil")
	}

	b := resp.Bridge
	if b.Name != "alice" {
		t.Errorf("expected name=alice, got %q", b.Name)
	}
	if len(b.Channels) != 1 || b.Channels[0] != "telegram" {
		t.Errorf("expected channels=[telegram], got %v", b.Channels)
	}
	if b.MessagesSent != 1 {
		t.Errorf("expected 1 sent, got %d", b.MessagesSent)
	}
	if b.Uptime == "" {
		t.Error("expected non-empty uptime")
	}
	if b.LastActivity == "" {
		t.Error("expected non-empty last_activity after sending a message")
	}

	cancel()
	<-errCh
}

func TestStatusRequest_MultipleChannels(t *testing.T) {
	tmpDir := shortTempDir(t)
	sender1 := &mockSender{name: "telegram"}
	sender2 := &mockSender{name: "macos"}
	svc := New([]bridge.Bridge{sender1, sender2}, "", tmpDir, "alice", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()

	sockPath := filepath.Join(tmpDir, socketdir.Format(socketdir.TypeBridge, "alice"))
	waitForSocket(t, sockPath)

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	message.SendRequest(conn, &message.Request{Type: "status"})
	resp, err := message.ReadResponse(conn)
	if err != nil {
		t.Fatal(err)
	}

	b := resp.Bridge
	if len(b.Channels) != 2 {
		t.Fatalf("expected 2 channels, got %v", b.Channels)
	}
	if b.Channels[0] != "telegram" || b.Channels[1] != "macos" {
		t.Errorf("expected [telegram, macos], got %v", b.Channels)
	}
	if b.MessagesSent != 0 {
		t.Errorf("expected 0 sent, got %d", b.MessagesSent)
	}
	if b.LastActivity != "" {
		t.Errorf("expected empty last_activity, got %q", b.LastActivity)
	}

	cancel()
	<-errCh
}

func TestStatusRequest_CountsInbound(t *testing.T) {
	tmpDir := shortTempDir(t)
	sender := &mockSender{name: "telegram"}
	recv := &mockReceiver{name: "telegram-recv"}
	agent := newMockAgent(t, tmpDir, "concierge")
	svc := New([]bridge.Bridge{sender, recv}, "concierge", tmpDir, "alice", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()

	sockPath := filepath.Join(tmpDir, socketdir.Format(socketdir.TypeBridge, "alice"))
	waitForSocket(t, sockPath)

	// Simulate inbound messages.
	recv.handler("concierge", "hello")
	recv.handler("concierge", "world")

	// Wait for messages to be delivered.
	time.Sleep(100 * time.Millisecond)
	_ = agent

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	message.SendRequest(conn, &message.Request{Type: "status"})
	resp, err := message.ReadResponse(conn)
	if err != nil {
		t.Fatal(err)
	}

	if resp.Bridge.MessagesReceived != 2 {
		t.Errorf("expected 2 received, got %d", resp.Bridge.MessagesReceived)
	}

	cancel()
	<-errCh
}

func TestTypingLoop_RespondsToStateChange(t *testing.T) {
	typingTickInterval = 50 * time.Millisecond
	defer func() { typingTickInterval = 4 * time.Second }()

	tmpDir := shortTempDir(t)
	agent := newMockStatusAgent(t, tmpDir, "concierge", "idle")

	tb := &mockTypingBridge{name: "telegram"}
	svc := New([]bridge.Bridge{tb}, "concierge", tmpDir, "alice", nil)

	ctx, cancel := context.WithCancel(context.Background())
	go svc.runTypingLoop(ctx)

	// Start idle — no typing calls.
	time.Sleep(150 * time.Millisecond)
	callsBefore := tb.TypingCalls()
	if callsBefore != 0 {
		t.Errorf("expected 0 typing calls while idle, got %d", callsBefore)
	}

	// Switch to active — typing calls should start.
	agent.SetState("active")
	time.Sleep(200 * time.Millisecond)
	cancel()

	callsAfter := tb.TypingCalls()
	if callsAfter < 2 {
		t.Errorf("expected >= 2 typing calls after becoming active, got %d", callsAfter)
	}
}

// --- sendBridgeStatus tests ---

func TestSendBridgeStatus(t *testing.T) {
	sender := &mockSender{name: "telegram"}
	svc := New([]bridge.Bridge{sender}, "", t.TempDir(), "alice", nil)

	svc.sendBridgeStatus(context.Background(), "Bridge is up.")

	msgs := sender.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	want := "[bridge alice] Bridge is up."
	if msgs[0] != want {
		t.Errorf("got %q, want %q", msgs[0], want)
	}
}

// --- handleSetConcierge tests ---

func TestHandleSetConcierge_NewConcierge(t *testing.T) {
	tmpDir := shortTempDir(t)
	sender := &mockSender{name: "telegram"}
	_ = newMockStatusAgent(t, tmpDir, "sage", "idle")
	svc := New([]bridge.Bridge{sender}, "", tmpDir, "alice", nil)

	resp := svc.handleSetConcierge("sage")

	if !resp.OK {
		t.Fatalf("expected OK, got error: %s", resp.Error)
	}
	if resp.OldConcierge != "" {
		t.Errorf("expected empty old concierge, got %q", resp.OldConcierge)
	}

	// Concierge should be set.
	svc.mu.Lock()
	got := svc.concierge
	svc.mu.Unlock()
	if got != "sage" {
		t.Errorf("expected concierge=sage, got %q", got)
	}

	// Should have sent a status message about adding concierge.
	msgs := sender.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 status message, got %d: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "Concierge added") {
		t.Errorf("expected 'Concierge added' in message, got %q", msgs[0])
	}
}

func TestHandleSetConcierge_ReplaceConcierge(t *testing.T) {
	tmpDir := shortTempDir(t)
	sender := &mockSender{name: "telegram"}
	_ = newMockStatusAgent(t, tmpDir, "new-agent", "idle")
	svc := New([]bridge.Bridge{sender}, "old-agent", tmpDir, "alice", nil)

	resp := svc.handleSetConcierge("new-agent")

	if !resp.OK {
		t.Fatalf("expected OK, got error: %s", resp.Error)
	}
	if resp.OldConcierge != "old-agent" {
		t.Errorf("expected old concierge=old-agent, got %q", resp.OldConcierge)
	}

	svc.mu.Lock()
	got := svc.concierge
	svc.mu.Unlock()
	if got != "new-agent" {
		t.Errorf("expected concierge=new-agent, got %q", got)
	}

	msgs := sender.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 status message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0], "Concierge changed") {
		t.Errorf("expected 'Concierge changed' in message, got %q", msgs[0])
	}
}

func TestHandleSetConcierge_EmptyName(t *testing.T) {
	svc := New(nil, "", t.TempDir(), "alice", nil)

	resp := svc.handleSetConcierge("")

	if resp.OK {
		t.Error("expected error for empty agent name")
	}
	if resp.Error != "agent name is required" {
		t.Errorf("unexpected error: %q", resp.Error)
	}
}

func TestHandleSetConcierge_ResetsLastRoutedAgent(t *testing.T) {
	tmpDir := shortTempDir(t)
	_ = newMockStatusAgent(t, tmpDir, "sage", "idle")
	svc := New(nil, "", tmpDir, "alice", nil)
	svc.mu.Lock()
	svc.lastRoutedAgent = "old-target"
	svc.mu.Unlock()

	svc.handleSetConcierge("sage")

	svc.mu.Lock()
	got := svc.lastRoutedAgent
	svc.mu.Unlock()
	if got != "" {
		t.Errorf("expected lastRoutedAgent to be cleared, got %q", got)
	}
}

// --- handleRemoveConcierge tests ---

func TestHandleRemoveConcierge(t *testing.T) {
	tmpDir := shortTempDir(t)
	sender := &mockSender{name: "telegram"}
	svc := New([]bridge.Bridge{sender}, "sage", tmpDir, "alice", nil)

	resp := svc.handleRemoveConcierge()

	if !resp.OK {
		t.Fatalf("expected OK, got error: %s", resp.Error)
	}

	svc.mu.Lock()
	got := svc.concierge
	svc.mu.Unlock()
	if got != "" {
		t.Errorf("expected concierge to be cleared, got %q", got)
	}

	msgs := sender.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 status message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0], "Concierge removed") {
		t.Errorf("expected 'Concierge removed' in message, got %q", msgs[0])
	}
}

func TestHandleRemoveConcierge_NoneSet(t *testing.T) {
	svc := New(nil, "", t.TempDir(), "alice", nil)

	resp := svc.handleRemoveConcierge()

	if resp.OK {
		t.Error("expected error when no concierge is set")
	}
	if resp.Error != "no concierge is set" {
		t.Errorf("unexpected error: %q", resp.Error)
	}
}

// --- handleConciergeDown tests ---

func TestHandleConciergeDown_ClearsConcierge(t *testing.T) {
	tmpDir := shortTempDir(t)
	sender := &mockSender{name: "telegram"}
	svc := New([]bridge.Bridge{sender}, "sage", tmpDir, "alice", nil)
	svc.mu.Lock()
	svc.lastRoutedAgent = "sage"
	svc.mu.Unlock()

	svc.handleConciergeDown(context.Background(), "sage")

	svc.mu.Lock()
	concierge := svc.concierge
	lastRouted := svc.lastRoutedAgent
	svc.mu.Unlock()

	if concierge != "" {
		t.Errorf("expected concierge to be cleared, got %q", concierge)
	}
	if lastRouted != "" {
		t.Errorf("expected lastRoutedAgent to be cleared, got %q", lastRouted)
	}

	msgs := sender.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 status message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0], "sage stopped") {
		t.Errorf("expected 'sage stopped' in message, got %q", msgs[0])
	}
}

// --- handleInbound lastRoutedAgent tests ---

func TestHandleInbound_SetsLastRoutedAgent(t *testing.T) {
	tmpDir := shortTempDir(t)
	_ = newMockAgent(t, tmpDir, "coder-1")
	svc := New(nil, "", tmpDir, "alice", nil)

	svc.handleInbound("coder-1", "hello")

	svc.mu.Lock()
	got := svc.lastRoutedAgent
	svc.mu.Unlock()
	if got != "coder-1" {
		t.Errorf("expected lastRoutedAgent=coder-1, got %q", got)
	}
}

func TestHandleInbound_DoesNotSetLastRoutedOnFailure(t *testing.T) {
	tmpDir := shortTempDir(t)
	sender := &mockSender{name: "telegram"}
	svc := New([]bridge.Bridge{sender}, "", tmpDir, "alice", nil)

	// Target agent doesn't exist — should fail.
	svc.handleInbound("nonexistent", "hello")

	svc.mu.Lock()
	got := svc.lastRoutedAgent
	svc.mu.Unlock()
	if got != "" {
		t.Errorf("expected lastRoutedAgent to remain empty on failure, got %q", got)
	}
}

// --- Socket-level set-concierge / remove-concierge tests ---

func TestSocketSetConcierge(t *testing.T) {
	tmpDir := shortTempDir(t)
	sender := &mockSender{name: "telegram"}
	_ = newMockStatusAgent(t, tmpDir, "sage", "idle")
	svc := New([]bridge.Bridge{sender}, "", tmpDir, "alice", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()

	sockPath := filepath.Join(tmpDir, socketdir.Format(socketdir.TypeBridge, "alice"))
	waitForSocket(t, sockPath)

	// Send set-concierge request.
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := message.SendRequest(conn, &message.Request{
		Type: "set-concierge",
		Body: "sage",
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := message.ReadResponse(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Errorf("expected OK, got error: %s", resp.Error)
	}

	// Concierge should be set.
	svc.mu.Lock()
	got := svc.concierge
	svc.mu.Unlock()
	if got != "sage" {
		t.Errorf("expected concierge=sage, got %q", got)
	}

	cancel()
	<-errCh
}

func TestSocketRemoveConcierge(t *testing.T) {
	tmpDir := shortTempDir(t)
	sender := &mockSender{name: "telegram"}
	svc := New([]bridge.Bridge{sender}, "sage", tmpDir, "alice", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()

	sockPath := filepath.Join(tmpDir, socketdir.Format(socketdir.TypeBridge, "alice"))
	waitForSocket(t, sockPath)

	// Send remove-concierge request.
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := message.SendRequest(conn, &message.Request{
		Type: "remove-concierge",
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := message.ReadResponse(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Errorf("expected OK, got error: %s", resp.Error)
	}

	// Concierge should be cleared.
	svc.mu.Lock()
	got := svc.concierge
	svc.mu.Unlock()
	if got != "" {
		t.Errorf("expected concierge to be cleared, got %q", got)
	}

	cancel()
	<-errCh
}

// --- Startup message tests ---

func TestStartupMessage_WithConcierge(t *testing.T) {
	sender := &mockSender{name: "telegram"}
	svc := New([]bridge.Bridge{sender}, "sage", t.TempDir(), "alice", []string{"status", "help"})

	svc.sendStartupMessage(context.Background())

	msgs := sender.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	msg := msgs[0]
	if !strings.Contains(msg, "Bridge is up and running") {
		t.Errorf("missing 'Bridge is up and running': %q", msg)
	}
	if !strings.Contains(msg, "sage") {
		t.Errorf("missing concierge name 'sage': %q", msg)
	}
	if !strings.Contains(msg, "status, help") {
		t.Errorf("missing allowed commands: %q", msg)
	}
}

func TestStartupMessage_NoAgents(t *testing.T) {
	sender := &mockSender{name: "telegram"}
	svc := New([]bridge.Bridge{sender}, "", t.TempDir(), "alice", nil)

	svc.sendStartupMessage(context.Background())

	msgs := sender.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0], "no agents are running") {
		t.Errorf("expected 'no agents are running' message, got %q", msgs[0])
	}
}

func TestStartupMessage_NoConciergeWithAgents(t *testing.T) {
	tmpDir := t.TempDir()
	// Create a fake agent socket file.
	os.WriteFile(filepath.Join(tmpDir, socketdir.Format(socketdir.TypeAgent, "coder-1")), nil, 0o600)

	sender := &mockSender{name: "telegram"}
	svc := New([]bridge.Bridge{sender}, "", tmpDir, "alice", nil)

	svc.sendStartupMessage(context.Background())

	msgs := sender.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	msg := msgs[0]
	if !strings.Contains(msg, "Bridge is up and running") {
		t.Errorf("missing 'Bridge is up and running': %q", msg)
	}
	if !strings.Contains(msg, "coder-1") {
		t.Errorf("missing first agent name: %q", msg)
	}
}

// --- Concierge monitoring in typing loop test ---

func TestTypingLoop_DetectsConciergeDown(t *testing.T) {
	typingTickInterval = 50 * time.Millisecond
	defer func() { typingTickInterval = 4 * time.Second }()

	tmpDir := shortTempDir(t)
	agent := newMockStatusAgent(t, tmpDir, "sage", "active")

	sender := &mockSender{name: "telegram"}
	svc := New([]bridge.Bridge{sender}, "sage", tmpDir, "alice", nil)

	ctx, cancel := context.WithCancel(context.Background())

	go svc.runTypingLoop(ctx)

	// Let the loop see the agent as alive.
	time.Sleep(150 * time.Millisecond)

	// Kill the agent.
	agent.listener.Close()
	agent.wg.Wait()

	// Wait for the loop to detect it's down.
	time.Sleep(200 * time.Millisecond)
	cancel()

	// Concierge should be cleared.
	svc.mu.Lock()
	got := svc.concierge
	svc.mu.Unlock()
	if got != "" {
		t.Errorf("expected concierge to be cleared after agent down, got %q", got)
	}

	// Should have sent a status message about concierge stopping.
	msgs := sender.Messages()
	found := false
	for _, m := range msgs {
		if strings.Contains(m, "sage stopped") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'sage stopped' status message, got %v", msgs)
	}
}
