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

// AttachmentInput is the data used to create (upsert) an attachment.
type AttachmentInput struct {
	IssueID  string
	Title    string
	Subtitle string
	URL      string
	Metadata map[string]any
}

// AttachmentPatch is the subset of fields updated on an existing attachment.
// URL is intentionally omitted — it is the dedup key and must stay stable.
type AttachmentPatch struct {
	Title    string
	Subtitle string
	Metadata map[string]any
}

// ResolveIssue maps a Linear issue identifier ("LIN-123") to its internal UUID.
// It splits the identifier into a team key and number and filters on both, which
// is the robust way to look up an issue by its human-facing identifier.
func (c *Client) ResolveIssue(ctx context.Context, identifier string) (string, error) {
	teamKey, number, err := splitIdentifier(identifier)
	if err != nil {
		return "", err
	}

	const query = `query IssueByIdentifier($number: Float!, $key: String!) {
  issues(filter: {number: {eq: $number}, team: {key: {eq: $key}}}, first: 1) {
    nodes { id }
  }
}`
	vars := map[string]any{"number": number, "key": teamKey}

	var resp struct {
		Issues struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		} `json:"issues"`
	}
	if err := c.do(ctx, query, vars, &resp); err != nil {
		return "", err
	}
	if len(resp.Issues.Nodes) == 0 {
		return "", fmt.Errorf("linear: no issue found for %q", identifier)
	}
	return resp.Issues.Nodes[0].ID, nil
}

// UpsertAttachment creates an attachment on an issue. Linear dedupes by URL per
// issue, so calling this again with the same URL updates the existing
// attachment rather than creating a duplicate — which makes it safe across
// resume. It returns the attachment's ID.
func (c *Client) UpsertAttachment(ctx context.Context, in AttachmentInput) (string, error) {
	const mutation = `mutation AttachmentCreate($input: AttachmentCreateInput!) {
  attachmentCreate(input: $input) { success attachment { id } }
}`
	input := map[string]any{
		"issueId": in.IssueID,
		"title":   in.Title,
		"url":     in.URL,
	}
	if in.Subtitle != "" {
		input["subtitle"] = in.Subtitle
	}
	if in.Metadata != nil {
		input["metadata"] = in.Metadata
	}
	vars := map[string]any{"input": input}

	var resp struct {
		AttachmentCreate struct {
			Success    bool `json:"success"`
			Attachment struct {
				ID string `json:"id"`
			} `json:"attachment"`
		} `json:"attachmentCreate"`
	}
	if err := c.do(ctx, mutation, vars, &resp); err != nil {
		return "", err
	}
	if !resp.AttachmentCreate.Success {
		return "", fmt.Errorf("linear: attachmentCreate returned success=false")
	}
	return resp.AttachmentCreate.Attachment.ID, nil
}

// UpdateAttachment updates the mutable fields of an existing attachment.
func (c *Client) UpdateAttachment(ctx context.Context, id string, p AttachmentPatch) error {
	const mutation = `mutation AttachmentUpdate($id: String!, $input: AttachmentUpdateInput!) {
  attachmentUpdate(id: $id, input: $input) { success }
}`
	input := map[string]any{"title": p.Title}
	if p.Subtitle != "" {
		input["subtitle"] = p.Subtitle
	}
	if p.Metadata != nil {
		input["metadata"] = p.Metadata
	}
	vars := map[string]any{"id": id, "input": input}

	var resp struct {
		AttachmentUpdate struct {
			Success bool `json:"success"`
		} `json:"attachmentUpdate"`
	}
	if err := c.do(ctx, mutation, vars, &resp); err != nil {
		return err
	}
	if !resp.AttachmentUpdate.Success {
		return fmt.Errorf("linear: attachmentUpdate returned success=false")
	}
	return nil
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
