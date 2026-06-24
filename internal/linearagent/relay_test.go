package linearagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"h2/internal/linear"
)

// TestRelaySource_PollEmitsEvent verifies the client long-polls and emits the
// event the relay returns.
func TestRelaySource_PollEmitsEvent(t *testing.T) {
	ev := AgentSessionEvent{
		Type:         TypeAgentSession,
		Action:       ActionCreated,
		AgentSession: AgentSession{ID: "s1", Issue: Issue{Identifier: "LIN-1"}},
	}
	var polled int
	mu := sync.Mutex{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pair") != "p1" {
			http.Error(w, "bad pair", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		n := polled
		polled++
		mu.Unlock()
		if n == 0 {
			json.NewEncoder(w).Encode(ev) // first poll: an event
			return
		}
		w.WriteHeader(http.StatusNoContent) // subsequent: timeout
	}))
	defer srv.Close()

	s := NewRelaySource(srv.URL, "p1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	select {
	case got := <-s.Events():
		if got.AgentSession.ID != "s1" || got.AgentSession.Issue.Identifier != "LIN-1" {
			t.Fatalf("event = %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event emitted")
	}
}

// TestRelaySource_BadStatusBacksOff confirms a non-2xx poll is treated as an
// error (retryable) and doesn't emit an event.
func TestRelaySource_BadStatusNoEmit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()

	s := NewRelaySource(srv.URL, "p1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	select {
	case ev := <-s.Events():
		t.Fatalf("unexpected event on error: %+v", ev)
	case <-time.After(200 * time.Millisecond):
		// good: backed off, no emit
	}
}

func TestRelayReporter_PostsActivity(t *testing.T) {
	var gotPair string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPair = r.URL.Query().Get("pair")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := NewRelayReporter(srv.URL, "p1")
	err := r.CreateAgentActivity(context.Background(), "sess-1", linear.AgentActivity{
		Type: linear.ActivityResponse,
		Body: "done",
	})
	if err != nil {
		t.Fatalf("CreateAgentActivity: %v", err)
	}
	if gotPair != "p1" {
		t.Errorf("pair = %q", gotPair)
	}
	if gotBody["sessionId"] != "sess-1" {
		t.Errorf("sessionId = %v", gotBody["sessionId"])
	}
}

func TestRelayReporter_ErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()

	r := NewRelayReporter(srv.URL, "p1")
	err := r.CreateAgentActivity(context.Background(), "s", linear.AgentActivity{Type: linear.ActivityError, Body: "x"})
	if err == nil {
		t.Fatal("expected error on 502")
	}
}
