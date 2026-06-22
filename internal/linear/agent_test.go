package linear

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestViewer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer oauth-tok" {
			t.Errorf("Authorization = %q, want Bearer oauth-tok", got)
		}
		io.WriteString(w, `{"data":{"viewer":{"id":"app-user-1"}}}`)
	}))
	defer srv.Close()

	c := NewOAuthClient("oauth-tok")
	c.apiURL = srv.URL
	id, err := c.Viewer(context.Background())
	if err != nil {
		t.Fatalf("Viewer: %v", err)
	}
	if id != "app-user-1" {
		t.Errorf("id = %q, want app-user-1", id)
	}
}

func TestCreateAgentActivity_Thought(t *testing.T) {
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
		io.WriteString(w, `{"data":{"agentActivityCreate":{"success":true}}}`)
	}))
	defer srv.Close()

	c := NewOAuthClient("tok")
	c.apiURL = srv.URL
	err := c.CreateAgentActivity(context.Background(), "sess-1", AgentActivity{
		Type: ActivityThought,
		Body: "On it",
	})
	if err != nil {
		t.Fatalf("CreateAgentActivity: %v", err)
	}
	if gotInput["agentSessionId"] != "sess-1" {
		t.Errorf("agentSessionId = %v, want sess-1", gotInput["agentSessionId"])
	}
	content, _ := gotInput["content"].(map[string]any)
	if content["type"] != "thought" || content["body"] != "On it" {
		t.Errorf("content = %#v, want thought/On it", content)
	}
}

func TestCreateAgentActivity_Action(t *testing.T) {
	var content map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Variables struct {
				Input struct {
					Content map[string]any `json:"content"`
				} `json:"input"`
			} `json:"variables"`
		}
		json.Unmarshal(body, &req)
		content = req.Variables.Input.Content
		io.WriteString(w, `{"data":{"agentActivityCreate":{"success":true}}}`)
	}))
	defer srv.Close()

	c := NewOAuthClient("tok")
	c.apiURL = srv.URL
	err := c.CreateAgentActivity(context.Background(), "sess-1", AgentActivity{
		Type:      ActivityAction,
		Action:    "Ran tests",
		Parameter: "go test ./...",
		Result:    "ok",
	})
	if err != nil {
		t.Fatalf("CreateAgentActivity: %v", err)
	}
	if content["type"] != "action" || content["action"] != "Ran tests" ||
		content["parameter"] != "go test ./..." || content["result"] != "ok" {
		t.Errorf("content = %#v", content)
	}
}

func TestCreateAgentActivity_Failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"agentActivityCreate":{"success":false}}}`)
	}))
	defer srv.Close()

	c := NewOAuthClient("tok")
	c.apiURL = srv.URL
	err := c.CreateAgentActivity(context.Background(), "sess-1", AgentActivity{Type: ActivityResponse, Body: "done"})
	if err == nil {
		t.Fatal("expected error on success=false")
	}
}
