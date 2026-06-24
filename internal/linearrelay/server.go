package linearrelay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"h2/internal/linear"
	"h2/internal/linearagent"
)

// pollTimeout bounds a single long-poll before returning 204 so the daemon
// re-polls (keeps the connection fresh through proxies/load balancers).
const pollTimeout = 25 * time.Second

// hubBuffer is the per-workspace event queue depth.
const hubBuffer = 64

// LinearAuth abstracts Linear's OAuth token exchange and identity lookup so the
// server is testable without hitting Linear.
type LinearAuth interface {
	// Exchange swaps an authorization code for an actor=app access token.
	Exchange(ctx context.Context, code, redirectURI string) (token string, err error)
	// OrgID returns the workspace ID for a token (matches webhook organizationId).
	OrgID(ctx context.Context, token string) (string, error)
}

// ActivityPoster posts an activity to Linear using a workspace's token.
type ActivityPoster interface {
	Post(ctx context.Context, token, sessionID string, a linear.AgentActivity) error
}

// Config configures a relay Server.
type Config struct {
	BaseURL       string // public base URL, e.g. https://relay.h2.dev
	ClientID      string // Linear OAuth app client id
	ClientSecret  string // Linear OAuth app client secret
	WebhookSecret string // Linear webhook signing secret
	StatePath     string // optional path to persist tokens/pairings across restarts
}

// Server is the hosted relay.
type Server struct {
	cfg       Config
	auth      LinearAuth
	poster    ActivityPoster
	newToken  func() string
	statePath string

	mu       sync.Mutex
	tokens   map[string]string                             // orgID -> oauth token
	pairings map[string]string                             // pairingToken -> orgID
	hubs     map[string]chan linearagent.AgentSessionEvent // orgID -> event queue
}

// New builds a relay Server. auth and poster default to live Linear
// implementations when nil. If cfg.StatePath is set, previously-installed
// workspaces are loaded from it and persisted on each new install.
func New(cfg Config, auth LinearAuth, poster ActivityPoster) *Server {
	if auth == nil {
		auth = &liveAuth{clientID: cfg.ClientID, clientSecret: cfg.ClientSecret}
	}
	if poster == nil {
		poster = livePoster{}
	}
	s := &Server{
		cfg:       cfg,
		auth:      auth,
		poster:    poster,
		newToken:  randomToken,
		statePath: cfg.StatePath,
		tokens:    map[string]string{},
		pairings:  map[string]string{},
		hubs:      map[string]chan linearagent.AgentSessionEvent{},
	}
	s.loadState()
	return s
}

// Handler returns the relay's HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/authorize", s.handleAuthorize)
	mux.HandleFunc("/oauth/callback", s.handleCallback)
	mux.HandleFunc("/webhook", s.handleWebhook)
	mux.HandleFunc("/poll", s.handlePoll)
	mux.HandleFunc("/activity", s.handleActivity)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	return mux
}

// --- OAuth install ---

func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	redirect := s.cfg.BaseURL + "/oauth/callback"
	q := url.Values{}
	q.Set("client_id", s.cfg.ClientID)
	q.Set("redirect_uri", redirect)
	q.Set("response_type", "code")
	q.Set("scope", "read,write,app:assignable,app:mentionable")
	q.Set("actor", "app")
	http.Redirect(w, r, "https://linear.app/oauth/authorize?"+q.Encode(), http.StatusFound)
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	token, err := s.auth.Exchange(ctx, code, s.cfg.BaseURL+"/oauth/callback")
	if err != nil {
		log.Printf("relay: token exchange failed: %v", err)
		http.Error(w, "token exchange failed", http.StatusBadGateway)
		return
	}
	org, err := s.auth.OrgID(ctx, token)
	if err != nil {
		log.Printf("relay: identity lookup failed: %v", err)
		http.Error(w, "identity lookup failed", http.StatusBadGateway)
		return
	}

	pairing := s.newToken()
	s.mu.Lock()
	s.tokens[org] = token
	s.pairings[pairing] = org
	s.persistLocked()
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, installedHTML, pairing)
}

// --- Linear webhook ingress ---

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body := readLimited(r)
	if !linearagent.VerifySignature(s.cfg.WebhookSecret, r.Header.Get("Linear-Signature"), body) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	// Ack Linear immediately; route asynchronously.
	w.WriteHeader(http.StatusOK)

	var ev linearagent.AgentSessionEvent
	if err := json.Unmarshal(body, &ev); err != nil || !ev.IsAgentSession() {
		return
	}
	if ev.OrganizationID == "" {
		log.Printf("relay: event without organizationId (session=%s) dropped", ev.AgentSession.ID)
		return
	}
	hub := s.hub(ev.OrganizationID)
	select {
	case hub <- ev:
	default:
		log.Printf("relay: queue full for org=%s, event dropped", ev.OrganizationID)
	}
}

// --- Daemon long-poll + activity proxy ---

func (s *Server) handlePoll(w http.ResponseWriter, r *http.Request) {
	org, ok := s.orgForPairing(r.URL.Query().Get("pair"))
	if !ok {
		http.Error(w, "unknown pairing token", http.StatusUnauthorized)
		return
	}
	hub := s.hub(org)
	select {
	case ev := <-hub:
		writeJSON(w, ev)
	case <-time.After(pollTimeout):
		w.WriteHeader(http.StatusNoContent)
	case <-r.Context().Done():
		// client hung up; nothing to do
	}
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	org, ok := s.orgForPairing(r.URL.Query().Get("pair"))
	if !ok {
		http.Error(w, "unknown pairing token", http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	token := s.tokens[org]
	s.mu.Unlock()
	if token == "" {
		http.Error(w, "no token for workspace", http.StatusForbidden)
		return
	}

	var env ActivityEnvelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if err := s.poster.Post(r.Context(), token, env.SessionID, env.Activity); err != nil {
		log.Printf("relay: post activity for org=%s failed: %v", org, err)
		http.Error(w, "post failed", http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// --- helpers ---

func (s *Server) hub(org string) chan linearagent.AgentSessionEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.hubs[org]
	if !ok {
		h = make(chan linearagent.AgentSessionEvent, hubBuffer)
		s.hubs[org] = h
	}
	return h
}

func (s *Server) orgForPairing(pair string) (string, bool) {
	if pair == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	org, ok := s.pairings[pair]
	return org, ok
}

func randomToken() string {
	b := make([]byte, 24)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func readLimited(r *http.Request) []byte {
	const max = 4 << 20
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 32*1024)
	total := 0
	for {
		n, err := r.Body.Read(tmp)
		if n > 0 {
			if total+n > max {
				buf = append(buf, tmp[:max-total]...)
				break
			}
			buf = append(buf, tmp[:n]...)
			total += n
		}
		if err != nil {
			break
		}
	}
	return buf
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

const installedHTML = `<!doctype html><html><head><meta charset="utf-8"><title>h2 connected</title>
<style>body{font-family:system-ui;max-width:640px;margin:64px auto;padding:0 16px;line-height:1.5}
code,pre{background:#f4f4f5;border-radius:6px}pre{padding:12px;overflow:auto}</style></head>
<body><h1>✅ h2 is connected to your Linear workspace</h1>
<p>Add this to <code>~/.h2/config.yaml</code>, then run <code>h2 linear serve</code>:</p>
<pre>linear:
  inbound:
    mode: relay
    pairing_token: "%s"
  agent:
    role: "default"</pre>
<p>Delegate an issue to <strong>h2</strong> in Linear and it will get to work.</p>
</body></html>`
