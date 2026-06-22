package linear

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient returns a Client pointed at the given test server URL.
func newTestClient(url string) *Client {
	c := NewClient("test-token")
	c.apiURL = url
	return c
}

func TestResolveIssue(t *testing.T) {
	var gotVars map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "test-token" {
			t.Errorf("Authorization = %q, want test-token", got)
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		json.Unmarshal(body, &req)
		gotVars = req.Variables
		io.WriteString(w, `{"data":{"issues":{"nodes":[{"id":"issue-uuid-1"}]}}}`)
	}))
	defer srv.Close()

	id, err := newTestClient(srv.URL).ResolveIssue(context.Background(), "LIN-123")
	if err != nil {
		t.Fatalf("ResolveIssue: %v", err)
	}
	if id != "issue-uuid-1" {
		t.Errorf("id = %q, want issue-uuid-1", id)
	}
	if gotVars["key"] != "LIN" {
		t.Errorf("key var = %v, want LIN", gotVars["key"])
	}
	// JSON numbers decode to float64.
	if gotVars["number"] != float64(123) {
		t.Errorf("number var = %v, want 123", gotVars["number"])
	}
}

func TestResolveIssueNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"issues":{"nodes":[]}}}`)
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL).ResolveIssue(context.Background(), "LIN-999")
	if err == nil {
		t.Fatal("expected error for missing issue")
	}
}

func TestUpsertAttachment(t *testing.T) {
	var gotInput map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Variables struct {
				Input map[string]any `json:"input"`
			} `json:"variables"`
		}
		json.Unmarshal(body, &req)
		gotInput = req.Variables.Input
		io.WriteString(w, `{"data":{"attachmentCreate":{"success":true,"attachment":{"id":"att-1"}}}}`)
	}))
	defer srv.Close()

	id, err := newTestClient(srv.URL).UpsertAttachment(context.Background(), AttachmentInput{
		IssueID:  "issue-uuid-1",
		Title:    "coder-1",
		Subtitle: "Working",
		URL:      "h2://agent/coder-1",
		Metadata: map[string]any{"h2State": "working"},
	})
	if err != nil {
		t.Fatalf("UpsertAttachment: %v", err)
	}
	if id != "att-1" {
		t.Errorf("id = %q, want att-1", id)
	}
	if gotInput["issueId"] != "issue-uuid-1" || gotInput["url"] != "h2://agent/coder-1" {
		t.Errorf("input missing fields: %#v", gotInput)
	}
}

func TestUpdateAttachment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"attachmentUpdate":{"success":true}}}`)
	}))
	defer srv.Close()

	err := newTestClient(srv.URL).UpdateAttachment(context.Background(), "att-1", AttachmentPatch{
		Title:    "coder-1",
		Subtitle: "Idle",
	})
	if err != nil {
		t.Fatalf("UpdateAttachment: %v", err)
	}
}

func TestDoGraphQLError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"errors":[{"message":"boom"}]}`)
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL).ResolveIssue(context.Background(), "LIN-1")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want graphql error containing boom, got %v", err)
	}
}

func TestDoHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `unauthorized`)
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL).ResolveIssue(context.Background(), "LIN-1")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want http 401 error, got %v", err)
	}
}
