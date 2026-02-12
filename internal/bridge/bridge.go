package bridge

import (
	"context"
	"regexp"
	"strings"
)

// Bridge is the base interface all bridges implement.
type Bridge interface {
	Name() string
	Close() error
}

// Sender is the capability interface for bridges that can send messages.
type Sender interface {
	Send(ctx context.Context, text string) error
}

// InboundHandler is called when a message arrives from an external platform.
// targetAgent is empty string if no prefix was parsed (un-addressed).
type InboundHandler func(targetAgent string, body string)

// Receiver is the capability interface for bridges that can receive messages.
type Receiver interface {
	Start(ctx context.Context, handler InboundHandler) error
	Stop()
}

// TypingIndicator is the capability interface for bridges that can show a
// typing indicator (e.g. Telegram's "typing..." status).
type TypingIndicator interface {
	SendTyping(ctx context.Context) error
}

var agentTagRe = regexp.MustCompile(`^\[([a-zA-Z0-9_-]+)\]\s*`)

// ParseAgentTag extracts an "[agent-name]" tag from the start of text.
// Returns the agent name, or empty string if no tag found.
func ParseAgentTag(text string) string {
	m := agentTagRe.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[1]
}

// FormatAgentTag prepends an "[agent-name] " tag to text.
func FormatAgentTag(agent, text string) string {
	return "[" + agent + "] " + text
}

var agentPrefixRe = regexp.MustCompile(`^([a-zA-Z0-9_-]+):\s*(.*)$`)

// ParseAgentPrefix extracts an "agent-name: body" prefix from text.
// The agent name is lowercased to match socket naming conventions.
// Returns empty agent if no valid prefix found.
func ParseAgentPrefix(text string) (agent, body string) {
	m := agentPrefixRe.FindStringSubmatch(text)
	if m == nil {
		return "", text
	}
	return strings.ToLower(m[1]), m[2]
}

// ParseSlashCommand checks if text starts with /<command> where command
// is in the allowed list. Returns the command name and args string,
// or empty command if not matched.
func ParseSlashCommand(text string, allowed []string) (command, args string) {
	if !strings.HasPrefix(text, "/") {
		return "", ""
	}
	rest := text[1:] // strip leading /
	parts := strings.SplitN(rest, " ", 2)
	cmd := strings.TrimSpace(parts[0])
	if cmd == "" {
		return "", ""
	}
	for _, a := range allowed {
		if cmd == a {
			if len(parts) > 1 {
				args := strings.TrimSpace(parts[1])
				if args != "" {
					return cmd, args
				}
			}
			return cmd, ""
		}
	}
	return "", ""
}

var envelopeRe = regexp.MustCompile(`^\[(URGENT )?h2 message from: [^\]]+\]\s*`)

// StripH2Envelope strips "[h2 message from: X]" or "[URGENT h2 message from: X]"
// prefix from text. Returns the body unchanged if no envelope is present.
func StripH2Envelope(text string) string {
	loc := envelopeRe.FindStringIndex(text)
	if loc == nil {
		return text
	}
	return strings.TrimSpace(text[loc[1]:])
}
