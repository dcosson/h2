package agent

import (
	"path/filepath"

	"h2/internal/activitylog"
	"h2/internal/session/agent/adapter"
	"h2/internal/session/agent/adapter/claude"
	"h2/internal/session/agent/adapter/codex"
)

// AgentType defines how h2 launches, monitors, and interacts with a specific
// kind of agent. Each supported agent (Claude Code, Codex, generic shell)
// implements this interface.
type AgentType interface {
	// Name returns the agent type identifier (e.g. "claude", "generic").
	Name() string

	// Command returns the executable to run.
	Command() string

	// DisplayCommand returns the command name for display purposes.
	DisplayCommand() string

	// NewAdapter creates an AgentAdapter for this agent type.
	// Returns nil for agent types that don't support adapters (e.g. generic).
	// The adapter handles telemetry collection, hook events, and emits
	// normalized AgentEvents to the monitor.
	NewAdapter(log *activitylog.Logger) adapter.AgentAdapter
}

// ClaudeCodeType provides full integration: OTEL, hooks, session ID, env vars.
type ClaudeCodeType struct{}

// NewClaudeCodeType creates a ClaudeCodeType agent.
func NewClaudeCodeType() *ClaudeCodeType {
	return &ClaudeCodeType{}
}

func (t *ClaudeCodeType) Name() string         { return "claude" }
func (t *ClaudeCodeType) Command() string       { return "claude" }
func (t *ClaudeCodeType) DisplayCommand() string { return "claude" }

// NewAdapter creates a Claude Code adapter with OTEL + hooks support.
func (t *ClaudeCodeType) NewAdapter(log *activitylog.Logger) adapter.AgentAdapter {
	return claude.New(log)
}

// CodexType provides integration for OpenAI Codex CLI: OTEL traces, no hooks.
type CodexType struct{}

// NewCodexType creates a CodexType agent.
func NewCodexType() *CodexType {
	return &CodexType{}
}

func (t *CodexType) Name() string         { return "codex" }
func (t *CodexType) Command() string       { return "codex" }
func (t *CodexType) DisplayCommand() string { return "codex" }

// NewAdapter creates a Codex adapter with OTEL trace support.
func (t *CodexType) NewAdapter(log *activitylog.Logger) adapter.AgentAdapter {
	return codex.New(log)
}

// RoleArgs maps role configuration to Codex CLI flags.
func (t *CodexType) RoleArgs(model, permissionMode string) []string {
	var args []string
	if model != "" {
		args = append(args, "--model", model)
	}
	switch permissionMode {
	case "full-auto":
		args = append(args, "--full-auto")
	case "suggest":
		args = append(args, "--suggest")
	case "ask":
		// --ask is the default for Codex, no flag needed.
	default:
		// Default to full-auto for h2-managed agents.
		args = append(args, "--full-auto")
	}
	return args
}

// GenericType is a fallback for unknown agents — no adapter, output-based state detection.
type GenericType struct {
	command string
}

// NewGenericType creates a GenericType for the given command.
func NewGenericType(command string) *GenericType {
	return &GenericType{command: command}
}

func (t *GenericType) Name() string         { return "generic" }
func (t *GenericType) Command() string       { return t.command }
func (t *GenericType) DisplayCommand() string { return t.command }

// NewAdapter returns nil — generic agents use output-based state detection
// instead of an adapter.
func (t *GenericType) NewAdapter(log *activitylog.Logger) adapter.AgentAdapter {
	return nil
}

// ResolveAgentType maps a command name to a known agent type,
// falling back to GenericType for unknown commands.
func ResolveAgentType(command string) AgentType {
	switch filepath.Base(command) {
	case "claude":
		return NewClaudeCodeType()
	case "codex":
		return NewCodexType()
	default:
		return NewGenericType(command)
	}
}
