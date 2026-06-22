package linearagent

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testSecret = "whsec_test"

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func postWebhook(t *testing.T, s *WebhookSource, body, sig string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, s.path, strings.NewReader(body))
	if sig != "" {
		req.Header.Set(signatureHeader, sig)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

const createdPayload = `{
  "type": "AgentSessionEvent",
  "action": "created",
  "organizationId": "org-1",
  "agentSession": {
    "id": "sess-1",
    "issue": {"id":"iss-1","identifier":"LIN-7","title":"Fix login","description":"users cannot log in"}
  }
}`

func TestWebhook_ValidSignatureEmitsEvent(t *testing.T) {
	s := NewWebhookSource(testSecret, "/linear/webhook")
	rec := postWebhook(t, s, createdPayload, sign(testSecret, []byte(createdPayload)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	select {
	case ev := <-s.Events():
		if ev.Action != ActionCreated || ev.AgentSession.ID != "sess-1" {
			t.Fatalf("event = %+v", ev)
		}
		if ev.AgentSession.Issue.Identifier != "LIN-7" {
			t.Errorf("identifier = %q", ev.AgentSession.Issue.Identifier)
		}
		if got := ev.PromptText(); got != "Fix login\n\nusers cannot log in" {
			t.Errorf("PromptText = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no event emitted")
	}
}

func TestWebhook_InvalidSignatureRejected(t *testing.T) {
	s := NewWebhookSource(testSecret, "/linear/webhook")
	rec := postWebhook(t, s, createdPayload, "deadbeef")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	select {
	case <-s.Events():
		t.Fatal("event emitted despite bad signature")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestWebhook_NonAgentEventIgnored(t *testing.T) {
	s := NewWebhookSource(testSecret, "/linear/webhook")
	body := `{"type":"Issue","action":"update"}`
	rec := postWebhook(t, s, body, sign(testSecret, []byte(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (acked but ignored)", rec.Code)
	}
	select {
	case ev := <-s.Events():
		t.Fatalf("unexpected event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestWebhook_PromptTextPrefersComment(t *testing.T) {
	ev := AgentSessionEvent{
		Type:   TypeAgentSession,
		Action: ActionPrompted,
		AgentSession: AgentSession{
			ID:      "s",
			Issue:   Issue{Title: "T", Description: "D"},
			Comment: &Comment{Body: "  please add tests  "},
		},
	}
	if got := ev.PromptText(); got != "please add tests" {
		t.Errorf("PromptText = %q, want comment body", got)
	}
}

func TestWebhook_NoSecretSkipsVerification(t *testing.T) {
	s := NewWebhookSource("", "/linear/webhook")
	rec := postWebhook(t, s, createdPayload, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	select {
	case <-s.Events():
	case <-time.After(time.Second):
		t.Fatal("no event emitted with verification disabled")
	}
}
