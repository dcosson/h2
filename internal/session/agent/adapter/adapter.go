package adapter

import (
	"context"
	"encoding/json"
	"io"

	"h2/internal/session/agent/monitor"
)

// AgentAdapter translates agent-specific telemetry into normalized AgentEvents.
type AgentAdapter interface {
	// Name returns the adapter identifier (e.g. "claude-code", "codex").
	Name() string

	// PrepareForLaunch returns env vars and CLI args to inject into the
	// child process so the adapter can receive telemetry from it.
	// Called before the agent process starts. sessionID is the h2 session
	// ID (may be empty); adapters that need it include it in PrependArgs.
	PrepareForLaunch(agentName, sessionID string) (LaunchConfig, error)

	// Start begins consuming agent-specific events and emitting AgentEvents.
	// Blocks until ctx is cancelled or the adapter encounters a fatal error.
	Start(ctx context.Context, events chan<- monitor.AgentEvent) error

	// HandleHookEvent processes a hook event received on the agent's Unix
	// socket. Not all adapters use hooks — return false if not handled.
	HandleHookEvent(eventName string, payload json.RawMessage) bool

	// Stop cleans up resources (HTTP servers, goroutines, etc).
	Stop()
}

// InputSender delivers input to an agent process.
// The default implementation writes to PTY stdin, but agent types
// with richer APIs can override this.
type InputSender interface {
	// SendInput delivers text input to the agent.
	SendInput(text string) error

	// SendInterrupt sends an interrupt signal (e.g. Ctrl+C).
	SendInterrupt() error
}

// LaunchConfig holds configuration to inject into the agent child process.
type LaunchConfig struct {
	Env         map[string]string // extra env vars for child process
	PrependArgs []string          // args to prepend before user args
}

// PTYInputSender is the default InputSender that writes to a PTY master.
// It works for any agent type that accepts input via stdin (Claude Code,
// Codex, generic commands).
type PTYInputSender struct {
	pty io.Writer // PTY master file descriptor
}

// NewPTYInputSender creates a PTYInputSender that writes to the given writer.
// Typically called with vt.Ptm (the PTY master *os.File).
func NewPTYInputSender(pty io.Writer) *PTYInputSender {
	return &PTYInputSender{pty: pty}
}

// SendInput writes text to the PTY stdin.
func (s *PTYInputSender) SendInput(text string) error {
	_, err := s.pty.Write([]byte(text))
	return err
}

// SendInterrupt sends Ctrl+C (ETX, 0x03) to the PTY.
func (s *PTYInputSender) SendInterrupt() error {
	_, err := s.pty.Write([]byte{0x03})
	return err
}
