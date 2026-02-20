package agent

import (
	"fmt"
	"path/filepath"
)

// AgentType defines how h2 launches, monitors, and interacts with a specific
// kind of agent. Each supported agent (Claude Code, generic shell, future types)
// implements this interface.
type AgentType interface {
	// Name returns the agent type identifier (e.g. "claude", "generic").
	Name() string

	// Command returns the executable to run.
	Command() string

	// PrependArgs returns extra args to inject before the user's args.
	// e.g. Claude returns ["--session-id", uuid] when sessionID is set.
	PrependArgs(sessionID string) []string

	// ChildEnv returns extra environment variables for the child process.
	// Called after collectors are started so it can include OTEL endpoints, etc.
	ChildEnv(cp *CollectorPorts) map[string]string

	// Collectors returns which collectors this agent type supports.
	Collectors() CollectorSet

	// OtelParser returns the parser for this agent's OTEL events.
	// Returns nil if OTEL is not supported or no parsing is needed.
	OtelParser() OtelParser

	// DisplayCommand returns the command name for display purposes.
	DisplayCommand() string
}

// CollectorPorts holds connection info for active collectors,
// passed to ChildEnv so the agent type can configure the child.
type CollectorPorts struct {
	OtelPort int // 0 if OTEL not active
}

// CollectorSet declares which collectors an agent type supports.
type CollectorSet struct {
	Otel  bool
	Hooks bool
}

// ClaudeCodeType provides full integration: OTEL, hooks, session ID, env vars.
type ClaudeCodeType struct {
	parser *ClaudeCodeParser
}

// NewClaudeCodeType creates a ClaudeCodeType agent.
func NewClaudeCodeType() *ClaudeCodeType {
	return &ClaudeCodeType{parser: NewClaudeCodeParser()}
}

func (t *ClaudeCodeType) Name() string           { return "claude" }
func (t *ClaudeCodeType) Command() string         { return "claude" }
func (t *ClaudeCodeType) DisplayCommand() string   { return "claude" }
func (t *ClaudeCodeType) Collectors() CollectorSet { return CollectorSet{Otel: true, Hooks: true} }
func (t *ClaudeCodeType) OtelParser() OtelParser   { return t.parser }

func (t *ClaudeCodeType) PrependArgs(sessionID string) []string {
	if sessionID != "" {
		return []string{"--session-id", sessionID}
	}
	return nil
}

func (t *ClaudeCodeType) ChildEnv(cp *CollectorPorts) map[string]string {
	if cp == nil || cp.OtelPort == 0 {
		return nil
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", cp.OtelPort)
	return map[string]string{
		"CLAUDE_CODE_ENABLE_TELEMETRY": "1",
		"OTEL_METRICS_EXPORTER":        "otlp",
		"OTEL_LOGS_EXPORTER":           "otlp",
		"OTEL_TRACES_EXPORTER":         "none",
		"OTEL_EXPORTER_OTLP_PROTOCOL":  "http/json",
		"OTEL_EXPORTER_OTLP_ENDPOINT":  endpoint,
		"OTEL_METRIC_EXPORT_INTERVAL":  "5000",
		"OTEL_LOGS_EXPORT_INTERVAL":    "1000",
	}
}

// CodexType provides integration for OpenAI Codex CLI: OTEL traces, no hooks.
type CodexType struct{}

// NewCodexType creates a CodexType agent.
func NewCodexType() *CodexType {
	return &CodexType{}
}

func (t *CodexType) Name() string           { return "codex" }
func (t *CodexType) Command() string         { return "codex" }
func (t *CodexType) DisplayCommand() string   { return "codex" }
func (t *CodexType) Collectors() CollectorSet { return CollectorSet{Otel: true, Hooks: false} }
func (t *CodexType) OtelParser() OtelParser   { return nil } // Codex OTEL parser in adapter

func (t *CodexType) PrependArgs(sessionID string) []string {
	// Codex doesn't use --session-id; OTEL config is via the adapter.
	return nil
}

func (t *CodexType) ChildEnv(cp *CollectorPorts) map[string]string {
	// Codex OTEL trace exporter config is via -c CLI arg in the adapter,
	// not environment variables.
	return nil
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

// GenericType is a fallback for unknown agents — no collectors, no special config.
type GenericType struct {
	command string
}

// NewGenericType creates a GenericType for the given command.
func NewGenericType(command string) *GenericType {
	return &GenericType{command: command}
}

func (t *GenericType) Name() string                                     { return "generic" }
func (t *GenericType) Command() string                                   { return t.command }
func (t *GenericType) DisplayCommand() string                             { return t.command }
func (t *GenericType) Collectors() CollectorSet                           { return CollectorSet{} }
func (t *GenericType) OtelParser() OtelParser                             { return nil }
func (t *GenericType) PrependArgs(sessionID string) []string              { return nil }
func (t *GenericType) ChildEnv(cp *CollectorPorts) map[string]string      { return nil }

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
