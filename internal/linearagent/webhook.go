package linearagent

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"time"
)

// Source delivers inbound Linear AgentSession events. It abstracts the
// transport so the service is agnostic to how events arrive (webhook today,
// outbound-dialed relay later).
type Source interface {
	// Events returns a channel of verified, well-formed agent-session events.
	Events() <-chan AgentSessionEvent
}

// signatureHeader is the header Linear sets to the hex HMAC-SHA256 of the raw
// request body, keyed by the webhook signing secret.
const signatureHeader = "Linear-Signature"

// maxBody caps webhook payload size.
const maxBody = 4 << 20

// WebhookSource is a Source backed by a local HTTP receiver. It verifies the
// Linear signature, decodes the payload, and emits well-formed agent-session
// events. Intended for dev / self-hosting; the relay Source will replace it as
// the default transport without changing the service.
type WebhookSource struct {
	secret []byte
	path   string
	events chan AgentSessionEvent
	srv    *http.Server

	// Debug, when true, logs each raw inbound payload (and signature header).
	// Useful for confirming Linear's exact payload shape on first integration.
	// Off by default since payloads may contain issue content.
	Debug bool
}

// NewWebhookSource creates a WebhookSource. secret is the webhook signing
// secret; path is the URL path to serve (defaults to "/linear/webhook").
func NewWebhookSource(secret, path string) *WebhookSource {
	if path == "" {
		path = "/linear/webhook"
	}
	return &WebhookSource{
		secret: []byte(secret),
		path:   path,
		events: make(chan AgentSessionEvent, 64),
	}
}

func (s *WebhookSource) Events() <-chan AgentSessionEvent { return s.events }

// Handler returns the http.Handler that ingests webhooks. Exposed so the
// receiver can be mounted on an existing mux or tested directly.
func (s *WebhookSource) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(s.path, s.handle)
	return mux
}

// Serve runs an HTTP server on addr until ctx is cancelled.
func (s *WebhookSource) Serve(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.srv = &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.srv.Shutdown(shutdownCtx)
	}()
	log.Printf("linear: webhook listening on %s%s", addr, s.path)
	if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *WebhookSource) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	if s.Debug {
		sigPresent := r.Header.Get(signatureHeader) != ""
		log.Printf("linear: [debug] inbound webhook (signature_present=%t):\n%s", sigPresent, string(body))
	}
	if !s.verify(r.Header.Get(signatureHeader), body) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	var ev AgentSessionEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}

	// Acknowledge fast (Linear expects a quick 200); process asynchronously.
	w.WriteHeader(http.StatusOK)

	if !ev.IsAgentSession() {
		return // not for us (other webhook category)
	}
	select {
	case s.events <- ev:
	default:
		log.Printf("linear: webhook event dropped (queue full) session=%s", ev.AgentSession.ID)
	}
}

// verify checks the hex HMAC-SHA256 signature against the raw body. When no
// secret is configured, verification is skipped (dev convenience) and a warning
// is logged.
func (s *WebhookSource) verify(sig string, body []byte) bool {
	if len(s.secret) == 0 {
		log.Printf("linear: WARNING webhook signature verification disabled (no secret configured)")
		return true
	}
	return VerifySignature(string(s.secret), sig, body)
}
