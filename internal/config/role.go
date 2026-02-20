package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"h2/internal/tmpl"

	"gopkg.in/yaml.v3"
)

// HeartbeatConfig defines a heartbeat nudge mechanism for idle agents.
type HeartbeatConfig struct {
	IdleTimeout string `yaml:"idle_timeout"`
	Message     string `yaml:"message"`
	Condition   string `yaml:"condition,omitempty"`
}

// ParseIdleTimeout parses the IdleTimeout string as a Go duration.
func (k *HeartbeatConfig) ParseIdleTimeout() (time.Duration, error) {
	return time.ParseDuration(k.IdleTimeout)
}

// WorktreeConfig defines git worktree settings for an agent.
// Presence of this block implies worktree is enabled (no separate "enabled" flag).
// Mutually exclusive with Role.WorkingDir.
type WorktreeConfig struct {
	ProjectDir      string `yaml:"project_dir"`                 // required: source git repo
	Name            string `yaml:"name"`                        // required: worktree dir name under <h2-dir>/worktrees/
	BranchFrom      string `yaml:"branch_from,omitempty"`       // default: "main"
	BranchName      string `yaml:"branch_name,omitempty"`       // default: Name
	UseDetachedHead bool   `yaml:"use_detached_head,omitempty"` // default: false
}

// GetBranchFrom returns the branch to base the worktree on, defaulting to "main".
func (w *WorktreeConfig) GetBranchFrom() string {
	if w.BranchFrom != "" {
		return w.BranchFrom
	}
	return "main"
}

// GetBranchName returns the branch name for the worktree, defaulting to Name.
func (w *WorktreeConfig) GetBranchName() string {
	if w.BranchName != "" {
		return w.BranchName
	}
	return w.Name
}

// ResolveProjectDir returns the absolute path for the worktree's source git repo.
// Relative paths are resolved against the h2 dir. Absolute paths are used as-is.
func (w *WorktreeConfig) ResolveProjectDir() (string, error) {
	dir := w.ProjectDir
	if dir == "" {
		return "", fmt.Errorf("worktree.project_dir is required")
	}
	if filepath.IsAbs(dir) {
		return dir, nil
	}
	// Relative path: resolve against h2 dir.
	h2Dir, err := ResolveDir()
	if err != nil {
		return "", fmt.Errorf("resolve h2 dir for worktree.project_dir: %w", err)
	}
	return filepath.Join(h2Dir, dir), nil
}

// Validate checks that the WorktreeConfig has required fields.
func (w *WorktreeConfig) Validate() error {
	if w.ProjectDir == "" {
		return fmt.Errorf("worktree.project_dir is required")
	}
	if w.Name == "" {
		return fmt.Errorf("worktree.name is required")
	}
	return nil
}

// WorktreesDir returns <h2-dir>/worktrees/.
func WorktreesDir() string {
	return filepath.Join(ConfigDir(), "worktrees")
}

// ValidPermissionModes lists all valid values for the permission_mode field.
var ValidPermissionModes = []string{
	"default", "delegate", "acceptEdits", "plan", "dontAsk", "bypassPermissions",
}

// AgentHarnessConfig holds harness-specific configuration nested under agent_harness.
type AgentHarnessConfig struct {
	HarnessType     string `yaml:"harness_type,omitempty"`
	Model           string `yaml:"model,omitempty"`
	Command         string `yaml:"command,omitempty"`           // generic only
	ClaudeConfigDir string `yaml:"claude_config_dir,omitempty"` // claude_code only
	CodexConfigDir  string `yaml:"codex_config_dir,omitempty"`  // codex only
}

// Role defines a named configuration bundle for an h2 agent.
type Role struct {
	Name            string                  `yaml:"name"`
	Description     string                  `yaml:"description,omitempty"`
	AgentHarness    *AgentHarnessConfig     `yaml:"agent_harness,omitempty"`

	// Deprecated top-level fields (backward compat — use agent_harness instead).
	AgentTypeLegacy       string            `yaml:"agent_type,omitempty"`
	ModelLegacy           string            `yaml:"model,omitempty"`
	ClaudeConfigDirLegacy string            `yaml:"claude_config_dir,omitempty"`

	WorkingDir      string                  `yaml:"working_dir,omitempty"`  // agent CWD (default ".")
	Worktree        *WorktreeConfig         `yaml:"worktree,omitempty"`    // git worktree settings
	SystemPrompt    string                  `yaml:"system_prompt,omitempty"` // replaces Claude's entire default system prompt (--system-prompt)
	Instructions    string                  `yaml:"instructions"`           // appended to default system prompt (--append-system-prompt)
	PermissionMode  string                  `yaml:"permission_mode,omitempty"` // Claude CLI --permission-mode flag
	Permissions     Permissions             `yaml:"permissions,omitempty"`
	Heartbeat       *HeartbeatConfig        `yaml:"heartbeat,omitempty"`
	Hooks           yaml.Node               `yaml:"hooks,omitempty"`      // passed through as-is to settings.json
	Settings        yaml.Node               `yaml:"settings,omitempty"`   // extra settings.json keys
	Variables       map[string]tmpl.VarDef  `yaml:"variables,omitempty"`  // template variable definitions
}

// ResolveWorkingDir returns the absolute path for the agent's working directory.
// "." (or empty) is interpreted as invocationCWD. Relative paths are resolved
// against the h2 dir. Absolute paths are used as-is.
func (r *Role) ResolveWorkingDir(invocationCWD string) (string, error) {
	dir := r.WorkingDir
	if dir == "" || dir == "." {
		return invocationCWD, nil
	}
	if filepath.IsAbs(dir) {
		return dir, nil
	}
	// Relative path: resolve against h2 dir.
	h2Dir, err := ResolveDir()
	if err != nil {
		return "", fmt.Errorf("resolve h2 dir for working_dir: %w", err)
	}
	return filepath.Join(h2Dir, dir), nil
}

// GetHarnessType returns the canonical harness type name, defaulting to "claude_code".
// Checks AgentHarness.HarnessType first, then falls back to legacy AgentType
// (mapping "claude" → "claude_code").
func (r *Role) GetHarnessType() string {
	if r.AgentHarness != nil && r.AgentHarness.HarnessType != "" {
		return r.AgentHarness.HarnessType
	}
	if r.AgentTypeLegacy != "" {
		if r.AgentTypeLegacy == "claude" {
			return "claude_code"
		}
		return r.AgentTypeLegacy
	}
	return "claude_code"
}

// GetAgentType returns the command name for this role's agent type.
// Defaults to "claude". Maps canonical harness type names back to
// command names: "claude_code" → "claude".
func (r *Role) GetAgentType() string {
	ht := r.GetHarnessType()
	switch ht {
	case "claude_code":
		return "claude"
	case "generic":
		if r.AgentHarness != nil && r.AgentHarness.Command != "" {
			return r.AgentHarness.Command
		}
		return ht
	default:
		return ht // "codex" etc. match command name
	}
}

// GetModel returns the model, checking nested AgentHarness config first,
// then falling back to the legacy top-level field.
func (r *Role) GetModel() string {
	if r.AgentHarness != nil && r.AgentHarness.Model != "" {
		return r.AgentHarness.Model
	}
	return r.ModelLegacy
}

// GetCodexConfigDir returns the Codex config directory from the nested config.
// Returns empty string if not set.
func (r *Role) GetCodexConfigDir() string {
	if r.AgentHarness != nil && r.AgentHarness.CodexConfigDir != "" {
		return r.AgentHarness.CodexConfigDir
	}
	return ""
}

// Permissions defines the permission configuration for a role.
type Permissions struct {
	Allow []string         `yaml:"allow,omitempty"`
	Deny  []string         `yaml:"deny,omitempty"`
	Agent *PermissionAgent `yaml:"agent,omitempty"`
}

// PermissionAgent configures the AI permission reviewer.
type PermissionAgent struct {
	Enabled      *bool  `yaml:"enabled,omitempty"` // defaults to true if instructions are set
	Instructions string `yaml:"instructions,omitempty"`
}

// IsEnabled returns whether the permission agent is enabled.
// Defaults to true when instructions are present.
func (pa *PermissionAgent) IsEnabled() bool {
	if pa.Enabled != nil {
		return *pa.Enabled
	}
	return pa.Instructions != ""
}

// RolesDir returns the directory where role files are stored (~/.h2/roles/).
func RolesDir() string {
	return filepath.Join(ConfigDir(), "roles")
}

// DefaultClaudeConfigDir returns the default shared Claude config directory.
func DefaultClaudeConfigDir() string {
	return filepath.Join(ConfigDir(), "claude-config", "default")
}

// GetClaudeConfigDir returns the Claude config directory for this role.
// Checks AgentHarness.ClaudeConfigDir first, then legacy ClaudeConfigDirLegacy.
// If not specified in either, returns the default shared config dir.
// If set to "~/" (the home directory), returns "" to indicate that
// CLAUDE_CONFIG_DIR should not be overridden (use system default).
func (r *Role) GetClaudeConfigDir() string {
	dir := r.ClaudeConfigDirLegacy
	if r.AgentHarness != nil && r.AgentHarness.ClaudeConfigDir != "" {
		dir = r.AgentHarness.ClaudeConfigDir
	}
	if dir != "" {
		return expandClaudeConfigDir(dir)
	}
	return DefaultClaudeConfigDir()
}

// expandClaudeConfigDir handles tilde expansion for Claude config dir paths.
func expandClaudeConfigDir(dir string) string {
	if strings.HasPrefix(dir, "~/") {
		rest := dir[2:]
		if rest == "" {
			// "~/" means use system default — don't override CLAUDE_CONFIG_DIR.
			return ""
		}
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, rest)
		}
	}
	return dir
}

// IsClaudeConfigAuthenticated checks if the given Claude config directory
// has been authenticated (i.e., has a valid .claude.json with oauthAccount).
func IsClaudeConfigAuthenticated(configDir string) (bool, error) {
	claudeJSON := filepath.Join(configDir, ".claude.json")

	// Check if .claude.json exists
	data, err := os.ReadFile(claudeJSON)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read .claude.json: %w", err)
	}

	// Parse and check for oauthAccount field
	var config struct {
		OAuthAccount *struct {
			AccountUUID  string `json:"accountUuid"`
			EmailAddress string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return false, fmt.Errorf("parse .claude.json: %w", err)
	}

	// Consider authenticated if oauthAccount exists and has required fields
	return config.OAuthAccount != nil &&
		config.OAuthAccount.AccountUUID != "" &&
		config.OAuthAccount.EmailAddress != "", nil
}

// IsRoleAuthenticated checks if the role's Claude config directory is authenticated.
func (r *Role) IsRoleAuthenticated() (bool, error) {
	return IsClaudeConfigAuthenticated(r.GetClaudeConfigDir())
}

// LoadRole loads a role by name from ~/.h2/roles/<name>.yaml.
func LoadRole(name string) (*Role, error) {
	path := filepath.Join(RolesDir(), name+".yaml")
	return LoadRoleFrom(path)
}

// LoadRoleFrom loads a role from the given file path.
func LoadRoleFrom(path string) (*Role, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read role file: %w", err)
	}

	var role Role
	if err := yaml.Unmarshal(data, &role); err != nil {
		return nil, fmt.Errorf("parse role YAML: %w", err)
	}

	if err := role.Validate(); err != nil {
		return nil, fmt.Errorf("invalid role %q: %w", path, err)
	}

	return &role, nil
}

// LoadRoleRendered loads a role by name, rendering it with the given template context.
// If ctx is nil, behaves like LoadRole (no rendering — backward compat).
func LoadRoleRendered(name string, ctx *tmpl.Context) (*Role, error) {
	path := filepath.Join(RolesDir(), name+".yaml")
	return LoadRoleRenderedFrom(path, ctx)
}

// LoadRoleRenderedFrom loads a role from the given file path, rendering it with
// the given template context. If ctx is nil, behaves like LoadRoleFrom.
func LoadRoleRenderedFrom(path string, ctx *tmpl.Context) (*Role, error) {
	if ctx == nil {
		return LoadRoleFrom(path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read role file: %w", err)
	}

	// Extract variables section before rendering.
	defs, remaining, err := tmpl.ParseVarDefs(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse variables in role %q: %w", path, err)
	}

	// Clone ctx.Var so we don't mutate the caller's map.
	vars := make(map[string]string, len(ctx.Var))
	for k, v := range ctx.Var {
		vars[k] = v
	}
	for name, def := range defs {
		if _, ok := vars[name]; !ok && def.Default != nil {
			vars[name] = *def.Default
		}
	}

	// Validate that all required variables are present.
	if err := tmpl.ValidateVars(defs, vars); err != nil {
		return nil, fmt.Errorf("role %q: %w", filepath.Base(path), err)
	}

	// Render template with cloned vars.
	renderCtx := *ctx
	renderCtx.Var = vars
	rendered, err := tmpl.Render(remaining, &renderCtx)
	if err != nil {
		return nil, fmt.Errorf("template error in role %q (%s): %w", filepath.Base(path), path, err)
	}

	var role Role
	if err := yaml.Unmarshal([]byte(rendered), &role); err != nil {
		return nil, fmt.Errorf("parse rendered role YAML %q: %w", path, err)
	}

	role.Variables = defs

	if err := role.Validate(); err != nil {
		return nil, fmt.Errorf("invalid role %q: %w", path, err)
	}

	return &role, nil
}

// ListRoles returns all available roles from ~/.h2/roles/.
func ListRoles() ([]*Role, error) {
	dir := RolesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read roles dir: %w", err)
	}

	var roles []*Role
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		roleName := strings.TrimSuffix(entry.Name(), ".yaml")
		// Try rendered load first (handles template files like role init generates).
		ctx := &tmpl.Context{
			RoleName: roleName,
			H2Dir:    ConfigDir(),
		}
		role, err := LoadRoleRenderedFrom(path, ctx)
		if err != nil {
			// Fallback to plain load (handles roles with required vars).
			role, err = LoadRoleFrom(path)
			if err != nil {
				continue
			}
		}
		roles = append(roles, role)
	}
	return roles, nil
}

// Validate checks that a role has the minimum required fields.
func (r *Role) Validate() error {
	if r.Name == "" {
		return fmt.Errorf("name is required")
	}
	if r.Instructions == "" && r.SystemPrompt == "" {
		return fmt.Errorf("at least one of instructions or system_prompt is required")
	}
	if r.PermissionMode != "" {
		valid := false
		for _, mode := range ValidPermissionModes {
			if r.PermissionMode == mode {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("invalid permission_mode %q; valid values: %s",
				r.PermissionMode, strings.Join(ValidPermissionModes, ", "))
		}
	}
	// working_dir and worktree are mutually exclusive (working_dir "." or empty is allowed).
	if r.Worktree != nil && r.WorkingDir != "" && r.WorkingDir != "." {
		return fmt.Errorf("working_dir and worktree are mutually exclusive; use worktree.project_dir instead")
	}
	if r.Worktree != nil {
		if err := r.Worktree.Validate(); err != nil {
			return err
		}
	}
	return nil
}
