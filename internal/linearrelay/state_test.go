package linearrelay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// TestRelayPersistence confirms installs survive a server restart: a pairing
// created before restart still resolves to its workspace afterwards.
func TestRelayPersistence(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "relay-state.json")

	mk := func() *Server {
		s := New(Config{
			BaseURL:      "https://relay.test",
			ClientID:     "cid",
			ClientSecret: "csecret",
			StatePath:    statePath,
		}, fakeAuth{token: "ws-token", org: "org-1"}, &fakePoster{})
		s.newToken = func() string { return "pair-persist" }
		return s
	}

	// First instance: install.
	s1 := mk()
	srv1 := httptest.NewServer(s1.Handler())
	resp, err := http.Get(srv1.URL + "/oauth/callback?code=abc")
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	srv1.Close()

	// Second instance from the same state path: pairing must still resolve.
	s2 := mk()
	if org, ok := s2.orgForPairing("pair-persist"); !ok || org != "org-1" {
		t.Fatalf("pairing not restored: org=%q ok=%v", org, ok)
	}

	// And it should still proxy activities (token restored too).
	poster := &fakePoster{}
	s2.poster = poster
	srv2 := httptest.NewServer(s2.Handler())
	defer srv2.Close()
	ar, err := http.Post(srv2.URL+"/activity?pair=pair-persist", "application/json",
		strings.NewReader(`{"sessionId":"s","activity":{"Type":"thought","Body":"hi"}}`))
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if ar.StatusCode != http.StatusOK {
		t.Fatalf("activity status = %d", ar.StatusCode)
	}
	poster.mu.Lock()
	defer poster.mu.Unlock()
	if len(poster.calls) != 1 || poster.calls[0].token != "ws-token" {
		t.Fatalf("token not restored: %+v", poster.calls)
	}
}
