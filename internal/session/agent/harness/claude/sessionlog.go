package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"time"

	"h2/internal/session/agent/monitor"
)

// SessionLogCollector tails Claude Code's session JSONL file and emits
// EventAgentMessage events for assistant messages. This provides the
// full message text for peek.
type SessionLogCollector struct {
	path string
}

// NewSessionLogCollector creates a collector that tails the given JSONL path.
func NewSessionLogCollector(path string) *SessionLogCollector {
	return &SessionLogCollector{path: path}
}

// Run watches the session log file and emits EventAgentMessage for
// assistant messages. Blocks until ctx is cancelled.
//
// If the file doesn't exist yet, Run waits for it to appear (Claude Code
// creates it when the session starts). Lines that don't parse or aren't
// assistant messages are silently skipped.
func (c *SessionLogCollector) Run(ctx context.Context, events chan<- monitor.AgentEvent) {
	// Wait for the file to appear.
	var f *os.File
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		var err error
		f, err = os.Open(c.path)
		if err == nil {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	var partial []byte
	for {
		// Try to read all available lines.
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				// Partial data (no trailing newline yet) — accumulate.
				partial = append(partial, line...)
				break
			}
			if len(partial) > 0 {
				line = append(partial, line...)
				partial = nil
			}
			if ev, ok := parseSessionLine(line); ok {
				select {
				case events <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
		// Wait for more data.
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

// sessionLogEntry represents a single line in Claude Code's session JSONL.
type sessionLogEntry struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message,omitempty"`
}

// sessionMessage is the message field for assistant entries.
type sessionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
}

// parseSessionLine parses a JSONL line and returns an AgentEvent if it's
// an assistant message.
func parseSessionLine(line []byte) (monitor.AgentEvent, bool) {
	var entry sessionLogEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return monitor.AgentEvent{}, false
	}

	if entry.Type != "assistant" {
		return monitor.AgentEvent{}, false
	}

	var msg sessionMessage
	if len(entry.Message) > 0 {
		if err := json.Unmarshal(entry.Message, &msg); err != nil {
			return monitor.AgentEvent{}, false
		}
	}

	if msg.Content == "" {
		return monitor.AgentEvent{}, false
	}

	return monitor.AgentEvent{
		Type:      monitor.EventAgentMessage,
		Timestamp: time.Now(),
		Data:      monitor.AgentMessageData{Content: msg.Content},
	}, true
}
