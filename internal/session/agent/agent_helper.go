package agent

import "fmt"

// AgentHelper provides agent-specific configuration for a Session.
// Different agents (Claude Code, etc.) implement this interface.
type AgentHelper interface {
	// OtelParser returns the parser for this agent's OTEL events.
	OtelParser() OtelParser

	// OtelEnv returns environment variables to inject for OTEL telemetry.
	// The otelPort parameter is the port the collector is listening on.
	OtelEnv(otelPort int) map[string]string
}

// ClaudeCodeHelper provides Claude Code-specific configuration.
type ClaudeCodeHelper struct {
	parser *ClaudeCodeParser
}

// NewClaudeCodeHelper creates a helper for Claude Code sessions.
func NewClaudeCodeHelper() *ClaudeCodeHelper {
	return &ClaudeCodeHelper{
		parser: NewClaudeCodeParser(),
	}
}

// OtelParser returns the Claude Code OTEL parser.
func (h *ClaudeCodeHelper) OtelParser() OtelParser {
	return h.parser
}

// OtelEnv returns the environment variables needed to enable OTEL in Claude Code.
func (h *ClaudeCodeHelper) OtelEnv(otelPort int) map[string]string {
	if otelPort == 0 {
		return nil
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", otelPort)
	return map[string]string{
		"CLAUDE_CODE_ENABLE_TELEMETRY": "1",
		"OTEL_METRICS_EXPORTER":        "otlp",
		"OTEL_LOGS_EXPORTER":           "otlp",
		"OTEL_TRACES_EXPORTER":         "none",
		"OTEL_EXPORTER_OTLP_PROTOCOL":  "http/json",
		"OTEL_EXPORTER_OTLP_ENDPOINT":  endpoint,
		// Export intervals
		"OTEL_METRIC_EXPORT_INTERVAL": "5000",
		"OTEL_LOGS_EXPORT_INTERVAL":   "1000",
	}
}

// GenericAgentHelper is a fallback for unknown agents.
// It has no parser (OTEL events are ignored) and no special env vars.
type GenericAgentHelper struct{}

// NewGenericAgentHelper creates a helper for unknown agents.
func NewGenericAgentHelper() *GenericAgentHelper {
	return &GenericAgentHelper{}
}

// OtelParser returns nil — unknown agents don't have parsers.
func (h *GenericAgentHelper) OtelParser() OtelParser {
	return nil
}

// OtelEnv returns nil — unknown agents don't need special env vars.
func (h *GenericAgentHelper) OtelEnv(otelPort int) map[string]string {
	return nil
}
