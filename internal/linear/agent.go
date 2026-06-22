package linear

import (
	"context"
	"fmt"
)

// ActivityType is the kind of agent-session activity posted back to Linear.
// The session's visible state is derived by Linear from the activities it
// receives, so there is no separate status field to manage.
type ActivityType string

const (
	// ActivityThought is reasoning/status shown inline (also used to ack a
	// session: the first activity must arrive within ~10s of the created event).
	ActivityThought ActivityType = "thought"
	// ActivityAction is a discrete step the agent took (with optional result).
	ActivityAction ActivityType = "action"
	// ActivityResponse is a terminal reply that completes the agent's turn.
	ActivityResponse ActivityType = "response"
	// ActivityElicitation asks the human for input; their reply re-enters the
	// session as a follow-up event.
	ActivityElicitation ActivityType = "elicitation"
	// ActivityError reports a failure.
	ActivityError ActivityType = "error"
)

// AgentActivity is a single activity to post to an agent session. Body is used
// by thought/response/elicitation/error; Action/Parameter/Result by action.
type AgentActivity struct {
	Type      ActivityType
	Body      string
	Action    string
	Parameter string
	Result    string
}

// content builds the GraphQL content object for this activity.
func (a AgentActivity) content() map[string]any {
	c := map[string]any{"type": string(a.Type)}
	switch a.Type {
	case ActivityAction:
		c["action"] = a.Action
		c["parameter"] = a.Parameter
		if a.Result != "" {
			c["result"] = a.Result
		}
	default:
		c["body"] = a.Body
	}
	return c
}

// Viewer returns the authenticated identity's ID. Under an actor=app OAuth
// token this is the agent's own app-user ID in the workspace.
func (c *Client) Viewer(ctx context.Context) (string, error) {
	const query = `query Viewer { viewer { id } }`
	var resp struct {
		Viewer struct {
			ID string `json:"id"`
		} `json:"viewer"`
	}
	if err := c.do(ctx, query, nil, &resp); err != nil {
		return "", err
	}
	if resp.Viewer.ID == "" {
		return "", fmt.Errorf("linear: empty viewer id")
	}
	return resp.Viewer.ID, nil
}

// CreateAgentActivity posts an activity to an agent session.
func (c *Client) CreateAgentActivity(ctx context.Context, sessionID string, a AgentActivity) error {
	const mutation = `mutation AgentActivityCreate($input: AgentActivityCreateInput!) {
  agentActivityCreate(input: $input) { success }
}`
	vars := map[string]any{
		"input": map[string]any{
			"agentSessionId": sessionID,
			"content":        a.content(),
		},
	}
	var resp struct {
		AgentActivityCreate struct {
			Success bool `json:"success"`
		} `json:"agentActivityCreate"`
	}
	if err := c.do(ctx, mutation, vars, &resp); err != nil {
		return err
	}
	if !resp.AgentActivityCreate.Success {
		return fmt.Errorf("linear: agentActivityCreate returned success=false")
	}
	return nil
}
