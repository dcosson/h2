package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
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
