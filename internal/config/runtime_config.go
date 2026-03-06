package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RuntimeConfig is the fully-resolved, serialized configuration for a daemon
// session. It is written to <session-dir>/session.metadata.json by the launcher
// and read by the daemon on startup. It serves as both the daemon's input
// config and the persistent session record for resume and tooling (peek, stats).
type RuntimeConfig struct {
	// Identity & provenance.
	AgentName string `json:"agent_name"`
	SessionID string `json:"session_id"`
	RoleName  string `json:"role,omitempty"`
	Pod       string `json:"pod,omitempty"`

	// Harness configuration.
	HarnessType             string `json:"harness_type"`
	HarnessConfigPathPrefix string `json:"harness_config_path_prefix,omitempty"` // e.g. <H2Dir>/claude-config
	Profile                 string `json:"profile,omitempty"`                    // profile name within prefix (default: "default")
	// HarnessSessionID is the session ID as known by the underlying harness
	// (e.g. Claude Code's session UUID, Codex's conversation.id). For harnesses
	// that accept a session-id input arg (currently just Claude Code), this will
	// equal SessionID since h2 generates the UUID and passes it through. For
	// other harnesses (Codex, future), this is reported async after launch and
	// will differ from SessionID. Use HarnessSessionID to look up session logs
	// in the harness's own config directory.
	HarnessSessionID string   `json:"harness_session_id,omitempty"`
	Command          string   `json:"command"`
	Args             []string `json:"args,omitempty"`
	Model            string   `json:"model,omitempty"`

	// Working directory.
	CWD string `json:"cwd"`

	// Prompt configuration.
	Instructions string `json:"instructions,omitempty"`
	SystemPrompt string `json:"system_prompt,omitempty"`

	// Permission configuration.
	ClaudePermissionMode string `json:"claude_permission_mode,omitempty"`
	CodexSandboxMode     string `json:"codex_sandbox_mode,omitempty"`
	CodexAskForApproval  string `json:"codex_ask_for_approval,omitempty"`

	// Additional directories.
	AdditionalDirs []string `json:"additional_dirs,omitempty"`

	// Heartbeat nudge configuration.
	HeartbeatIdleTimeout string `json:"heartbeat_idle_timeout,omitempty"` // Go duration string
	HeartbeatMessage     string `json:"heartbeat_message,omitempty"`
	HeartbeatCondition   string `json:"heartbeat_condition,omitempty"`

	// Overrides (recorded for display/debugging).
	Overrides map[string]string `json:"overrides,omitempty"`

	// Timestamps.
	StartedAt string `json:"started_at"`

	// ResumeSessionID is a transient field (not serialized) set by the daemon
	// when resuming a previous session. The harness uses it to pass --resume.
	ResumeSessionID string `json:"-"`
}

const runtimeConfigFilename = "session.metadata.json"

// WriteRuntimeConfig atomically writes the RuntimeConfig to the session directory.
// Uses a unique temp file + fsync + rename to prevent corruption from concurrent
// readers or writers and to provide crash durability.
func WriteRuntimeConfig(sessionDir string, rc *RuntimeConfig) error {
	if sessionDir == "" {
		return nil
	}
	data, err := json.MarshalIndent(rc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime config: %w", err)
	}
	data = append(data, '\n')
	path := filepath.Join(sessionDir, runtimeConfigFilename)

	// Use a unique temp file to avoid races between concurrent writers.
	tmp, err := os.CreateTemp(sessionDir, runtimeConfigFilename+".*.tmp")
	if err != nil {
		return fmt.Errorf("create runtime config tmp: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write runtime config tmp: %w", err)
	}
	// Fsync the file for crash durability before rename.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("sync runtime config tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close runtime config tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename runtime config: %w", err)
	}
	// Best-effort fsync of parent directory for rename durability.
	if dir, err := os.Open(sessionDir); err == nil {
		dir.Sync() //nolint:errcheck
		dir.Close()
	}
	return nil
}

// ReadRuntimeConfig reads and validates the RuntimeConfig from a session directory.
// Returns an error if the file is missing, malformed, or has missing required fields.
func ReadRuntimeConfig(sessionDir string) (*RuntimeConfig, error) {
	path := filepath.Join(sessionDir, runtimeConfigFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rc RuntimeConfig
	if err := json.Unmarshal(data, &rc); err != nil {
		return nil, fmt.Errorf("parse runtime config: %w", err)
	}

	if err := rc.Validate(); err != nil {
		return nil, err
	}

	return &rc, nil
}

// runtimeConfigRequiredFields lists fields that must be non-empty for a valid RuntimeConfig.
var runtimeConfigRequiredFields = []struct {
	name  string
	value func(*RuntimeConfig) string
}{
	{"agent_name", func(rc *RuntimeConfig) string { return rc.AgentName }},
	{"session_id", func(rc *RuntimeConfig) string { return rc.SessionID }},
	{"harness_type", func(rc *RuntimeConfig) string { return rc.HarnessType }},
	{"command", func(rc *RuntimeConfig) string { return rc.Command }},
	{"cwd", func(rc *RuntimeConfig) string { return rc.CWD }},
	{"started_at", func(rc *RuntimeConfig) string { return rc.StartedAt }},
}

// Validate checks that all required fields are present and non-empty.
func (rc *RuntimeConfig) Validate() error {
	var missing []string
	for _, f := range runtimeConfigRequiredFields {
		if f.value(rc) == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("invalid runtime config: missing required fields: %s", strings.Join(missing, ", "))
	}
	return nil
}

// HarnessConfigDir returns the resolved harness config directory: prefix + "/" + profile.
// Returns empty string if no prefix is set.
func (rc *RuntimeConfig) HarnessConfigDir() string {
	if rc.HarnessConfigPathPrefix == "" {
		return ""
	}
	profile := rc.Profile
	if profile == "" {
		profile = "default"
	}
	return filepath.Join(rc.HarnessConfigPathPrefix, profile)
}

// ParseHeartbeatIdleTimeout parses the HeartbeatIdleTimeout string as a Go duration.
// Returns zero duration if the field is empty.
func (rc *RuntimeConfig) ParseHeartbeatIdleTimeout() (time.Duration, error) {
	if rc.HeartbeatIdleTimeout == "" {
		return 0, nil
	}
	return time.ParseDuration(rc.HeartbeatIdleTimeout)
}
