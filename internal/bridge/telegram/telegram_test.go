package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSend(t *testing.T) {
	var gotChatID, gotText string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botTOKEN/sendMessage" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		r.ParseForm()
		gotChatID = r.FormValue("chat_id")
		gotText = r.FormValue("text")
		json.NewEncoder(w).Encode(apiResponse{OK: true})
	}))
	defer srv.Close()

	tg := &Telegram{
		Token:   "TOKEN",
		ChatID:  42,
		BaseURL: srv.URL,
	}

	err := tg.Send(context.Background(), "hello from h2")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotChatID != "42" {
		t.Errorf("chat_id = %q, want %q", gotChatID, "42")
	}
	if gotText != "hello from h2" {
		t.Errorf("text = %q, want %q", gotText, "hello from h2")
	}
}

func TestSendTyping(t *testing.T) {
	var gotChatID, gotAction string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botTOKEN/sendChatAction" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		r.ParseForm()
		gotChatID = r.FormValue("chat_id")
		gotAction = r.FormValue("action")
		json.NewEncoder(w).Encode(apiResponse{OK: true})
	}))
	defer srv.Close()

	tg := &Telegram{
		Token:   "TOKEN",
		ChatID:  42,
		BaseURL: srv.URL,
	}

	err := tg.SendTyping(context.Background())
	if err != nil {
		t.Fatalf("SendTyping: %v", err)
	}
	if gotChatID != "42" {
		t.Errorf("chat_id = %q, want %q", gotChatID, "42")
	}
	if gotAction != "typing" {
		t.Errorf("action = %q, want %q", gotAction, "typing")
	}
}

func TestSend_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(apiResponse{OK: false, Description: "bad request"})
	}))
	defer srv.Close()

	tg := &Telegram{
		Token:   "TOKEN",
		ChatID:  42,
		BaseURL: srv.URL,
	}

	err := tg.Send(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error from API")
	}
	if got := err.Error(); got != "telegram send: API error: bad request" {
		t.Errorf("error = %q", got)
	}
}

func TestStartStop(t *testing.T) {
	callCount := 0
	var mu sync.Mutex
	var received []struct{ agent, body string }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botTOKEN/getUpdates" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}

		mu.Lock()
		n := callCount
		callCount++
		mu.Unlock()

		if n == 0 {
			// First call: return two messages
			json.NewEncoder(w).Encode(getUpdatesResponse{
				OK: true,
				Result: []update{
					{
						UpdateID: 100,
						Message: &message{
							Text: "running-deer: check build",
							Chat: chat{ID: 42},
						},
					},
					{
						UpdateID: 101,
						Message: &message{
							Text: "plain message",
							Chat: chat{ID: 42},
						},
					},
				},
			})
		} else {
			// Subsequent calls: block until context is cancelled
			<-r.Context().Done()
		}
	}))
	defer srv.Close()

	tg := &Telegram{
		Token:   "TOKEN",
		ChatID:  42,
		BaseURL: srv.URL,
	}

	handler := func(agent, body string) {
		mu.Lock()
		received = append(received, struct{ agent, body string }{agent, body})
		mu.Unlock()
	}

	ctx := context.Background()
	if err := tg.Start(ctx, handler); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for messages to be processed
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	tg.Stop()

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 2 {
		t.Fatalf("got %d messages, want 2", len(received))
	}
	if received[0].agent != "running-deer" || received[0].body != "check build" {
		t.Errorf("msg[0] = %+v, want running-deer/check build", received[0])
	}
	if received[1].agent != "" || received[1].body != "plain message" {
		t.Errorf("msg[1] = %+v, want /plain message", received[1])
	}
}

func TestStartStop_ReplyRouting(t *testing.T) {
	var mu sync.Mutex
	var received []struct{ agent, body string }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botTOKEN/getUpdates" {
			http.NotFound(w, r)
			return
		}

		mu.Lock()
		first := len(received) == 0
		mu.Unlock()

		if first {
			json.NewEncoder(w).Encode(getUpdatesResponse{
				OK: true,
				Result: []update{
					{
						UpdateID: 300,
						Message: &message{
							Text: "what's the status?",
							Chat: chat{ID: 42},
							ReplyToMessage: &message{
								Text: "[researcher] here are the results",
								Chat: chat{ID: 42},
							},
						},
					},
				},
			})
		} else {
			<-r.Context().Done()
		}
	}))
	defer srv.Close()

	tg := &Telegram{
		Token:   "TOKEN",
		ChatID:  42,
		BaseURL: srv.URL,
	}

	handler := func(agent, body string) {
		mu.Lock()
		received = append(received, struct{ agent, body string }{agent, body})
		mu.Unlock()
	}

	if err := tg.Start(context.Background(), handler); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	tg.Stop()

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 {
		t.Fatalf("got %d messages, want 1", len(received))
	}
	// Reply to a [researcher] tagged message should route to researcher.
	if received[0].agent != "researcher" {
		t.Errorf("agent = %q, want %q", received[0].agent, "researcher")
	}
	if received[0].body != "what's the status?" {
		t.Errorf("body = %q, want %q", received[0].body, "what's the status?")
	}
}

func TestStartStop_FiltersChatID(t *testing.T) {
	var mu sync.Mutex
	var received []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botTOKEN/getUpdates" {
			http.NotFound(w, r)
			return
		}

		mu.Lock()
		first := len(received) == 0
		mu.Unlock()

		if first {
			json.NewEncoder(w).Encode(getUpdatesResponse{
				OK: true,
				Result: []update{
					{
						UpdateID: 200,
						Message: &message{
							Text: "wrong chat",
							Chat: chat{ID: 999},
						},
					},
					{
						UpdateID: 201,
						Message: &message{
							Text: "right chat",
							Chat: chat{ID: 42},
						},
					},
				},
			})
		} else {
			<-r.Context().Done()
		}
	}))
	defer srv.Close()

	tg := &Telegram{
		Token:   "TOKEN",
		ChatID:  42,
		BaseURL: srv.URL,
	}

	handler := func(agent, body string) {
		mu.Lock()
		received = append(received, body)
		mu.Unlock()
	}

	if err := tg.Start(context.Background(), handler); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	tg.Stop()

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 {
		t.Fatalf("got %d messages, want 1", len(received))
	}
	if received[0] != "right chat" {
		t.Errorf("got %q, want %q", received[0], "right chat")
	}
}

func TestPoll_ExponentialBackoff(t *testing.T) {
	var requestCount atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		// Always return an error to trigger backoff
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tg := &Telegram{
		Token:   "TOKEN",
		ChatID:  42,
		BaseURL: srv.URL,
	}

	handler := func(agent, body string) {}

	if err := tg.Start(context.Background(), handler); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// With 1s initial backoff, in 3.5 seconds we should see:
	//   - request 1 at t=0 (fails, wait 1s)
	//   - request 2 at t=1 (fails, wait 2s)
	//   - request 3 at t=3 (fails, wait 4s)
	// Without backoff we'd see dozens of requests.
	time.Sleep(3500 * time.Millisecond)
	tg.Stop()

	count := requestCount.Load()
	if count > 5 {
		t.Errorf("expected <= 5 requests with backoff, got %d (backoff not working)", count)
	}
	if count < 2 {
		t.Errorf("expected >= 2 requests, got %d (polling not running?)", count)
	}
}

func TestPoll_BackoffResetsOnSuccess(t *testing.T) {
	var mu sync.Mutex
	var callTimes []time.Time
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callTimes = append(callTimes, time.Now())
		n := callCount
		callCount++
		mu.Unlock()

		switch {
		case n == 0:
			// First call: error to trigger backoff
			w.WriteHeader(http.StatusInternalServerError)
		case n == 1:
			// Second call (after 1s backoff): error again to grow backoff to 2s
			w.WriteHeader(http.StatusInternalServerError)
		case n == 2:
			// Third call (after 2s backoff): succeed to reset backoff
			json.NewEncoder(w).Encode(getUpdatesResponse{OK: true})
		case n == 3:
			// Fourth call (should be immediate after success): error to trigger backoff
			w.WriteHeader(http.StatusInternalServerError)
		case n == 4:
			// Fifth call: should be after 1s (reset backoff), not 4s
			json.NewEncoder(w).Encode(getUpdatesResponse{OK: true})
		default:
			<-r.Context().Done()
		}
	}))
	defer srv.Close()

	tg := &Telegram{
		Token:   "TOKEN",
		ChatID:  42,
		BaseURL: srv.URL,
	}

	handler := func(agent, body string) {}

	if err := tg.Start(context.Background(), handler); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for enough calls to verify reset behavior
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := callCount
		mu.Unlock()
		if n >= 5 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	tg.Stop()

	mu.Lock()
	defer mu.Unlock()

	if len(callTimes) < 5 {
		t.Fatalf("expected at least 5 calls, got %d", len(callTimes))
	}

	// Gap between call 4 and 5 should be ~1s (reset backoff), not ~4s
	gap := callTimes[4].Sub(callTimes[3])
	if gap > 2*time.Second {
		t.Errorf("backoff did not reset after success: gap between call 4 and 5 was %v, expected ~1s", gap)
	}
}
