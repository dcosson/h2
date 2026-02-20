package agent

import (
	"path/filepath"
	"strings"

	"h2/internal/activitylog"
	"h2/internal/config"
	"h2/internal/session/agent/adapter"
	"h2/internal/session/agent/adapter/claude"
	"h2/internal/session/agent/adapter/codex"
)

// CommandArgsConfig holds role configuration fields to be mapped to CLI flags.
// Each AgentType maps these to its own flag conventions.
type CommandArgsConfig struct {
	Instructions    string
	SystemPrompt    string
	Model           string
	PermissionMode  string
	AllowedTools    []string
	DisallowedTools []string
}

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

	// BuildCommandArgs maps role configuration fields to CLI flags for this
	// agent type. Returns nil if no flags are applicable (e.g. generic).
	BuildCommandArgs(cfg CommandArgsConfig) []string

	// BuildCommandEnvVars returns environment variables to set for the child
	// process. h2Dir is the resolved h2 config directory; roleName is the
	// role name (empty means "default").
	BuildCommandEnvVars(h2Dir, roleName string) map[string]string

	// EnsureConfigDir creates any agent-type-specific config directories
	// needed before launching. Returns nil if no setup is needed.
	EnsureConfigDir(h2Dir, roleName string) error
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

// BuildCommandArgs maps role config to Claude Code CLI flags.
func (t *ClaudeCodeType) BuildCommandArgs(cfg CommandArgsConfig) []string {
	var args []string
	if cfg.SystemPrompt != "" {
		args = append(args, "--system-prompt", cfg.SystemPrompt)
	}
	if cfg.Instructions != "" {
		args = append(args, "--append-system-prompt", cfg.Instructions)
	}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	if cfg.PermissionMode != "" {
		args = append(args, "--permission-mode", cfg.PermissionMode)
	}
	if len(cfg.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(cfg.AllowedTools, ","))
	}
	if len(cfg.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(cfg.DisallowedTools, ","))
	}
	return args
}

// BuildCommandEnvVars returns env vars for Claude Code (CLAUDE_CONFIG_DIR).
func (t *ClaudeCodeType) BuildCommandEnvVars(h2Dir, roleName string) map[string]string {
	if roleName == "" {
		roleName = "default"
	}
	return map[string]string{
		"CLAUDE_CONFIG_DIR": filepath.Join(h2Dir, "claude-config", roleName),
	}
}

// EnsureConfigDir creates the Claude config directory and writes default settings.
func (t *ClaudeCodeType) EnsureConfigDir(h2Dir, roleName string) error {
	configDir := t.BuildCommandEnvVars(h2Dir, roleName)["CLAUDE_CONFIG_DIR"]
	return config.EnsureClaudeConfigDir(configDir)
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

// BuildCommandArgs maps role config to Codex CLI flags.
// SystemPrompt, AllowedTools, and DisallowedTools have no Codex equivalent
// and are silently ignored.
func (t *CodexType) BuildCommandArgs(cfg CommandArgsConfig) []string {
	var args []string
	if cfg.Instructions != "" {
		args = append(args, "-c", `instructions="`+cfg.Instructions+`"`)
	}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	switch cfg.PermissionMode {
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

// BuildCommandEnvVars returns an empty map — Codex doesn't need special env vars.
func (t *CodexType) BuildCommandEnvVars(h2Dir, roleName string) map[string]string {
	return nil
}

// EnsureConfigDir is a no-op for Codex.
func (t *CodexType) EnsureConfigDir(h2Dir, roleName string) error { return nil }

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

// BuildCommandArgs returns nil — generic agents don't use role flags.
func (t *GenericType) BuildCommandArgs(cfg CommandArgsConfig) []string {
	return nil
}

// BuildCommandEnvVars returns nil — generic agents don't need special env vars.
func (t *GenericType) BuildCommandEnvVars(h2Dir, roleName string) map[string]string {
	return nil
}

// EnsureConfigDir is a no-op for generic agents.
func (t *GenericType) EnsureConfigDir(h2Dir, roleName string) error { return nil }

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
