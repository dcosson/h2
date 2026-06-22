package linear

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestSplitIdentifier(t *testing.T) {
	tests := []struct {
		in      string
		key     string
		number  float64
		wantErr bool
	}{
		{"LIN-123", "LIN", 123, false},
		{"ENG-7", "ENG", 7, false},
		{"TEAM-ABC-12", "TEAM-ABC", 12, false}, // last dash splits
		{"  LIN-5  ", "LIN", 5, false},         // trimmed
		{"LIN", "", 0, true},
		{"LIN-", "", 0, true},
		{"-5", "", 0, true},
		{"LIN-x", "", 0, true},
	}
	for _, tt := range tests {
		key, num, err := splitIdentifier(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("splitIdentifier(%q): want error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("splitIdentifier(%q): %v", tt.in, err)
			continue
		}
		if key != tt.key || num != tt.number {
			t.Errorf("splitIdentifier(%q) = (%q,%v), want (%q,%v)", tt.in, key, num, tt.key, tt.number)
		}
	}
}

func TestCoarseStatus(t *testing.T) {
	tests := []struct {
		state, sub string
		wantKey    string
	}{
		{"initialized", "", "starting"},
		{"active", "", "working"},
		{"active", "thinking", "working_thinking"},
		{"active", "tool_use", "working_tool"},
		{"idle", "", "idle"},
		{"exited", "", "finished"},
		// sub-state blocks take priority over top-level state.
		{"active", "blocked_on_permission", "blocked_permission"},
		{"idle", "auth_error", "blocked_auth"},
		{"active", "server_error", "blocked_server"},
		{"active", "usage_limit", "blocked_usage"},
		{"active", "compacting", "compacting"},
	}
	for _, tt := range tests {
		if got := coarseStatus(tt.state, tt.sub); got.key != tt.wantKey {
			t.Errorf("coarseStatus(%q,%q).key = %q, want %q", tt.state, tt.sub, got.key, tt.wantKey)
		}
	}
}

// fakeObs is a controllable Observer.
type fakeObs struct {
	mu         sync.Mutex
	state, sub string
	ch         chan struct{}
}

func newFakeObs(state, sub string) *fakeObs {
	return &fakeObs{state: state, sub: sub, ch: make(chan struct{})}
}

func (f *fakeObs) State() (string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, f.sub
}

func (f *fakeObs) StateChanged() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ch
}

func (f *fakeObs) set(state, sub string) {
	f.mu.Lock()
	f.state, f.sub = state, sub
	old := f.ch
	f.ch = make(chan struct{})
	f.mu.Unlock()
	close(old)
}

type sinkCall struct {
	kind   string // "resolve", "upsert", "update"
	status string // h2State metadata value
}

// fakeSink records calls and reports the status of each write on a channel.
type fakeSink struct {
	resolveErr error
	calls      chan sinkCall
}

func newFakeSink() *fakeSink {
	return &fakeSink{calls: make(chan sinkCall, 64)}
}

func (f *fakeSink) ResolveIssue(_ context.Context, _ string) (string, error) {
	if f.resolveErr != nil {
		return "", f.resolveErr
	}
	return "issue-1", nil
}

func (f *fakeSink) UpsertAttachment(_ context.Context, in AttachmentInput) (string, error) {
	f.calls <- sinkCall{"upsert", statusOf(in.Metadata)}
	return "att-1", nil
}

func (f *fakeSink) UpdateAttachment(_ context.Context, _ string, p AttachmentPatch) error {
	f.calls <- sinkCall{"update", statusOf(p.Metadata)}
	return nil
}

func statusOf(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	s, _ := meta["h2State"].(string)
	return s
}

// waitCall reads the next sink call within a timeout.
func waitCall(t *testing.T, f *fakeSink) sinkCall {
	t.Helper()
	select {
	case c := <-f.calls:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for sink call")
		return sinkCall{}
	}
}

func newTestWatcher(sink *fakeSink, obs Observer) *Watcher {
	w := NewWatcher(sink, obs, "LIN-1", "coder-1", "h2://agent/coder-1", nil)
	w.pollInterval = 2 * time.Millisecond // ticker catches sub-state changes fast
	w.minWriteInterval = 0                // no rate cap in tests
	return w
}

func TestWatcherCreateThenUpdate(t *testing.T) {
	sink := newFakeSink()
	obs := newFakeObs("initialized", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go newTestWatcher(sink, obs).Run(ctx)

	// First write is an upsert (attachment created) with the starting status.
	if c := waitCall(t, sink); c.kind != "upsert" || c.status != "starting" {
		t.Fatalf("first call = %+v, want upsert/starting", c)
	}

	obs.set("active", "thinking")
	if c := waitCall(t, sink); c.kind != "update" || c.status != "working_thinking" {
		t.Fatalf("call = %+v, want update/working_thinking", c)
	}

	obs.set("active", "tool_use")
	if c := waitCall(t, sink); c.kind != "update" || c.status != "working_tool" {
		t.Fatalf("call = %+v, want update/working_tool", c)
	}

	obs.set("idle", "")
	if c := waitCall(t, sink); c.kind != "update" || c.status != "idle" {
		t.Fatalf("call = %+v, want update/idle", c)
	}

	// Dedup: state is unchanged, so the ticker must not produce more writes.
	select {
	case c := <-sink.calls:
		t.Fatalf("unexpected extra write while idle: %+v", c)
	case <-time.After(40 * time.Millisecond):
	}

	// On shutdown, a terminal write is forced past the dedup.
	cancel()
	if c := waitCall(t, sink); c.kind != "update" || c.status != "idle" {
		t.Fatalf("terminal call = %+v, want update/idle", c)
	}
}

func TestWatcherResolveFailureNoWrites(t *testing.T) {
	sink := newFakeSink()
	sink.resolveErr = context.DeadlineExceeded
	obs := newFakeObs("active", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() { newTestWatcher(sink, obs).Run(ctx); close(done) }()

	// Run should return immediately after the failed resolve, with no writes.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not exit after resolve failure")
	}
	select {
	case c := <-sink.calls:
		t.Fatalf("unexpected write after resolve failure: %+v", c)
	default:
	}
}
