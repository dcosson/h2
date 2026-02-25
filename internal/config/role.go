package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	Enabled                 *bool  `yaml:"enabled,omitempty"` // defaults to true if instructions are set
	Instructions            string `yaml:"instructions,omitempty"`
	InstructionsIntro       string `yaml:"instructions_intro,omitempty"`
	InstructionsBody        string `yaml:"instructions_body,omitempty"`
	InstructionsAdditional1 string `yaml:"instructions_additional_1,omitempty"`
	InstructionsAdditional2 string `yaml:"instructions_additional_2,omitempty"`
	InstructionsAdditional3 string `yaml:"instructions_additional_3,omitempty"`
}

// IsEnabled returns whether the permission review agent is enabled.
// Defaults to true when any instructions are present.
func (pa *PermissionReviewAgent) IsEnabled() bool {
	if pa.Enabled != nil {
		return *pa.Enabled
	}
	return pa.GetInstructions() != ""
}

// GetInstructions returns the assembled instructions string.
// If any of the split fields (instructions_intro, instructions_body, etc.) are set,
// they are concatenated with newlines. Otherwise falls back to the single instructions field.
func (pa *PermissionReviewAgent) GetInstructions() string {
	return assembleInstructions(
		pa.Instructions,
		pa.InstructionsIntro,
		pa.InstructionsBody,
		pa.InstructionsAdditional1,
		pa.InstructionsAdditional2,
		pa.InstructionsAdditional3,
	)
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

	WorkingDir              string                 `yaml:"working_dir,omitempty"`               // agent CWD (default ".")
	AdditionalDirs          []string               `yaml:"additional_dirs,omitempty"`           // extra dirs passed via --add-dir
	Worktree                *WorktreeConfig        `yaml:"worktree,omitempty"`                  // git worktree settings
	SystemPrompt            string                 `yaml:"system_prompt,omitempty"`             // replaces Claude's entire default system prompt (--system-prompt)
	Instructions            string                 `yaml:"instructions,omitempty"`              // appended to default system prompt (--append-system-prompt)
	InstructionsIntro       string                 `yaml:"instructions_intro,omitempty"`        // split instructions: intro
	InstructionsBody        string                 `yaml:"instructions_body,omitempty"`         // split instructions: body
	InstructionsAdditional1 string                 `yaml:"instructions_additional_1,omitempty"` // split instructions: additional 1
	InstructionsAdditional2 string                 `yaml:"instructions_additional_2,omitempty"` // split instructions: additional 2
	InstructionsAdditional3 string                 `yaml:"instructions_additional_3,omitempty"` // split instructions: additional 3
	PermissionMode          string                 `yaml:"permission_mode,omitempty"`           // Claude Code --permission-mode flag
	CodexSandboxMode        string                 `yaml:"codex_sandbox_mode,omitempty"`        // Codex --sandbox flag
	CodexAskForApproval     string                 `yaml:"codex_ask_for_approval,omitempty"`    // Codex --ask-for-approval flag
	PermissionReviewAgent   *PermissionReviewAgent `yaml:"permission_review_agent,omitempty"`   // AI permission reviewer
	Heartbeat               *HeartbeatConfig       `yaml:"heartbeat,omitempty"`
	Hooks                   yaml.Node              `yaml:"hooks,omitempty"`     // passed through as-is to settings.json
	Settings                yaml.Node              `yaml:"settings,omitempty"`  // extra settings.json keys
	Variables               map[string]tmpl.VarDef `yaml:"variables,omitempty"` // template variable definitions
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

// ResolveAdditionalDirs returns absolute paths for additional directories.
// Relative paths are resolved against the h2 dir. Absolute paths are used as-is.
func (r *Role) ResolveAdditionalDirs(invocationCWD string) ([]string, error) {
	if len(r.AdditionalDirs) == 0 {
		return nil, nil
	}
	h2Dir, err := ResolveDir()
	if err != nil {
		return nil, fmt.Errorf("resolve h2 dir for additional_dirs: %w", err)
	}
	resolved := make([]string, 0, len(r.AdditionalDirs))
	for _, dir := range r.AdditionalDirs {
		if dir == "" || dir == "." {
			resolved = append(resolved, invocationCWD)
		} else if filepath.IsAbs(dir) {
			resolved = append(resolved, dir)
		} else {
			resolved = append(resolved, filepath.Join(h2Dir, dir))
		}
	}
	return resolved, nil
}

// GetInstructions returns the assembled instructions string.
// If any of the split fields (instructions_intro, instructions_body, etc.) are set,
// they are concatenated with newlines. Otherwise falls back to the single instructions field.
func (r *Role) GetInstructions() string {
	return assembleInstructions(
		r.Instructions,
		r.InstructionsIntro,
		r.InstructionsBody,
		r.InstructionsAdditional1,
		r.InstructionsAdditional2,
		r.InstructionsAdditional3,
	)
}

// hasSplitInstructions returns true if any of the split instruction parts are set.
func hasSplitInstructions(intro, body, add1, add2, add3 string) bool {
	for _, p := range []string{intro, body, add1, add2, add3} {
		if strings.TrimSpace(p) != "" {
			return true
		}
	}
	return false
}

// assembleInstructions concatenates split instruction parts with newlines.
// If any split parts are set, they are used. Otherwise falls back to the single field.
func assembleInstructions(single, intro, body, add1, add2, add3 string) string {
	parts := []string{intro, body, add1, add2, add3}
	var nonEmpty []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			nonEmpty = append(nonEmpty, strings.TrimRight(p, "\n"))
		}
	}
	if len(nonEmpty) > 0 {
		return strings.Join(nonEmpty, "\n")
	}
	return single
}

// validateInstructionsMutualExclusivity checks that single instructions and split instruction
// fields are not both set. Returns an error with the given context label if both are set.
func validateInstructionsMutualExclusivity(label, single, intro, body, add1, add2, add3 string) error {
	if strings.TrimSpace(single) != "" && hasSplitInstructions(intro, body, add1, add2, add3) {
		return fmt.Errorf("%s: instructions and split instruction fields (instructions_intro, instructions_body, etc.) are mutually exclusive", label)
	}
	return nil
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

// resolveRolePath finds the role file for the given name, trying .yaml.tmpl first, then .yaml.
// Returns the path and whether it's a template file.
func resolveRolePath(dir, name string) (string, bool) {
	tmplPath := filepath.Join(dir, name+".yaml.tmpl")
	if _, err := os.Stat(tmplPath); err == nil {
		return tmplPath, true
	}
	return filepath.Join(dir, name+".yaml"), false
}

// ResolveRolePath returns the path to a role file by name, checking .yaml.tmpl first then .yaml.
func ResolveRolePath(name string) string {
	path, _ := resolveRolePath(RolesDir(), name)
	return path
}

// LoadRole loads a role by name from ~/.h2/roles/<name>.yaml or <name>.yaml.tmpl.
func LoadRole(name string) (*Role, error) {
	path, _ := resolveRolePath(RolesDir(), name)
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
// Tries <name>.yaml.tmpl first, then <name>.yaml.
// If ctx is nil, behaves like LoadRole (no rendering — backward compat).
func LoadRoleRendered(name string, ctx *tmpl.Context) (*Role, error) {
	path, _ := resolveRolePath(RolesDir(), name)
	return LoadRoleRenderedFrom(path, ctx)
}

// LoadRoleRenderedFrom loads a role from the given file path, rendering it with
// the given template context. If ctx is nil, behaves like LoadRoleFrom.
func LoadRoleRenderedFrom(path string, ctx *tmpl.Context) (*Role, error) {
	if ctx == nil {
		return LoadRoleFrom(path)
	}
	return loadRoleRenderedFromWithFuncs(path, ctx, nil)
}

// agentNamePlaceholder is used during the first render pass to detect
// whether the role template references {{ .AgentName }}.
const agentNamePlaceholder = "<AGENT_NAME_PLACEHOLDER>"

const maxRoleInheritanceDepth = 10

type inheritanceLevel struct {
	name      string
	path      string
	defs      map[string]tmpl.VarDef
	remaining string
}

type inheritanceRenderPlan struct {
	chain       []inheritanceLevel
	renderDefs  map[string]tmpl.VarDef
	exposedDefs map[string]tmpl.VarDef
}

type mergedRoleRender struct {
	data            map[string]interface{}
	hooks           *yaml.Node
	settings        *yaml.Node
	hooksPresent    bool
	settingsPresent bool
}

// RoleInheritanceMetadata describes a role's inheritance chain and variable origins.
type RoleInheritanceMetadata struct {
	DirectParent      string
	Chain             []string
	ExposedVarOrigins map[string]string
	HiddenVarOrigins  map[string]string
}

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
	if ctx == nil {
		ctx = &tmpl.Context{}
	}

	plan, err := buildInheritanceRenderPlan(path)
	if err != nil {
		return nil, "", err
	}

	vars := mergeVarDefaults(ctx.Var, plan.renderDefs)
	if err := tmpl.ValidateVars(plan.renderDefs, vars); err != nil {
		return nil, "", fmt.Errorf("role %q: %w", filepath.Base(path), err)
	}
	if err := tmpl.ValidateNoUnknownVars(plan.exposedDefs, ctx.Var); err != nil {
		return nil, "", fmt.Errorf("role %q: %w", filepath.Base(path), err)
	}

	renderCtx := *ctx
	renderCtx.Var = vars

	// Fast path: CLI name provided, no two-pass needed.
	if cliName != "" {
		renderCtx.AgentName = cliName
		role, err := renderRoleFromPlan(plan, &renderCtx, nameFuncs, filepath.Base(path))
		if err != nil {
			return nil, "", err
		}
		return role, cliName, nil
	}

	// Pass 1 to resolve agent_name from merged inherited output.
	pass1Ctx := renderCtx
	pass1Ctx.AgentName = agentNamePlaceholder
	pass1Merged, err := renderMergedRoleMap(plan.chain, &pass1Ctx, nameFuncs, filepath.Base(path), "pass 1")
	if err != nil {
		return nil, "", err
	}

	resolvedName, err := extractResolvedAgentName(pass1Merged.data, filepath.Base(path))
	if err != nil {
		return nil, "", err
	}
	if resolvedName == "" {
		resolvedName = generateFallback()
	}

	// Pass 2 with resolved agent name.
	pass2Ctx := renderCtx
	pass2Ctx.AgentName = resolvedName
	role, err := renderRoleFromPlan(plan, &pass2Ctx, nameFuncs, filepath.Base(path))
	if err != nil {
		return nil, "", err
	}

	return role, resolvedName, nil
}

// loadRoleRenderedFromWithFuncs is like LoadRoleRenderedFrom but uses extra template functions.
func loadRoleRenderedFromWithFuncs(path string, ctx *tmpl.Context, extraFuncs template.FuncMap) (*Role, error) {
	if ctx == nil {
		return LoadRoleFrom(path)
	}

	plan, err := buildInheritanceRenderPlan(path)
	if err != nil {
		return nil, err
	}

	vars := mergeVarDefaults(ctx.Var, plan.renderDefs)
	if err := tmpl.ValidateVars(plan.renderDefs, vars); err != nil {
		return nil, fmt.Errorf("role %q: %w", filepath.Base(path), err)
	}

	renderCtx := *ctx
	renderCtx.Var = vars
	return renderRoleFromPlan(plan, &renderCtx, extraFuncs, filepath.Base(path))
}

// loadRoleRenderedForDisplay renders a role for display commands.
// Unlike launch-time rendering, it intentionally does not enforce required-var
// presence so role metadata can be inspected without caller-provided --var values.
func loadRoleRenderedForDisplay(path string, ctx *tmpl.Context, extraFuncs template.FuncMap) (*Role, error) {
	if ctx == nil {
		return LoadRoleFrom(path)
	}

	plan, err := buildInheritanceRenderPlan(path)
	if err != nil {
		return nil, err
	}

	renderCtx := *ctx
	renderCtx.Var = mergeVarDefaults(ctx.Var, plan.renderDefs)
	return renderRoleFromPlan(plan, &renderCtx, extraFuncs, filepath.Base(path))
}

func buildInheritanceRenderPlan(path string) (*inheritanceRenderPlan, error) {
	chain, err := resolveInheritanceChain(path, map[string]bool{}, 1)
	if err != nil {
		return nil, err
	}

	renderDefs := map[string]tmpl.VarDef{}
	for i, level := range chain {
		if i > 0 {
			if err := tmpl.ValidateChildCoversRequired(chain[i-1].defs, level.defs); err != nil {
				return nil, fmt.Errorf("role %q inheritance from %q invalid: %w", level.name, chain[i-1].name, err)
			}
		}
		renderDefs = tmpl.MergeVarDefs(renderDefs, level.defs)
	}

	exposedDefs := copyVarDefs(renderDefs)
	if len(chain) > 1 {
		childDefs := chain[len(chain)-1].defs
		exposedDefs = copyVarDefs(childDefs)

		parentDefs := map[string]tmpl.VarDef{}
		for i := 0; i < len(chain)-1; i++ {
			parentDefs = tmpl.MergeVarDefs(parentDefs, chain[i].defs)
		}
		for name, def := range parentDefs {
			if !def.Required() {
				continue
			}
			if childDef, ok := childDefs[name]; ok {
				exposedDefs[name] = childDef
			}
		}
	}

	return &inheritanceRenderPlan{
		chain:       chain,
		renderDefs:  renderDefs,
		exposedDefs: exposedDefs,
	}, nil
}

func resolveInheritanceChain(path string, seen map[string]bool, depth int) ([]inheritanceLevel, error) {
	if depth > maxRoleInheritanceDepth {
		return nil, fmt.Errorf("role inheritance depth exceeds maximum of %d", maxRoleInheritanceDepth)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read role file: %w", err)
	}

	name := roleNameFromFile(filepath.Base(path))
	if seen[name] {
		return nil, fmt.Errorf("circular role inheritance detected at %q", name)
	}
	seen[name] = true

	inherits, withoutInherits, err := tmpl.ParseInherits(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse inherits in role %q: %w", path, err)
	}

	defs, remaining, err := tmpl.ParseVarDefs(withoutInherits)
	if err != nil {
		return nil, fmt.Errorf("parse variables in role %q: %w", path, err)
	}

	current := inheritanceLevel{
		name:      name,
		path:      path,
		defs:      defs,
		remaining: remaining,
	}

	if inherits == "" {
		return []inheritanceLevel{current}, nil
	}

	parentPath, _ := resolveRolePath(RolesDir(), inherits)
	if _, err := os.Stat(parentPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("role %q inherits unknown parent role %q", name, inherits)
		}
		return nil, fmt.Errorf("resolve parent role %q: %w", inherits, err)
	}

	parentChain, err := resolveInheritanceChain(parentPath, seen, depth+1)
	if err != nil {
		return nil, err
	}
	return append(parentChain, current), nil
}

func renderRoleFromPlan(plan *inheritanceRenderPlan, ctx *tmpl.Context, extraFuncs template.FuncMap, roleLabel string) (*Role, error) {
	merged, err := renderMergedRoleMap(plan.chain, ctx, extraFuncs, roleLabel, "")
	if err != nil {
		return nil, err
	}

	var role Role
	roleYAML, err := yaml.Marshal(merged.data)
	if err != nil {
		return nil, fmt.Errorf("marshal merged role YAML %q: %w", roleLabel, err)
	}
	if err := yaml.Unmarshal(roleYAML, &role); err != nil {
		return nil, fmt.Errorf("parse merged role YAML %q: %w", roleLabel, err)
	}
	if merged.hooksPresent && merged.hooks != nil {
		role.Hooks = *cloneYAMLNode(merged.hooks)
	}
	if merged.settingsPresent && merged.settings != nil {
		role.Settings = *cloneYAMLNode(merged.settings)
	}

	role.Variables = copyVarDefs(plan.exposedDefs)
	if err := role.Validate(); err != nil {
		return nil, fmt.Errorf("invalid role %q: %w", roleLabel, err)
	}

	return &role, nil
}

func renderMergedRoleMap(chain []inheritanceLevel, ctx *tmpl.Context, extraFuncs template.FuncMap, roleLabel, passLabel string) (*mergedRoleRender, error) {
	merged := map[string]interface{}{}
	var mergedHooks *yaml.Node
	var mergedSettings *yaml.Node
	hooksPresent := false
	settingsPresent := false

	for _, level := range chain {
		rendered, err := tmpl.RenderWithExtraFuncs(level.remaining, ctx, extraFuncs)
		if err != nil {
			if passLabel != "" {
				return nil, fmt.Errorf("template error in role %q (%s, %s): %w", roleLabel, level.path, passLabel, err)
			}
			return nil, fmt.Errorf("template error in role %q (%s): %w", roleLabel, level.path, err)
		}

		var roleMap map[string]interface{}
		if err := yaml.Unmarshal([]byte(rendered), &roleMap); err != nil {
			if passLabel != "" {
				return nil, fmt.Errorf("parse rendered role YAML %q (%s, %s): %w", roleLabel, level.path, passLabel, err)
			}
			return nil, fmt.Errorf("parse rendered role YAML %q (%s): %w", roleLabel, level.path, err)
		}

		var renderedNode yaml.Node
		if err := yaml.Unmarshal([]byte(rendered), &renderedNode); err != nil {
			if passLabel != "" {
				return nil, fmt.Errorf("parse rendered YAML node %q (%s, %s): %w", roleLabel, level.path, passLabel, err)
			}
			return nil, fmt.Errorf("parse rendered YAML node %q (%s): %w", roleLabel, level.path, err)
		}
		if err := rejectUnsupportedCustomTags(&renderedNode); err != nil {
			if passLabel != "" {
				return nil, fmt.Errorf("role %q (%s, %s): %w", roleLabel, level.path, passLabel, err)
			}
			return nil, fmt.Errorf("role %q (%s): %w", roleLabel, level.path, err)
		}
		if hooksNode, ok := extractTopLevelYAMLNode(&renderedNode, "hooks"); ok {
			hooksPresent = true
			if mergedHooks == nil {
				mergedHooks = cloneYAMLNode(hooksNode)
			} else {
				mergedHooks = mergeYAMLNodes(mergedHooks, hooksNode)
			}
		}
		if settingsNode, ok := extractTopLevelYAMLNode(&renderedNode, "settings"); ok {
			settingsPresent = true
			if mergedSettings == nil {
				mergedSettings = cloneYAMLNode(settingsNode)
			} else {
				mergedSettings = mergeYAMLNodes(mergedSettings, settingsNode)
			}
		}

		delete(roleMap, "inherits")
		delete(roleMap, "variables")
		delete(roleMap, "hooks")
		delete(roleMap, "settings")
		merged = deepMergeMaps(merged, roleMap)
	}

	if hooksPresent {
		merged["hooks"] = yamlNodeToInterface(mergedHooks)
	}
	if settingsPresent {
		merged["settings"] = yamlNodeToInterface(mergedSettings)
	}

	return &mergedRoleRender{
		data:            merged,
		hooks:           mergedHooks,
		settings:        mergedSettings,
		hooksPresent:    hooksPresent,
		settingsPresent: settingsPresent,
	}, nil
}

func extractResolvedAgentName(rendered map[string]interface{}, roleLabel string) (string, error) {
	rawName, ok := rendered["agent_name"]
	if !ok || rawName == nil {
		return "", nil
	}
	name, ok := rawName.(string)
	if !ok {
		return "", fmt.Errorf("role %q: agent_name must render to a string", roleLabel)
	}
	if strings.Contains(name, agentNamePlaceholder) {
		return "", fmt.Errorf("role %q: agent_name must not reference {{ .AgentName }} (circular reference)", roleLabel)
	}
	return name, nil
}

func deepMergeMaps(base, overlay map[string]interface{}) map[string]interface{} {
	merged := make(map[string]interface{}, len(base)+len(overlay))
	for k, v := range base {
		merged[k] = v
	}

	for key, overlayValue := range overlay {
		baseValue, exists := merged[key]
		if exists {
			baseMap, baseOK := baseValue.(map[string]interface{})
			overlayMap, overlayOK := overlayValue.(map[string]interface{})
			if baseOK && overlayOK {
				merged[key] = deepMergeMaps(baseMap, overlayMap)
				continue
			}
		}
		merged[key] = overlayValue
	}

	return merged
}

func extractTopLevelYAMLNode(doc *yaml.Node, key string) (*yaml.Node, bool) {
	if doc == nil || len(doc.Content) == 0 {
		return nil, false
	}
	root := doc.Content[0]
	if root == nil || root.Kind != yaml.MappingNode {
		return nil, false
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k := root.Content[i]
		v := root.Content[i+1]
		if k.Value == key {
			return v, true
		}
	}
	return nil, false
}

func mergeYAMLNodes(base, overlay *yaml.Node) *yaml.Node {
	if overlay == nil {
		return cloneYAMLNode(base)
	}
	if base == nil {
		return cloneYAMLNode(overlay)
	}

	if base.Kind == yaml.MappingNode && overlay.Kind == yaml.MappingNode {
		result := cloneYAMLNode(base)
		indexByKey := map[string]int{}
		for i := 0; i+1 < len(result.Content); i += 2 {
			indexByKey[result.Content[i].Value] = i + 1
		}
		for i := 0; i+1 < len(overlay.Content); i += 2 {
			k := overlay.Content[i]
			v := overlay.Content[i+1]
			if existingIdx, ok := indexByKey[k.Value]; ok {
				result.Content[existingIdx] = mergeYAMLNodes(result.Content[existingIdx], v)
				continue
			}
			result.Content = append(result.Content, cloneYAMLNode(k), cloneYAMLNode(v))
		}
		return result
	}

	// Scalar, sequence, null, and type/shape replacement: overlay wins.
	return cloneYAMLNode(overlay)
}

func cloneYAMLNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	cloned := *node
	if len(node.Content) > 0 {
		cloned.Content = make([]*yaml.Node, len(node.Content))
		for i, child := range node.Content {
			cloned.Content[i] = cloneYAMLNode(child)
		}
	}
	return &cloned
}

func yamlNodeToInterface(node *yaml.Node) interface{} {
	if node == nil {
		return nil
	}
	var out interface{}
	if err := node.Decode(&out); err != nil {
		return nil
	}
	return out
}

func rejectUnsupportedCustomTags(doc *yaml.Node) error {
	var paths []string
	collectUnsupportedCustomTags(doc, "", false, &paths)
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	return fmt.Errorf("custom YAML tags outside hooks/settings are not supported in role inheritance merge; unsupported tags at: %s", strings.Join(paths, ", "))
}

func collectUnsupportedCustomTags(node *yaml.Node, path string, allowCustom bool, paths *[]string) {
	if node == nil {
		return
	}
	if strings.HasPrefix(node.Tag, "!") && !strings.HasPrefix(node.Tag, "!!") && !allowCustom {
		*paths = append(*paths, pathWithDefault(path))
	}

	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			collectUnsupportedCustomTags(child, path, allowCustom, paths)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			k := node.Content[i]
			v := node.Content[i+1]
			childPath := appendPath(path, k.Value)
			childAllow := allowCustom || path == "" && (k.Value == "hooks" || k.Value == "settings")
			collectUnsupportedCustomTags(v, childPath, childAllow, paths)
		}
	case yaml.SequenceNode:
		for idx, child := range node.Content {
			collectUnsupportedCustomTags(child, fmt.Sprintf("%s[%d]", pathWithDefault(path), idx), allowCustom, paths)
		}
	case yaml.AliasNode:
		collectUnsupportedCustomTags(node.Alias, path, allowCustom, paths)
	}
}

func appendPath(path, next string) string {
	if path == "" {
		return next
	}
	return path + "." + next
}

func pathWithDefault(path string) string {
	if path == "" {
		return "<root>"
	}
	return path
}

func copyVarDefs(defs map[string]tmpl.VarDef) map[string]tmpl.VarDef {
	if len(defs) == 0 {
		return map[string]tmpl.VarDef{}
	}
	names := make([]string, 0, len(defs))
	for name := range defs {
		names = append(names, name)
	}
	sort.Strings(names)

	copied := make(map[string]tmpl.VarDef, len(defs))
	for _, name := range names {
		copied[name] = defs[name]
	}
	return copied
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

// roleFileExtensions lists recognized role file extensions in priority order.
var roleFileExtensions = []string{".yaml.tmpl", ".yaml"}

// isRoleFile checks if a filename has a recognized role file extension.
func isRoleFile(name string) bool {
	for _, ext := range roleFileExtensions {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

// roleNameFromFile extracts the role name from a filename by removing the extension.
func roleNameFromFile(name string) string {
	for _, ext := range roleFileExtensions {
		if strings.HasSuffix(name, ext) {
			return strings.TrimSuffix(name, ext)
		}
	}
	return name
}

// listStubFuncs provides stub template functions for listing roles.
// These are needed because templates may reference functions like randomName
// or autoIncrement that are only fully functional during agent launch.
var listStubFuncs = template.FuncMap{
	"randomName":    func() string { return "<name>" },
	"autoIncrement": func(prefix string) int { return 0 },
}

// listRolesFromDir scans a directory for role files (.yaml and .yaml.tmpl) and loads them.
func listRolesFromDir(dir string) ([]*Role, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read roles dir: %w", err)
	}

	seen := make(map[string]bool) // track role names to avoid duplicates
	var roles []*Role
	for _, entry := range entries {
		if entry.IsDir() || !isRoleFile(entry.Name()) {
			continue
		}
		roleName := roleNameFromFile(entry.Name())
		if seen[roleName] {
			continue // .yaml.tmpl was already loaded (processed first alphabetically)
		}
		seen[roleName] = true

		path := filepath.Join(dir, entry.Name())
		// Try rendered load with stub name functions (handles template files).
		rootDir, _ := RootDir()
		ctx := &tmpl.Context{
			RoleName:  roleName,
			AgentName: "<name>",
			H2Dir:     ConfigDir(),
			H2RootDir: rootDir,
		}
		role, err := loadRoleRenderedFromWithFuncs(path, ctx, listStubFuncs)
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

// ListRoles returns all available roles from ~/.h2/roles/.
func ListRoles() ([]*Role, error) {
	return listRolesFromDir(RolesDir())
}

// LoadRoleForDisplay loads a role for display purposes (e.g., `h2 role show`).
// It renders templates with stub values so that template files can be parsed
// and displayed. The returned role has Variables populated from the template's
// variable definitions. Returns the role and a map of variable definitions.
func LoadRoleForDisplay(name string) (*Role, map[string]tmpl.VarDef, error) {
	path, _ := resolveRolePath(RolesDir(), name)
	return loadRoleForDisplay(path, name)
}

// loadRoleForDisplay loads a role for display from a specific path.
func loadRoleForDisplay(path, roleName string) (*Role, map[string]tmpl.VarDef, error) {
	// Read the raw file to extract variable definitions.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read role file: %w", err)
	}

	defs, _, err := tmpl.ParseVarDefs(string(data))
	if err != nil {
		return nil, nil, fmt.Errorf("parse variables in role %q: %w", path, err)
	}

	// Try rendered load with stub context (handles template files).
	rootDir, _ := RootDir()
	ctx := &tmpl.Context{
		RoleName:  roleName,
		AgentName: "<name>",
		H2Dir:     ConfigDir(),
		H2RootDir: rootDir,
	}
	role, err := loadRoleRenderedForDisplay(path, ctx, listStubFuncs)
	if err != nil {
		// Fallback to plain load (handles roles with required vars).
		var fallbackErr error
		role, fallbackErr = LoadRoleFrom(path)
		if fallbackErr != nil {
			return nil, nil, fmt.Errorf("%w (display render failed: %v)", fallbackErr, err)
		}
		err = nil
	}

	// Ensure Variables is populated from defs (may not be if fallback was used).
	if role.Variables == nil && len(defs) > 0 {
		role.Variables = defs
	}

	return role, defs, nil
}

// GetRoleInheritanceMetadata returns inheritance metadata for a global role.
// It validates the full inheritance chain (including cycle/depth/parent resolution)
// and reports variable origins for exposed and hidden inherited variables.
func GetRoleInheritanceMetadata(name string) (*RoleInheritanceMetadata, error) {
	path, _ := resolveRolePath(RolesDir(), name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read role file: %w", err)
	}
	parent, _, err := tmpl.ParseInherits(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse inherits in role %q: %w", path, err)
	}

	chain, err := resolveInheritanceChain(path, map[string]bool{}, 1)
	if err != nil {
		return nil, err
	}

	meta := &RoleInheritanceMetadata{
		DirectParent:      parent,
		Chain:             make([]string, 0, len(chain)),
		ExposedVarOrigins: map[string]string{},
		HiddenVarOrigins:  map[string]string{},
	}
	for _, level := range chain {
		meta.Chain = append(meta.Chain, level.name)
	}

	renderDefs := map[string]tmpl.VarDef{}
	lastOrigin := map[string]string{}
	for _, level := range chain {
		renderDefs = tmpl.MergeVarDefs(renderDefs, level.defs)
		for name := range level.defs {
			lastOrigin[name] = level.name
		}
	}

	exposedDefs := copyVarDefs(renderDefs)
	if len(chain) > 1 {
		childDefs := chain[len(chain)-1].defs
		exposedDefs = copyVarDefs(childDefs)

		parentDefs := map[string]tmpl.VarDef{}
		for i := 0; i < len(chain)-1; i++ {
			parentDefs = tmpl.MergeVarDefs(parentDefs, chain[i].defs)
		}
		for name, def := range parentDefs {
			if !def.Required() {
				continue
			}
			if childDef, ok := childDefs[name]; ok {
				exposedDefs[name] = childDef
			}
		}
	}

	for name := range exposedDefs {
		meta.ExposedVarOrigins[name] = lastOrigin[name]
	}
	for name := range renderDefs {
		if _, ok := exposedDefs[name]; ok {
			continue
		}
		meta.HiddenVarOrigins[name] = lastOrigin[name]
	}

	return meta, nil
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
	// instructions and split instruction fields are mutually exclusive.
	if err := validateInstructionsMutualExclusivity("role",
		r.Instructions, r.InstructionsIntro, r.InstructionsBody,
		r.InstructionsAdditional1, r.InstructionsAdditional2, r.InstructionsAdditional3,
	); err != nil {
		return err
	}
	if r.PermissionReviewAgent != nil {
		if err := validateInstructionsMutualExclusivity("permission_review_agent",
			r.PermissionReviewAgent.Instructions,
			r.PermissionReviewAgent.InstructionsIntro, r.PermissionReviewAgent.InstructionsBody,
			r.PermissionReviewAgent.InstructionsAdditional1, r.PermissionReviewAgent.InstructionsAdditional2, r.PermissionReviewAgent.InstructionsAdditional3,
		); err != nil {
			return err
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
