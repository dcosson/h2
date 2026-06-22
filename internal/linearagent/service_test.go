package linearagent

import (
	"context"
	"sync"
	"testing"
	"time"

	"h2/internal/linear"
)

// fakeReporter records posted activities.
type fakeReporter struct {
	ch chan recorded
}

type recorded struct {
	session string
	act     linear.AgentActivity
}

func newFakeReporter() *fakeReporter { return &fakeReporter{ch: make(chan recorded, 64)} }

func (f *fakeReporter) CreateAgentActivity(_ context.Context, sessionID string, a linear.AgentActivity) error {
	f.ch <- recorded{sessionID, a}
	return nil
}

// wait returns activities in the order they were posted.
func (f *fakeReporter) wait(t *testing.T) recorded {
	t.Helper()
	select {
	case r := <-f.ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for activity")
		return recorded{}
	}
}

// fakeHandle is a controllable AgentHandle.
type fakeHandle struct {
	name     string
	done     chan struct{}
	acts     chan linear.AgentActivity
	result   string
	delivers chan string
}

func newFakeHandle(name string) *fakeHandle {
	return &fakeHandle{
		name:     name,
		done:     make(chan struct{}),
		acts:     make(chan linear.AgentActivity, 16),
		delivers: make(chan string, 16),
	}
}

func (h *fakeHandle) Name() string                            { return h.name }
func (h *fakeHandle) Done() <-chan struct{}                   { return h.done }
func (h *fakeHandle) Activities() <-chan linear.AgentActivity { return h.acts }
func (h *fakeHandle) Result() string                          { return h.result }
func (h *fakeHandle) Deliver(_ context.Context, text string) error {
	h.delivers <- text
	return nil
}

// fakeRunner returns a handle the test controls.
type fakeRunner struct {
	handle      *fakeHandle
	gotReq      SpawnRequest
	spawned     chan struct{}
	err         error
	deliveredTo chan string
}

func (r *fakeRunner) Spawn(_ context.Context, req SpawnRequest) (AgentHandle, error) {
	r.gotReq = req
	close(r.spawned)
	if r.err != nil {
		return nil, r.err
	}
	return r.handle, nil
}

func (r *fakeRunner) DeliverTo(_ context.Context, agentName, text string) error {
	if r.deliveredTo != nil {
		r.deliveredTo <- agentName + ":" + text
	}
	return nil
}

// fakeSource feeds events.
type fakeSource struct{ ch chan AgentSessionEvent }

func (s fakeSource) Events() <-chan AgentSessionEvent { return s.ch }

func createdEvent() AgentSessionEvent {
	return AgentSessionEvent{
		Type:   TypeAgentSession,
		Action: ActionCreated,
		AgentSession: AgentSession{
			ID:    "sess-9",
			Issue: Issue{ID: "iss-9", Identifier: "LIN-9", Title: "Add caching", Description: "slow page"},
		},
	}
}

func TestService_CreatedAcksSpawnsStreamsAndResponds(t *testing.T) {
	src := fakeSource{ch: make(chan AgentSessionEvent, 1)}
	rep := newFakeReporter()
	handle := newFakeHandle("lin-9")
	handle.result = "Implemented caching, opened PR."
	runner := &fakeRunner{handle: handle, spawned: make(chan struct{})}
	store := newMemStore()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go New(src, rep, runner, store).Run(ctx)

	src.ch <- createdEvent()

	// 1. First activity is the ack thought.
	first := rep.wait(t)
	if first.act.Type != linear.ActivityThought {
		t.Fatalf("first activity = %s, want thought (ack)", first.act.Type)
	}
	if first.session != "sess-9" {
		t.Errorf("session = %q, want sess-9", first.session)
	}

	// 2. The agent was spawned with the issue prompt, and the session persisted.
	select {
	case <-runner.spawned:
	case <-time.After(2 * time.Second):
		t.Fatal("agent was not spawned")
	}
	if runner.gotReq.Prompt != "Add caching\n\nslow page" {
		t.Errorf("spawn prompt = %q", runner.gotReq.Prompt)
	}
	if name, ok := store.Get("sess-9"); !ok || name != "lin-9" {
		t.Errorf("session not persisted: %q %v", name, ok)
	}

	// 3. Streamed activities are forwarded to Linear.
	handle.acts <- linear.AgentActivity{Type: linear.ActivityAction, Action: "Edited cache.go"}
	if a := rep.wait(t); a.act.Type != linear.ActivityAction || a.act.Action != "Edited cache.go" {
		t.Fatalf("streamed activity = %+v", a.act)
	}

	// 4. When the agent finishes, its result is posted as the response.
	close(handle.acts)
	close(handle.done)
	resp := rep.wait(t)
	if resp.act.Type != linear.ActivityResponse || resp.act.Body != "Implemented caching, opened PR." {
		t.Fatalf("response activity = %+v", resp.act)
	}

	// 5. Session cleaned up after completion.
	if _, ok := store.Get("sess-9"); ok {
		t.Error("session should be deleted after completion")
	}
}

func TestService_SpawnFailurePostsError(t *testing.T) {
	src := fakeSource{ch: make(chan AgentSessionEvent, 1)}
	rep := newFakeReporter()
	runner := &fakeRunner{spawned: make(chan struct{}), err: context.DeadlineExceeded}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go New(src, rep, runner, nil).Run(ctx)

	src.ch <- createdEvent()

	if first := rep.wait(t); first.act.Type != linear.ActivityThought {
		t.Fatalf("first activity = %s, want ack thought", first.act.Type)
	}
	if errAct := rep.wait(t); errAct.act.Type != linear.ActivityError {
		t.Fatalf("second activity = %s, want error", errAct.act.Type)
	}
}

func TestService_FollowupDeliveredToActiveHandle(t *testing.T) {
	src := fakeSource{ch: make(chan AgentSessionEvent, 2)}
	rep := newFakeReporter()
	handle := newFakeHandle("lin-9")
	runner := &fakeRunner{handle: handle, spawned: make(chan struct{})}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go New(src, rep, runner, newMemStore()).Run(ctx)

	src.ch <- createdEvent()
	rep.wait(t) // ack
	<-runner.spawned

	// Send a follow-up prompt for the same session.
	src.ch <- AgentSessionEvent{
		Type:   TypeAgentSession,
		Action: ActionPrompted,
		AgentSession: AgentSession{
			ID:      "sess-9",
			Issue:   Issue{Identifier: "LIN-9"},
			Comment: &Comment{Body: "also handle errors"},
		},
	}

	select {
	case got := <-handle.delivers:
		if got != "also handle errors" {
			t.Fatalf("delivered = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("follow-up not delivered to handle")
	}
}

func TestService_FollowupResumesViaStore(t *testing.T) {
	src := fakeSource{ch: make(chan AgentSessionEvent, 1)}
	rep := newFakeReporter()
	runner := &fakeRunner{deliveredTo: make(chan string, 1)}
	store := newMemStore()
	store.Put("sess-x", "lin-42") // simulate a session from a previous run

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go New(src, rep, runner, store).Run(ctx)

	src.ch <- AgentSessionEvent{
		Type:   TypeAgentSession,
		Action: ActionPrompted,
		AgentSession: AgentSession{
			ID:      "sess-x",
			Comment: &Comment{Body: "ping"},
		},
	}

	select {
	case got := <-runner.deliveredTo:
		if got != "lin-42:ping" {
			t.Fatalf("delivered = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("follow-up not routed via store")
	}
}

// memStore is an in-memory SessionStore for tests.
type memStore struct {
	mu sync.Mutex
	m  map[string]string
}

func newMemStore() *memStore { return &memStore{m: map[string]string{}} }

func (s *memStore) Put(id, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = name
	return nil
}
func (s *memStore) Get(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.m[id]
	return n, ok
}
func (s *memStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}
