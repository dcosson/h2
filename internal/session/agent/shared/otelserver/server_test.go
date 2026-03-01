package otelserver

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestNew_BindsRandomPort(t *testing.T) {
	s, err := New(Callbacks{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Stop()

	if s.Port == 0 {
		t.Fatal("expected non-zero port")
	}
}

func TestNew_TwoServers_DifferentPorts(t *testing.T) {
	s1, err := New(Callbacks{})
	if err != nil {
		t.Fatalf("New s1: %v", err)
	}
	defer s1.Stop()

	s2, err := New(Callbacks{})
	if err != nil {
		t.Fatalf("New s2: %v", err)
	}
	defer s2.Stop()

	if s1.Port == s2.Port {
		t.Fatalf("expected different ports, both got %d", s1.Port)
	}
}

func TestCallbacks_OnLogs(t *testing.T) {
	var mu sync.Mutex
	var got []byte

	s, err := New(Callbacks{
		OnLogs: func(body []byte) {
			mu.Lock()
			got = append([]byte{}, body...)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Stop()

	payload := `{"resourceLogs":[]}`
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/v1/logs", s.Port), "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /v1/logs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	if string(got) != payload {
		t.Errorf("callback got %q, want %q", got, payload)
	}
}

func TestCallbacks_OnMetrics(t *testing.T) {
	var mu sync.Mutex
	var got []byte

	s, err := New(Callbacks{
		OnMetrics: func(body []byte) {
			mu.Lock()
			got = append([]byte{}, body...)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Stop()

	payload := `{"resourceMetrics":[]}`
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/v1/metrics", s.Port), "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /v1/metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	if string(got) != payload {
		t.Errorf("callback got %q, want %q", got, payload)
	}
}

func TestCallbacks_OnTraces(t *testing.T) {
	var mu sync.Mutex
	var got []byte

	s, err := New(Callbacks{
		OnTraces: func(body []byte) {
			mu.Lock()
			got = append([]byte{}, body...)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Stop()

	payload := `{"resourceSpans":[]}`
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/v1/traces", s.Port), "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /v1/traces: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	if string(got) != payload {
		t.Errorf("callback got %q, want %q", got, payload)
	}
}

func TestNilCallback_StillReturns200(t *testing.T) {
	s, err := New(Callbacks{}) // all callbacks nil
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Stop()

	for _, path := range []string{"/v1/logs", "/v1/metrics", "/v1/traces"} {
		resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d%s", s.Port, path), "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("POST %s: expected 200, got %d", path, resp.StatusCode)
		}
		if string(body) != "{}" {
			t.Errorf("POST %s: expected body {}, got %q", path, body)
		}
	}
}

func TestMethodNotAllowed(t *testing.T) {
	s, err := New(Callbacks{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Stop()

	for _, path := range []string{"/v1/logs", "/v1/metrics", "/v1/traces"} {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s", s.Port, path))
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: expected 405, got %d", path, resp.StatusCode)
		}
	}
}

func TestStop_ClosesServer(t *testing.T) {
	s, err := New(Callbacks{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	port := s.Port

	s.Stop()

	// After Stop, the port should no longer accept connections.
	_, err = http.Post(fmt.Sprintf("http://127.0.0.1:%d/v1/logs", port), "application/json", strings.NewReader("{}"))
	if err == nil {
		t.Error("expected connection error after Stop, got nil")
	}
}
