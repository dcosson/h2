package linearrelay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"h2/internal/linear"
	"h2/internal/linearagent"
)

type fakeAuth struct {
	token string
	org   string
}

func (f fakeAuth) Exchange(_ context.Context, code, _ string) (string, error) { return f.token, nil }
func (f fakeAuth) OrgID(_ context.Context, _ string) (string, error)          { return f.org, nil }

type fakePoster struct {
	mu    sync.Mutex
	calls []postCall
}
type postCall struct {
	token, session string
	act            linear.AgentActivity
}

func (p *fakePoster) Post(_ context.Context, token, sessionID string, a linear.AgentActivity) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, postCall{token, sessionID, a})
	return nil
}

func newTestServer(secret string) (*Server, *fakePoster) {
	poster := &fakePoster{}
	s := New(Config{
		BaseURL:       "https://relay.test",
		ClientID:      "cid",
		ClientSecret:  "csecret",
		WebhookSecret: secret,
	}, fakeAuth{token: "ws-token", org: "org-1"}, poster)
	// Deterministic pairing token for the test.
	s.newToken = func() string { return "pair-abc" }
	return s, poster
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// TestRelayFullFlow exercises install -> webhook -> daemon poll -> activity post.
func TestRelayFullFlow(t *testing.T) {
	const secret = "whsec"
	s, poster := newTestServer(secret)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	// 1. OAuth callback installs the workspace and yields a pairing token.
	resp, err := http.Get(srv.URL + "/oauth/callback?code=abc123")
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	bodyHTML, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(bodyHTML), "pair-abc") {
		t.Fatalf("callback page missing pairing token: %s", bodyHTML)
	}

	// 2. A daemon long-polls for events.
	gotEvent := make(chan linearagent.AgentSessionEvent, 1)
	go func() {
		r, err := http.Get(srv.URL + "/poll?pair=pair-abc")
		if err != nil {
			return
		}
		defer r.Body.Close()
		if r.StatusCode == http.StatusOK {
			var ev linearagent.AgentSessionEvent
			json.NewDecoder(r.Body).Decode(&ev)
			gotEvent <- ev
		}
	}()
	time.Sleep(100 * time.Millisecond) // let the poll register

	// 3. Linear delivers a signed webhook for org-1.
	payload := `{"type":"AgentSessionEvent","action":"created","organizationId":"org-1","agentSession":{"id":"sess-1","issue":{"identifier":"LIN-3"}}}`
	wr, _ := http.NewRequest(http.MethodPost, srv.URL+"/webhook", strings.NewReader(payload))
	wr.Header.Set("Linear-Signature", sign(secret, []byte(payload)))
	whResp, err := http.DefaultClient.Do(wr)
	if err != nil {
		t.Fatalf("webhook: %v", err)
	}
	if whResp.StatusCode != http.StatusOK {
		t.Fatalf("webhook status = %d", whResp.StatusCode)
	}

	// 4. The daemon receives the routed event.
	select {
	case ev := <-gotEvent:
		if ev.AgentSession.ID != "sess-1" || ev.AgentSession.Issue.Identifier != "LIN-3" {
			t.Fatalf("routed event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not receive routed event")
	}

	// 5. The daemon posts an activity back; relay forwards to Linear with the token.
	env := ActivityEnvelope{SessionID: "sess-1", Activity: linear.AgentActivity{Type: linear.ActivityThought, Body: "hi"}}
	b, _ := json.Marshal(env)
	ar, err := http.Post(srv.URL+"/activity?pair=pair-abc", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if ar.StatusCode != http.StatusOK {
		t.Fatalf("activity status = %d", ar.StatusCode)
	}
	poster.mu.Lock()
	defer poster.mu.Unlock()
	if len(poster.calls) != 1 || poster.calls[0].token != "ws-token" || poster.calls[0].session != "sess-1" {
		t.Fatalf("poster calls = %+v", poster.calls)
	}
}

func TestRelay_WebhookBadSignature(t *testing.T) {
	s, _ := newTestServer("whsec")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	payload := `{"type":"AgentSessionEvent","action":"created","organizationId":"org-1","agentSession":{"id":"s"}}`
	wr, _ := http.NewRequest(http.MethodPost, srv.URL+"/webhook", strings.NewReader(payload))
	wr.Header.Set("Linear-Signature", "bad")
	resp, _ := http.DefaultClient.Do(wr)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestRelay_PollUnknownPairing(t *testing.T) {
	s, _ := newTestServer("whsec")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/poll?pair=nope")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestRelay_ActivityUnknownPairing(t *testing.T) {
	s, _ := newTestServer("whsec")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, _ := http.Post(srv.URL+"/activity?pair=nope", "application/json", strings.NewReader(`{}`))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
