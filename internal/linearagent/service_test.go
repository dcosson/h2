package linearagent

import (
	"context"
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
	done   chan struct{}
	result string
}

func (h *fakeHandle) Done() <-chan struct{} { return h.done }
func (h *fakeHandle) Result() string        { return h.result }

// fakeRunner returns a handle the test controls.
type fakeRunner struct {
	handle  *fakeHandle
	gotReq  SpawnRequest
	spawned chan struct{}
	err     error
}

func (r *fakeRunner) Spawn(_ context.Context, req SpawnRequest) (AgentHandle, error) {
	r.gotReq = req
	close(r.spawned)
	if r.err != nil {
		return nil, r.err
	}
	return r.handle, nil
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

func TestService_CreatedAcksSpawnsAndResponds(t *testing.T) {
	src := fakeSource{ch: make(chan AgentSessionEvent, 1)}
	rep := newFakeReporter()
	handle := &fakeHandle{done: make(chan struct{}), result: "Implemented caching, opened PR."}
	runner := &fakeRunner{handle: handle, spawned: make(chan struct{})}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go New(src, rep, runner).Run(ctx)

	src.ch <- createdEvent()

	// 1. First activity is the ack thought.
	first := rep.wait(t)
	if first.act.Type != linear.ActivityThought {
		t.Fatalf("first activity = %s, want thought (ack)", first.act.Type)
	}
	if first.session != "sess-9" {
		t.Errorf("session = %q, want sess-9", first.session)
	}

	// 2. The agent was spawned with the issue prompt.
	select {
	case <-runner.spawned:
	case <-time.After(2 * time.Second):
		t.Fatal("agent was not spawned")
	}
	if runner.gotReq.Issue.Identifier != "LIN-9" {
		t.Errorf("spawn issue = %q", runner.gotReq.Issue.Identifier)
	}
	if runner.gotReq.Prompt != "Add caching\n\nslow page" {
		t.Errorf("spawn prompt = %q", runner.gotReq.Prompt)
	}

	// 3. When the agent finishes, its result is posted as the response.
	close(handle.done)
	resp := rep.wait(t)
	if resp.act.Type != linear.ActivityResponse || resp.act.Body != "Implemented caching, opened PR." {
		t.Fatalf("response activity = %+v", resp.act)
	}
}

func TestService_SpawnFailurePostsError(t *testing.T) {
	src := fakeSource{ch: make(chan AgentSessionEvent, 1)}
	rep := newFakeReporter()
	runner := &fakeRunner{spawned: make(chan struct{}), err: context.DeadlineExceeded}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go New(src, rep, runner).Run(ctx)

	src.ch <- createdEvent()

	if first := rep.wait(t); first.act.Type != linear.ActivityThought {
		t.Fatalf("first activity = %s, want ack thought", first.act.Type)
	}
	if errAct := rep.wait(t); errAct.act.Type != linear.ActivityError {
		t.Fatalf("second activity = %s, want error", errAct.act.Type)
	}
}
