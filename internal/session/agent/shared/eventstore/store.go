// Package eventstore provides durable storage for normalized AgentEvents.
// One JSONL file per session in h2's session directory. The AgentMonitor
// appends every event as it processes it. Peek reads from this store
// instead of parsing agent-native log formats directly.
package eventstore

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"h2/internal/session/agent/monitor"
)

const eventsFileName = "events.jsonl"

// EventStore provides append/read access to a JSONL file of AgentEvents.
type EventStore struct {
	file *os.File
}

// Open creates or opens the events.jsonl file in the given session directory.
func Open(sessionDir string) (*EventStore, error) {
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return nil, fmt.Errorf("create eventstore dir: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(sessionDir, eventsFileName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open events file: %w", err)
	}
	return &EventStore{file: f}, nil
}

// Append JSON-encodes an AgentEvent and appends it as a single line.
func (s *EventStore) Append(event monitor.AgentEvent) error {
	env := toEnvelope(event)
	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	data = append(data, '\n')
	_, err = s.file.Write(data)
	return err
}

// Read reads all events from the file and returns them.
func (s *EventStore) Read() ([]monitor.AgentEvent, error) {
	path := s.file.Name()
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open events for read: %w", err)
	}
	defer f.Close()
	return readEvents(f)
}

// Tail streams new events appended after the current end of file.
// The returned channel is closed when ctx is cancelled.
func (s *EventStore) Tail(ctx context.Context) (<-chan monitor.AgentEvent, error) {
	path := s.file.Name()
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open events for tail: %w", err)
	}

	// Seek to end so we only get new events.
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, fmt.Errorf("seek to end: %w", err)
	}

	ch := make(chan monitor.AgentEvent, 64)
	go func() {
		defer f.Close()
		defer close(ch)
		reader := bufio.NewReader(f)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
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
				ev, err := parseEvent(line)
				if err != nil {
					continue // skip malformed lines
				}
				select {
				case ch <- ev:
				case <-ctx.Done():
					return
				}
			}
			// Wait for more data or cancellation.
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// Close closes the underlying file.
func (s *EventStore) Close() error {
	return s.file.Close()
}

// ReadEventsFile reads all events from events.jsonl in the given session directory.
// This is a standalone function that does not require an open EventStore.
func ReadEventsFile(sessionDir string) ([]monitor.AgentEvent, error) {
	f, err := os.Open(filepath.Join(sessionDir, eventsFileName))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readEvents(f)
}

// --- Wire format ---

// eventEnvelope is the JSON representation of an AgentEvent on disk.
type eventEnvelope struct {
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// eventTypeToString maps AgentEventType to its string representation.
var eventTypeToString = map[monitor.AgentEventType]string{
	monitor.EventSessionStarted:    "session_started",
	monitor.EventTurnStarted:       "turn_started",
	monitor.EventTurnCompleted:     "turn_completed",
	monitor.EventToolStarted:       "tool_started",
	monitor.EventToolCompleted:     "tool_completed",
	monitor.EventApprovalRequested: "approval_requested",
	monitor.EventAgentMessage:      "agent_message",
	monitor.EventStateChange:       "state_change",
	monitor.EventSessionEnded:      "session_ended",
}

// stringToEventType maps string to AgentEventType.
var stringToEventType map[string]monitor.AgentEventType

func init() {
	stringToEventType = make(map[string]monitor.AgentEventType, len(eventTypeToString))
	for k, v := range eventTypeToString {
		stringToEventType[v] = k
	}
}

func toEnvelope(ev monitor.AgentEvent) eventEnvelope {
	env := eventEnvelope{
		Type:      eventTypeToString[ev.Type],
		Timestamp: ev.Timestamp,
	}
	if ev.Data != nil {
		data, _ := json.Marshal(ev.Data)
		env.Data = data
	}
	return env
}

func parseEvent(line []byte) (monitor.AgentEvent, error) {
	var env eventEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		return monitor.AgentEvent{}, err
	}

	evType, ok := stringToEventType[env.Type]
	if !ok {
		return monitor.AgentEvent{}, fmt.Errorf("unknown event type: %s", env.Type)
	}

	ev := monitor.AgentEvent{
		Type:      evType,
		Timestamp: env.Timestamp,
	}

	if len(env.Data) > 0 {
		data, err := unmarshalData(evType, env.Data)
		if err != nil {
			return monitor.AgentEvent{}, fmt.Errorf("unmarshal data for %s: %w", env.Type, err)
		}
		ev.Data = data
	}

	return ev, nil
}

func unmarshalData(evType monitor.AgentEventType, raw json.RawMessage) (any, error) {
	switch evType {
	case monitor.EventSessionStarted:
		var d monitor.SessionStartedData
		return d, json.Unmarshal(raw, &d)
	case monitor.EventTurnStarted:
		var d monitor.TurnStartedData
		return d, json.Unmarshal(raw, &d)
	case monitor.EventTurnCompleted:
		var d monitor.TurnCompletedData
		return d, json.Unmarshal(raw, &d)
	case monitor.EventToolStarted:
		var d monitor.ToolStartedData
		return d, json.Unmarshal(raw, &d)
	case monitor.EventToolCompleted:
		var d monitor.ToolCompletedData
		return d, json.Unmarshal(raw, &d)
	case monitor.EventApprovalRequested:
		var d monitor.ApprovalRequestedData
		return d, json.Unmarshal(raw, &d)
	case monitor.EventAgentMessage:
		var d monitor.AgentMessageData
		return d, json.Unmarshal(raw, &d)
	case monitor.EventStateChange:
		var d monitor.StateChangeData
		return d, json.Unmarshal(raw, &d)
	case monitor.EventSessionEnded:
		var d monitor.SessionEndedData
		return d, json.Unmarshal(raw, &d)
	default:
		// For event types without a known payload struct, preserve raw JSON.
		var d map[string]any
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, err
		}
		return d, nil
	}
}

func readEvents(r io.Reader) ([]monitor.AgentEvent, error) {
	var events []monitor.AgentEvent
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		ev, err := parseEvent(line)
		if err != nil {
			continue // skip malformed lines
		}
		events = append(events, ev)
	}
	return events, scanner.Err()
}
