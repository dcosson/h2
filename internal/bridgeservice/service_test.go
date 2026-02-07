package bridgeservice

import (
	"context"
	"net"
	"os"
	"path/filepath"
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
	svc := New(nil, "concierge", tmpDir, "alice")

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
	svc := New(nil, "concierge", tmpDir, "alice")

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
	svc := New(nil, "", tmpDir, "alice") // no concierge
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
	svc := New(nil, "", tmpDir, "alice") // no concierge, no lastSender

	svc.handleInbound("", "fallback message")

	reqs := alpha.Received()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request to alpha, got %d", len(reqs))
	}
	if reqs[0].Body != "fallback message" {
		t.Errorf("expected body='fallback message', got %q", reqs[0].Body)
	}
}

// --- Outbound tests ---

func TestHandleOutbound(t *testing.T) {
	sender1 := &mockSender{name: "telegram"}
	sender2 := &mockSender{name: "macos"}
	recv := &mockReceiver{name: "recv-only"} // should not receive sends
	svc := New(
		[]bridge.Bridge{sender1, sender2, recv},
		"", t.TempDir(), "alice",
	)

	svc.handleOutbound("myagent", "build complete")

	// Both senders should have received the message.
	msgs1 := sender1.Messages()
	if len(msgs1) != 1 || msgs1[0] != "build complete" {
		t.Errorf("sender1: expected [build complete], got %v", msgs1)
	}
	msgs2 := sender2.Messages()
	if len(msgs2) != 1 || msgs2[0] != "build complete" {
		t.Errorf("sender2: expected [build complete], got %v", msgs2)
	}

	// lastSender should be updated.
	svc.mu.Lock()
	last := svc.lastSender
	svc.mu.Unlock()
	if last != "myagent" {
		t.Errorf("expected lastSender=myagent, got %q", last)
	}
}

// --- Socket listener test ---

func TestSocketListener(t *testing.T) {
	tmpDir := shortTempDir(t)
	sender := &mockSender{name: "test"}
	svc := New([]bridge.Bridge{sender}, "", tmpDir, "alice")

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

	// Give handleOutbound a moment to complete (it runs synchronously in handleConn,
	// but the response is sent after handleOutbound returns, so by now it's done).
	msgs := sender.Messages()
	if len(msgs) != 1 || msgs[0] != "hello human" {
		t.Errorf("expected sender to receive [hello human], got %v", msgs)
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

// --- Run lifecycle test ---

func TestRunStartsAndStopsReceivers(t *testing.T) {
	tmpDir := shortTempDir(t)
	recv := &mockReceiver{name: "test-recv"}
	svc := New([]bridge.Bridge{recv}, "concierge", tmpDir, "alice")

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
	svc := New(nil, "concierge", t.TempDir(), "alice")
	if got := svc.resolveDefaultTarget(); got != "concierge" {
		t.Errorf("expected concierge, got %q", got)
	}
}

func TestResolveDefaultTarget_LastSender(t *testing.T) {
	svc := New(nil, "", t.TempDir(), "alice")
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

	svc := New(nil, "", tmpDir, "alice")
	if got := svc.resolveDefaultTarget(); got != "alpha" {
		t.Errorf("expected alpha, got %q", got)
	}
}

func TestResolveDefaultTarget_NoAgents(t *testing.T) {
	svc := New(nil, "", t.TempDir(), "alice")
	if got := svc.resolveDefaultTarget(); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}
