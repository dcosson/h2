// Package linear implements the outbound Linear attachment integration: it
// surfaces an h2 agent's live status as an attachment on a Linear issue.
//
// This package is deliberately self-contained and one-directional (h2 ->
// Linear). It has no dependency on the session/monitor packages — the Watcher
// observes agent state through the small string-based Observer interface, which
// the daemon adapts from its AgentMonitor. The package never affects the agent:
// all work runs off the agent's hot path and every Linear call's error is
// returned to the caller, which logs and drops it.
package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultAPIURL is Linear's GraphQL endpoint.
const DefaultAPIURL = "https://api.linear.app/graphql"

// Client is a minimal Linear GraphQL client. It is safe for concurrent use.
type Client struct {
	token      string
	authScheme string // "" for personal API keys, "Bearer" for OAuth tokens
	apiURL     string
	http       *http.Client
}

// NewClient returns a Client authenticated with a Linear personal API key,
// which is sent in the Authorization header directly (no scheme prefix).
func NewClient(token string) *Client {
	return &Client{
		token:  token,
		apiURL: DefaultAPIURL,
		http:   &http.Client{Timeout: 15 * time.Second},
	}
}

// NewOAuthClient returns a Client authenticated with an OAuth access token
// (actor=app), sent as "Authorization: Bearer <token>". This is the client the
// agent integration uses for agent-session activity calls.
func NewOAuthClient(token string) *Client {
	c := NewClient(token)
	c.authScheme = "Bearer"
	return c
}

// do executes a GraphQL request and unmarshals the "data" field into out.
func (c *Client) do(ctx context.Context, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// Personal API keys go in the Authorization header directly; OAuth tokens
	// use the "Bearer" scheme.
	if c.authScheme != "" {
		req.Header.Set("Authorization", c.authScheme+" "+c.token)
	} else {
		req.Header.Set("Authorization", c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("linear: http %d: %s", resp.StatusCode, string(data))
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("linear: decode response: %w", err)
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("linear: graphql error: %s", envelope.Errors[0].Message)
	}
	if out != nil && len(envelope.Data) > 0 {
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			return fmt.Errorf("linear: decode data: %w", err)
		}
	}
	return nil
}
