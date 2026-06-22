// Package linearagent implements h2's Linear agent-delegation integration:
// users delegate or @mention an issue to the h2 agent, Linear delivers
// AgentSession events (currently via an inbound webhook), and h2 spawns an
// agent to work the issue and reports progress back as agent-session
// activities.
package linearagent

import "strings"

// Webhook event/action constants.
const (
	TypeAgentSession = "AgentSessionEvent"

	// ActionCreated fires when a session begins (delegation or @mention). The
	// agent must post its first activity within ~10s of receiving this.
	ActionCreated = "created"
	// ActionPrompted fires when the human adds a follow-up prompt to an
	// existing session.
	ActionPrompted = "prompted"
)

// AgentSessionEvent is the subset of a Linear AgentSession webhook payload that
// h2 acts on. Unknown fields are ignored by the JSON decoder.
type AgentSessionEvent struct {
	Type           string       `json:"type"`
	Action         string       `json:"action"`
	OrganizationID string       `json:"organizationId"`
	AgentSession   AgentSession `json:"agentSession"`
	// Webhook delivery timestamp (epoch ms); used for replay protection.
	WebhookTimestamp int64 `json:"webhookTimestamp"`
}

// AgentSession identifies the session and carries its issue/comment context.
type AgentSession struct {
	ID      string   `json:"id"`
	Issue   Issue    `json:"issue"`
	Comment *Comment `json:"comment,omitempty"`
}

// Issue is the delegated issue's context.
type Issue struct {
	ID          string `json:"id"`
	Identifier  string `json:"identifier"` // e.g. "LIN-123"
	Title       string `json:"title"`
	Description string `json:"description"`
}

// Comment is the triggering comment, present for @mention/prompt triggers.
type Comment struct {
	ID   string `json:"id"`
	Body string `json:"body"`
}

// PromptText returns the best available natural-language context to seed the
// agent: the triggering comment if present, otherwise the issue title and
// description.
func (e AgentSessionEvent) PromptText() string {
	if e.AgentSession.Comment != nil {
		if b := strings.TrimSpace(e.AgentSession.Comment.Body); b != "" {
			return b
		}
	}
	var sb strings.Builder
	iss := e.AgentSession.Issue
	if t := strings.TrimSpace(iss.Title); t != "" {
		sb.WriteString(t)
	}
	if d := strings.TrimSpace(iss.Description); d != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(d)
	}
	return sb.String()
}

// IsAgentSession reports whether this is a well-formed agent-session event.
func (e AgentSessionEvent) IsAgentSession() bool {
	return e.Type == TypeAgentSession && e.AgentSession.ID != ""
}
