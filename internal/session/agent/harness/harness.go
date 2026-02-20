// Package harness defines the Harness interface — a unified abstraction
// for agent integrations (Claude Code, Codex, generic). Each harness
// encapsulates all agent-type-specific behavior: config, launch, telemetry,
// hooks, and lifecycle.
package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"h2/internal/activitylog"
	"h2/internal/session/agent/monitor"
)

// Harness defines how h2 launches, monitors, and interacts with a specific
// kind of agent. Each supported agent (Claude Code, Codex, generic shell)
// implements this interface, merging the old AgentType + AgentAdapter split.
type Harness interface {
	// Identity
	Name() string           // "claude_code", "codex", or "generic"
	Command() string        // executable name: "claude", "codex", or custom
	DisplayCommand() string // for display

	// Config (called before launch)
	BuildCommandArgs(cfg CommandArgsConfig) []string
	BuildCommandEnvVars(h2Dir string) map[string]string
	EnsureConfigDir(h2Dir string) error

	// Launch (called once, before child process starts)
	PrepareForLaunch(agentName, sessionID string) (LaunchConfig, error)

	// Runtime (called after child process starts)
	Start(ctx context.Context, events chan<- monitor.AgentEvent) error
	HandleHookEvent(eventName string, payload json.RawMessage) bool
	HandleOutput() // signal that child process produced output
	Stop()
}

// HarnessConfig holds harness-specific configuration extracted from the Role.
// Passed to Resolve() and individual harness constructors.
type HarnessConfig struct {
	HarnessType string // "claude_code", "codex", or "generic"
	Model       string // model name (shared by claude/codex; empty for generic)
	ConfigDir   string // harness-specific config dir (resolved by Role)
	Command     string // executable command (only used by generic)
}

// CommandArgsConfig holds role configuration fields to be mapped to CLI flags.
// Each harness maps these to its own flag conventions.
type CommandArgsConfig struct {
	SessionID       string
	Instructions    string
	SystemPrompt    string
	Model           string
	PermissionMode  string
	AllowedTools    []string
	DisallowedTools []string
}

// LaunchConfig holds configuration to inject into the agent child process.
type LaunchConfig struct {
	Env         map[string]string // extra env vars for child process
	PrependArgs []string          // args to prepend before user args
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

// Resolve maps a HarnessConfig to a concrete Harness implementation.
// Returns an error for unknown harness types or invalid configs.
// Placeholder: actual harness constructors will be wired in tasks .3-.5.
func Resolve(cfg HarnessConfig, log *activitylog.Logger) (Harness, error) {
	switch cfg.HarnessType {
	case "claude_code", "claude":
		// placeholder: will be claude.New(cfg, log)
		return nil, fmt.Errorf("claude_code harness not yet implemented")
	case "codex":
		// placeholder: will be codex.New(cfg, log)
		return nil, fmt.Errorf("codex harness not yet implemented")
	case "generic":
		if cfg.Command == "" {
			return nil, fmt.Errorf("generic harness requires a command")
		}
		// placeholder: will be generic.New(cfg)
		return nil, fmt.Errorf("generic harness not yet implemented")
	default:
		return nil, fmt.Errorf("unknown harness type: %q (supported: claude_code, codex, generic)", cfg.HarnessType)
	}
}
