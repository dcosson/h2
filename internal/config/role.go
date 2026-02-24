package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
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

// ValidCodexAskForApproval lists valid values for permissions.codex.ask_for_approval.
var ValidCodexAskForApproval = []string{
	"untrusted",  // ask for approval on every action (Codex default)
	"on-request", // model decides when to ask
	"never",      // never ask for approval
}

// ValidCodexSandboxModes lists valid values for permissions.codex.sandbox.
var ValidCodexSandboxModes = []string{
	"read-only",          // can only read (Codex default)
	"workspace-write",    // write to project dir only
	"danger-full-access", // no filesystem restrictions
}

// PermissionReviewAgent configures the AI permission reviewer.
type PermissionReviewAgent struct {
	Enabled      *bool  `yaml:"enabled,omitempty"` // defaults to true if instructions are set
	Instructions string `yaml:"instructions,omitempty"`
}

// IsEnabled returns whether the permission review agent is enabled.
// Defaults to true when instructions are present.
func (pa *PermissionReviewAgent) IsEnabled() bool {
	if pa.Enabled != nil {
		return *pa.Enabled
	}
	return pa.Instructions != ""
}

// Role defines a named configuration bundle for an h2 agent.
type Role struct {
	RoleName    string `yaml:"role_name"`
	AgentName   string `yaml:"agent_name,omitempty"` // agent name when launched; supports templates
	Description string `yaml:"description,omitempty"`

	// Harness fields.
	AgentHarness               string `yaml:"agent_harness,omitempty"`                  // claude_code | codex | generic
	AgentModel                 string `yaml:"agent_model,omitempty"`                    // explicit model; empty => agent app's own default
	AgentHarnessCommand        string `yaml:"agent_harness_command,omitempty"`          // command override for any harness
	AgentAccountProfile        string `yaml:"agent_account_profile,omitempty"`          // default account profile name ("default")
	ClaudeCodeConfigPath       string `yaml:"claude_code_config_path,omitempty"`        // explicit path override
	ClaudeCodeConfigPathPrefix string `yaml:"claude_code_config_path_prefix,omitempty"` // default: <H2Dir>/claude-config
	CodexConfigPath            string `yaml:"codex_config_path,omitempty"`              // explicit path override
	CodexConfigPathPrefix      string `yaml:"codex_config_path_prefix,omitempty"`       // default: <H2Dir>/codex-config

	WorkingDir           string                  `yaml:"working_dir,omitempty"`             // agent CWD (default ".")
	Worktree             *WorktreeConfig         `yaml:"worktree,omitempty"`                // git worktree settings
	SystemPrompt         string                  `yaml:"system_prompt,omitempty"`           // replaces Claude's entire default system prompt (--system-prompt)
	Instructions         string                  `yaml:"instructions,omitempty"`            // appended to default system prompt (--append-system-prompt)
	PermissionMode       string                  `yaml:"permission_mode,omitempty"`         // Claude Code --permission-mode flag
	CodexSandboxMode     string                  `yaml:"codex_sandbox_mode,omitempty"`      // Codex --sandbox flag
	CodexAskForApproval  string                  `yaml:"codex_ask_for_approval,omitempty"`  // Codex --ask-for-approval flag
	PermissionReviewAgent *PermissionReviewAgent  `yaml:"permission_review_agent,omitempty"` // AI permission reviewer
	Heartbeat            *HeartbeatConfig         `yaml:"heartbeat,omitempty"`
	Hooks                yaml.Node                `yaml:"hooks,omitempty"`     // passed through as-is to settings.json
	Settings             yaml.Node                `yaml:"settings,omitempty"`  // extra settings.json keys
	Variables            map[string]tmpl.VarDef   `yaml:"variables,omitempty"` // template variable definitions
}

// UnmarshalYAML decodes a role from YAML.
func (r *Role) UnmarshalYAML(value *yaml.Node) error {
	// Use an alias type to avoid infinite recursion.
	type roleAlias Role
	var aux roleAlias
	if err := value.Decode(&aux); err != nil {
		return err
	}
	*r = Role(aux)
	return nil
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
func (r *Role) GetHarnessType() string {
	if r.AgentHarness != "" {
		if r.AgentHarness == "claude" {
			return "claude_code"
		}
		return r.AgentHarness
	}
	return "claude_code"
}

// GetAgentType returns the command name for this role's agent type.
func (r *Role) GetAgentType() string {
	if r.AgentHarnessCommand != "" {
		return r.AgentHarnessCommand
	}
	return ""
}

// GetModel returns the explicit configured model, or empty string for the agent app's own default.
func (r *Role) GetModel() string {
	return r.AgentModel
}

// GetCodexConfigDir returns the Codex config directory.
func (r *Role) GetCodexConfigDir() string {
	if r.CodexConfigPath != "" {
		return r.CodexConfigPath
	}
	prefix := r.CodexConfigPathPrefix
	if prefix == "" {
		prefix = filepath.Join(ConfigDir(), "codex-config")
	}
	return filepath.Join(prefix, r.GetAgentAccountProfile())
}

// GetAgentAccountProfile returns the selected account profile name.
func (r *Role) GetAgentAccountProfile() string {
	if strings.TrimSpace(r.AgentAccountProfile) != "" {
		return strings.TrimSpace(r.AgentAccountProfile)
	}
	return "default"
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
// If set to "~/" (the home directory), returns "" to indicate that
// CLAUDE_CONFIG_DIR should not be overridden (use system default).
func (r *Role) GetClaudeConfigDir() string {
	if r.ClaudeCodeConfigPath != "" {
		return expandClaudeConfigDir(r.ClaudeCodeConfigPath)
	}
	prefix := r.ClaudeCodeConfigPathPrefix
	if prefix == "" {
		prefix = filepath.Join(ConfigDir(), "claude-config")
	}
	return expandClaudeConfigDir(filepath.Join(prefix, r.GetAgentAccountProfile()))
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

// agentNamePlaceholder is used during the first render pass to detect
// whether the role template references {{ .AgentName }}.
const agentNamePlaceholder = "<AGENT_NAME_PLACEHOLDER>"

// LoadRoleWithNameResolution loads a role using two-pass rendering to resolve
// the agent_name field. This allows agent_name to use template functions like
// {{ randomName }} or {{ autoIncrement "worker" }} whose results are then
// available as {{ .AgentName }} in the rest of the template.
//
// Resolution order:
//  1. If cliName is non-empty, it is used directly (no two-pass needed).
//  2. The role's agent_name field is rendered via a first pass with a
//     placeholder AgentName and the provided nameFuncs. The resolved
//     agent_name is extracted and used as AgentName for the second pass.
//  3. If agent_name is empty after pass 1, generateFallback() is called.
//
// Returns the final Role and the resolved agent name.
func LoadRoleWithNameResolution(
	path string,
	ctx *tmpl.Context,
	nameFuncs template.FuncMap,
	cliName string,
	generateFallback func() string,
) (*Role, string, error) {
	// Fast path: CLI name provided, no two-pass needed.
	if cliName != "" {
		renderCtx := *ctx
		renderCtx.AgentName = cliName
		role, err := loadRoleRenderedFromWithFuncs(path, &renderCtx, nameFuncs)
		if err != nil {
			return nil, "", err
		}
		return role, cliName, nil
	}

	// Read the raw file and prepare template vars (shared across both passes).
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read role file: %w", err)
	}

	defs, remaining, err := tmpl.ParseVarDefs(string(data))
	if err != nil {
		return nil, "", fmt.Errorf("parse variables in role %q: %w", path, err)
	}

	vars := mergeVarDefaults(ctx.Var, defs)
	if err := tmpl.ValidateVars(defs, vars); err != nil {
		return nil, "", fmt.Errorf("role %q: %w", filepath.Base(path), err)
	}

	// --- Pass 1: render with placeholder AgentName to extract agent_name ---
	pass1Ctx := *ctx
	pass1Ctx.AgentName = agentNamePlaceholder
	pass1Ctx.Var = vars

	pass1Rendered, err := tmpl.RenderWithExtraFuncs(remaining, &pass1Ctx, nameFuncs)
	if err != nil {
		return nil, "", fmt.Errorf("template error in role %q (pass 1): %w", filepath.Base(path), err)
	}

	var pass1Role Role
	if err := yaml.Unmarshal([]byte(pass1Rendered), &pass1Role); err != nil {
		return nil, "", fmt.Errorf("parse role YAML %q (pass 1): %w", path, err)
	}

	// Resolve the agent name.
	resolvedName := pass1Role.AgentName
	if strings.Contains(resolvedName, agentNamePlaceholder) {
		return nil, "", fmt.Errorf("role %q: agent_name must not reference {{ .AgentName }} (circular reference)", filepath.Base(path))
	}
	if resolvedName == "" {
		resolvedName = generateFallback()
	}

	// --- Pass 2: re-render with the resolved AgentName ---
	pass2Ctx := *ctx
	pass2Ctx.AgentName = resolvedName
	pass2Ctx.Var = vars

	pass2Rendered, err := tmpl.RenderWithExtraFuncs(remaining, &pass2Ctx, nameFuncs)
	if err != nil {
		return nil, "", fmt.Errorf("template error in role %q (pass 2): %w", filepath.Base(path), err)
	}

	var role Role
	if err := yaml.Unmarshal([]byte(pass2Rendered), &role); err != nil {
		return nil, "", fmt.Errorf("parse role YAML %q (pass 2): %w", path, err)
	}

	role.Variables = defs
	if err := role.Validate(); err != nil {
		return nil, "", fmt.Errorf("invalid role %q: %w", path, err)
	}

	return &role, resolvedName, nil
}

// loadRoleRenderedFromWithFuncs is like LoadRoleRenderedFrom but uses extra template functions.
func loadRoleRenderedFromWithFuncs(path string, ctx *tmpl.Context, extraFuncs template.FuncMap) (*Role, error) {
	if ctx == nil {
		return LoadRoleFrom(path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read role file: %w", err)
	}

	defs, remaining, err := tmpl.ParseVarDefs(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse variables in role %q: %w", path, err)
	}

	vars := mergeVarDefaults(ctx.Var, defs)
	if err := tmpl.ValidateVars(defs, vars); err != nil {
		return nil, fmt.Errorf("role %q: %w", filepath.Base(path), err)
	}

	renderCtx := *ctx
	renderCtx.Var = vars
	rendered, err := tmpl.RenderWithExtraFuncs(remaining, &renderCtx, extraFuncs)
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

// mergeVarDefaults creates a new map with provided vars + defaults for missing ones.
func mergeVarDefaults(provided map[string]string, defs map[string]tmpl.VarDef) map[string]string {
	vars := make(map[string]string, len(provided)+len(defs))
	for k, v := range provided {
		vars[k] = v
	}
	for name, def := range defs {
		if _, ok := vars[name]; !ok && def.Default != nil {
			vars[name] = *def.Default
		}
	}
	return vars
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
	if r.RoleName == "" {
		return fmt.Errorf("role_name is required")
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
	if r.CodexSandboxMode != "" {
		valid := false
		for _, m := range ValidCodexSandboxModes {
			if r.CodexSandboxMode == m {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("invalid codex_sandbox_mode %q; valid values: %s",
				r.CodexSandboxMode, strings.Join(ValidCodexSandboxModes, ", "))
		}
	}
	if r.CodexAskForApproval != "" {
		valid := false
		for _, v := range ValidCodexAskForApproval {
			if r.CodexAskForApproval == v {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("invalid codex_ask_for_approval %q; valid values: %s",
				r.CodexAskForApproval, strings.Join(ValidCodexAskForApproval, ", "))
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
