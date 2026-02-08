package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SessionsDir returns the directory where agent session dirs are created (~/.h2/sessions/).
func SessionsDir() string {
	return filepath.Join(ConfigDir(), "sessions")
}

// SessionDir returns the session directory for a given agent name.
func SessionDir(agentName string) string {
	return filepath.Join(SessionsDir(), agentName)
}

// SetupSessionDir creates the session directory for an agent and generates
// Claude Code config files from the role definition.
func SetupSessionDir(agentName string, role *Role) (string, error) {
	sessionDir := SessionDir(agentName)
	claudeDir := filepath.Join(sessionDir, ".claude")

	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return "", fmt.Errorf("create session dir: %w", err)
	}

	// Generate CLAUDE.md from role instructions.
	claudeMD := filepath.Join(claudeDir, "CLAUDE.md")
	if err := os.WriteFile(claudeMD, []byte(role.Instructions), 0o644); err != nil {
		return "", fmt.Errorf("write CLAUDE.md: %w", err)
	}

	// Generate settings.json.
	settings := buildSettings(role)
	settingsJSON, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal settings.json: %w", err)
	}
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, settingsJSON, 0o644); err != nil {
		return "", fmt.Errorf("write settings.json: %w", err)
	}

	// Write permission-reviewer.md if permissions.agent is configured.
	if role.Permissions.Agent != nil && role.Permissions.Agent.IsEnabled() {
		reviewerPath := filepath.Join(sessionDir, "permission-reviewer.md")
		if err := os.WriteFile(reviewerPath, []byte(role.Permissions.Agent.Instructions), 0o644); err != nil {
			return "", fmt.Errorf("write permission-reviewer.md: %w", err)
		}
	}

	return sessionDir, nil
}

// hookEntry represents a single hook in the settings.json hooks array.
type hookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

// hookMatcher represents a matcher + hooks pair in settings.json.
type hookMatcher struct {
	Matcher string      `json:"matcher"`
	Hooks   []hookEntry `json:"hooks"`
}

// buildSettings constructs the settings.json content from a role.
func buildSettings(role *Role) map[string]any {
	settings := make(map[string]any)

	// Model.
	if role.Model != "" {
		settings["model"] = role.Model
	}

	// Permissions (allow/deny only — agent section is handled separately).
	perms := make(map[string]any)
	if len(role.Permissions.Allow) > 0 {
		perms["allow"] = role.Permissions.Allow
	}
	if len(role.Permissions.Deny) > 0 {
		perms["deny"] = role.Permissions.Deny
	}
	if len(perms) > 0 {
		settings["permissions"] = perms
	}

	// Hooks: h2 standard hooks + role custom hooks.
	hooks := buildHooks(role)
	settings["hooks"] = hooks

	// Merge any additional settings from the role.
	if role.Settings.Kind != 0 {
		var extra map[string]any
		raw, err := yaml.Marshal(&role.Settings)
		if err == nil {
			if json.Unmarshal(yamlToJSON(raw), &extra) == nil {
				for k, v := range extra {
					settings[k] = v
				}
			}
		}
	}

	return settings
}

// buildHooks creates the hooks section of settings.json with h2 standard hooks
// and any role-specific custom hooks merged in.
func buildHooks(role *Role) map[string][]hookMatcher {
	collectHook := hookEntry{
		Type:    "command",
		Command: "h2 hook collect",
		Timeout: 5,
	}

	permissionHook := hookEntry{
		Type:    "command",
		Command: "h2 permission-request",
		Timeout: 60,
	}

	// Standard hook events that get the collect hook.
	standardEvents := []string{
		"PreToolUse",
		"PostToolUse",
		"SessionStart",
		"Stop",
		"UserPromptSubmit",
	}

	hooks := make(map[string][]hookMatcher)

	for _, event := range standardEvents {
		hooks[event] = []hookMatcher{{
			Matcher: "",
			Hooks:   []hookEntry{collectHook},
		}}
	}

	// PermissionRequest gets the permission handler + collect hook.
	hooks["PermissionRequest"] = []hookMatcher{{
		Matcher: "",
		Hooks:   []hookEntry{permissionHook, collectHook},
	}}

	// TODO: merge role.Hooks custom hooks when we support that.

	return hooks
}

// yamlToJSON converts YAML bytes to JSON bytes via round-trip through interface{}.
func yamlToJSON(yamlBytes []byte) []byte {
	var v any
	if err := yaml.Unmarshal(yamlBytes, &v); err != nil {
		return nil
	}
	j, err := json.Marshal(convertYAMLToJSON(v))
	if err != nil {
		return nil
	}
	return j
}

// convertYAMLToJSON recursively converts YAML-decoded values to JSON-compatible types.
// YAML decodes maps as map[string]any but sometimes as map[any]any.
func convertYAMLToJSON(v any) any {
	switch v := v.(type) {
	case map[string]any:
		m := make(map[string]any)
		for k, val := range v {
			m[k] = convertYAMLToJSON(val)
		}
		return m
	case map[any]any:
		m := make(map[string]any)
		for k, val := range v {
			m[fmt.Sprint(k)] = convertYAMLToJSON(val)
		}
		return m
	case []any:
		arr := make([]any, len(v))
		for i, val := range v {
			arr[i] = convertYAMLToJSON(val)
		}
		return arr
	default:
		return v
	}
}

