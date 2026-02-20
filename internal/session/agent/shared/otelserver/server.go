// Package otelserver provides a reusable OTEL HTTP server that binds a random
// port on 127.0.0.1 and dispatches raw payloads via callbacks. Both the Claude
// Code and Codex adapters embed this.
package otelserver

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
)

// Callbacks defines the functions called when OTEL payloads are received.
// Any callback may be nil, in which case the endpoint still accepts POSTs
// and returns 200 but the body is discarded.
type Callbacks struct {
	OnLogs    func(body []byte)
	OnMetrics func(body []byte)
	OnTraces  func(body []byte)
}

// OtelServer is an HTTP server that accepts OTEL payloads on /v1/logs,
// /v1/metrics, and /v1/traces, dispatching the raw body to callbacks.
type OtelServer struct {
	Port     int
	listener net.Listener
	server   *http.Server
}

// New creates and starts an OtelServer bound to 127.0.0.1:0 (random port).
func New(cb Callbacks) (*OtelServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for otel: %w", err)
	}

	s := &OtelServer{
		Port:     ln.Addr().(*net.TCPAddr).Port,
		listener: ln,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/logs", makeHandler(cb.OnLogs))
	mux.HandleFunc("/v1/metrics", makeHandler(cb.OnMetrics))
	mux.HandleFunc("/v1/traces", makeHandler(cb.OnTraces))

	s.server = &http.Server{Handler: mux}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		wg.Done()
		s.server.Serve(ln)
	}()
	wg.Wait() // wait for goroutine to start

	return s, nil
}

// Stop gracefully shuts down the HTTP server and closes the listener.
func (s *OtelServer) Stop() {
	if s.server != nil {
		s.server.Shutdown(context.Background())
	}
	if s.listener != nil {
		s.listener.Close()
	}
}

// makeHandler returns an http.HandlerFunc that reads the POST body and
// dispatches it to the given callback.
func makeHandler(callback func(body []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body failed", http.StatusBadRequest)
			return
		}
		r.Body.Close()

		if callback != nil {
			callback(body)
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}
}
