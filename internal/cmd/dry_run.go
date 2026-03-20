package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"h2/internal/config"
	"h2/internal/session/agent/harness"
)

const dryRunAgentNamePlaceholder = "<agent-name>"

// ResolvedAgentConfig holds all resolved values for an agent launch,
// computed without any side effects. Used by --dry-run display.
type ResolvedAgentConfig struct {
	Name       string
	Role       *config.Role
	Command    string
	Model      string
	SessionDir string
	WorkingDir string
	IsWorktree bool
	Pod        string
	Overrides  []string
	EnvVars    map[string]string
	ChildArgs  []string
	RoleScope  string            // "pod" or "global" — set by pod dry-run
	MergedVars map[string]string // final merged vars — set by pod dry-run
}

// resolveAgentConfig computes all values needed to launch an agent without
// performing any side effects (no dir creation, no worktree creation, no forking).
func resolveAgentConfig(name string, role *config.Role, pod string, overrides []string, extraArgs []string) (*ResolvedAgentConfig, error) {
	if name == "" {
		name = dryRunAgentNamePlaceholder
	}

	// Build a minimal RuntimeConfig for harness resolution.
	minRC := buildRoleRuntimeConfig(role)
	h, err := harness.Resolve(minRC, nil)
	if err != nil {
		return nil, fmt.Errorf("resolve harness: %w", err)
	}

	// Resolve the working directory without side effects.
	var agentCWD string
	var isWorktree bool
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}
	worktreeCfg, err := role.BuildWorktreeConfig(cwd, name)
	if err != nil {
		return nil, fmt.Errorf("build worktree config: %w", err)
	}
	if worktreeCfg != nil {
		isWorktree = true
		agentCWD = worktreeCfg.GetPath()
	} else {
		agentCWD, err = role.ResolveWorkingDir(cwd)
		if err != nil {
			return nil, fmt.Errorf("resolve working_dir: %w", err)
		}
	}

	sessionDir := config.SessionDir(name)

	// Build the env vars that would be set.
	envVars := make(map[string]string)
	if h2Dir, err := config.ResolveDir(); err == nil {
		envVars["H2_DIR"] = h2Dir
	}
	envVars["H2_ACTOR"] = name
	if role.RoleName != "" {
		envVars["H2_ROLE"] = role.RoleName
	}
	if sessionDir != "" {
		envVars["H2_SESSION_DIR"] = sessionDir
	}
	// Merge harness-specific env vars (e.g. CLAUDE_CONFIG_DIR).
	for k, v := range h.BuildCommandEnvVars(config.ConfigDir()) {
		envVars[k] = v
	}
	if pod != "" {
		envVars["H2_POD"] = pod
	}

	// Capture launch config from PrepareForLaunch in dry-run mode (no side effects).
	// This includes harness-provided prepend args and launch-time env vars.
	var prependArgs []string
	if launchCfg, err := h.PrepareForLaunch(true); err == nil {
		prependArgs = launchCfg.PrependArgs
		for k, v := range launchCfg.Env {
			envVars[k] = v
		}
	}

	// Resolve additional dirs.
	additionalDirs, err := role.ResolveAdditionalDirs(agentCWD)
	if err != nil {
		return nil, fmt.Errorf("resolve additional_dirs: %w", err)
	}

	// Build a full RuntimeConfig for dry-run arg generation.
	// We need a RuntimeConfig with all fields so the harness can pull from it.
	dryRunRC := &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               "<generated-uuid>",
		HarnessType:             minRC.HarnessType,
		HarnessConfigPathPrefix: minRC.HarnessConfigPathPrefix,
		Profile:                 role.GetProfile(),
		Command:                 h.Command(),
		Args:                    extraArgs,
		Model:                   minRC.Model,
		CWD:                     agentCWD,
		Instructions:            role.GetInstructions(),
		SystemPrompt:            role.SystemPrompt,
		ClaudePermissionMode:    role.ClaudePermissionMode,
		CodexSandboxMode:        role.CodexSandboxMode,
		CodexAskForApproval:     role.CodexAskForApproval,
		AdditionalDirs:          additionalDirs,
		StartedAt:               "dry-run",
	}

	// Re-resolve harness with full config so BuildCommandArgs has access to all fields.
	dryRunH, err := harness.Resolve(dryRunRC, nil)
	if err != nil {
		return nil, fmt.Errorf("resolve harness for args: %w", err)
	}
	childArgs := dryRunH.BuildCommandArgs(prependArgs, extraArgs)

	return &ResolvedAgentConfig{
		Name:       name,
		Role:       role,
		Command:    h.Command(),
		Model:      minRC.Model,
		SessionDir: sessionDir,
		WorkingDir: agentCWD,
		IsWorktree: isWorktree,
		Pod:        pod,
		Overrides:  overrides,
		EnvVars:    envVars,
		ChildArgs:  childArgs,
	}, nil
}

// printDryRun displays the resolved agent configuration without launching.
func printDryRun(rc *ResolvedAgentConfig) {
	role := rc.Role

	fmt.Printf("Agent: %s\n", rc.Name)
	fmt.Printf("Role: %s\n", role.RoleName)
	if role.Description != "" {
		fmt.Printf("Description: %s\n", role.Description)
	}
	if rc.Model != "" {
		fmt.Printf("Model: %s\n", rc.Model)
	}
	if role.ClaudePermissionMode != "" {
		fmt.Printf("Permission Mode: %s\n", role.ClaudePermissionMode)
	}

	// System prompt (truncated with line count).
	if role.SystemPrompt != "" {
		lines := strings.Split(role.SystemPrompt, "\n")
		fmt.Printf("\nSystem Prompt: (%d lines)\n", len(lines))
		const maxLines = 10
		for i, line := range lines {
			if i >= maxLines {
				fmt.Printf("  ... (%d more lines)\n", len(lines)-maxLines)
				break
			}
			fmt.Printf("  %s\n", line)
		}
	}

	fmt.Println()

	// Instructions (truncated with line count).
	if instr := role.GetInstructions(); instr != "" {
		lines := strings.Split(instr, "\n")
		fmt.Printf("Instructions: (%d lines)\n", len(lines))
		const maxLines = 10
		for i, line := range lines {
			if i >= maxLines {
				fmt.Printf("  ... (%d more lines)\n", len(lines)-maxLines)
				break
			}
			fmt.Printf("  %s\n", line)
		}
	}

	fmt.Println()
	// Print command + args in a copy-pasteable format with \ continuations.
	fmt.Println("Command:")
	if len(rc.ChildArgs) == 0 {
		fmt.Printf("%s\n", rc.Command)
	} else {
		// Group flags with their values.
		var parts []string
		for i := 0; i < len(rc.ChildArgs); i++ {
			arg := rc.ChildArgs[i]
			if strings.HasPrefix(arg, "-") && i+1 < len(rc.ChildArgs) && !strings.HasPrefix(rc.ChildArgs[i+1], "-") {
				// Flag with a value: combine into one part.
				i++
				val := rc.ChildArgs[i]
				// Shell-quote the value if it contains spaces or special chars.
				if strings.ContainsAny(val, " \t\"'\\$`") {
					val = "'" + strings.ReplaceAll(val, "'", "'\\''") + "'"
				}
				parts = append(parts, arg+" "+val)
			} else {
				parts = append(parts, arg)
			}
		}
		fmt.Printf("%s \\\n", rc.Command)
		for i, part := range parts {
			if i < len(parts)-1 {
				fmt.Printf("  %s \\\n", part)
			} else {
				fmt.Printf("  %s\n", part)
			}
		}
	}

	fmt.Println()
	if rc.IsWorktree {
		fmt.Printf("Working Dir: %s (worktree)\n", rc.WorkingDir)
	} else {
		fmt.Printf("Working Dir: %s\n", rc.WorkingDir)
	}
	fmt.Printf("Session Dir: %s\n", rc.SessionDir)

	// Environment variables.
	fmt.Println()
	fmt.Println("Environment:")
	var envKeys []string
	for k := range rc.EnvVars {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, key := range envKeys {
		fmt.Printf("  %s=%s\n", key, rc.EnvVars[key])
	}

	// Permission review.
	if role.PermissionReview != nil {
		pr := role.PermissionReview
		if pr.DCG != nil && pr.DCG.IsEnabled() {
			fmt.Println()
			fmt.Printf("Permission Review (DCG): enabled\n")
			if pr.DCG.DestructivePolicy != "" {
				fmt.Printf("  Destructive Policy: %s\n", pr.DCG.DestructivePolicy)
			}
			if pr.DCG.PrivacyPolicy != "" {
				fmt.Printf("  Privacy Policy: %s\n", pr.DCG.PrivacyPolicy)
			}
		}
		if pr.AIReviewer != nil && pr.AIReviewer.IsEnabled() {
			fmt.Println()
			fmt.Printf("Permission Review (AI Reviewer): enabled (model: %s)\n", pr.AIReviewer.GetModel())
		}
	}

	// Triggers.
	if len(rc.Role.Triggers) > 0 {
		fmt.Println()
		fmt.Printf("Triggers: %d\n", len(rc.Role.Triggers))
		for _, t := range rc.Role.Triggers {
			fmt.Printf("  - %s (event=%s)\n", t.Name, t.Event)
		}
	}

	// Schedules (includes heartbeat if converted).
	if role.Heartbeat != nil || len(rc.Role.Schedules) > 0 {
		fmt.Println()
		total := len(rc.Role.Schedules)
		if role.Heartbeat != nil {
			total++
		}
		fmt.Printf("Schedules: %d\n", total)
		if role.Heartbeat != nil {
			fmt.Printf("  - heartbeat (rrule=FREQ=SECONDLY;INTERVAL=%s)\n",
				heartbeatIntervalFromDuration(role.Heartbeat.IdleTimeout))
		}
		for _, s := range rc.Role.Schedules {
			fmt.Printf("  - %s (rrule=%s)\n", s.Name, s.RRule)
		}
	}

	// Overrides.
	if len(rc.Overrides) > 0 {
		fmt.Println()
		fmt.Printf("Overrides: %s\n", strings.Join(rc.Overrides, ", "))
	}

	// Merged vars (pod dry-run only).
	if len(rc.MergedVars) > 0 {
		fmt.Println()
		fmt.Println("Variables:")
		var varKeys []string
		for k := range rc.MergedVars {
			varKeys = append(varKeys, k)
		}
		sort.Strings(varKeys)
		for _, k := range varKeys {
			fmt.Printf("  %s=%s\n", k, rc.MergedVars[k])
		}
	}

	// Role scope (pod dry-run only).
	if rc.RoleScope != "" {
		fmt.Printf("Role Scope: %s\n", rc.RoleScope)
	}
}

// printPodDryRun displays the full pod expansion without launching.
func printPodDryRun(templateName string, pod string, agents []*ResolvedAgentConfig) {
	fmt.Printf("Pod: %s\n", pod)
	fmt.Printf("Template: %s\n", templateName)
	fmt.Printf("Agents: %d\n", len(agents))

	// Collect roles used.
	roleSet := make(map[string]bool)
	for _, rc := range agents {
		roleSet[rc.Role.RoleName] = true
	}
	var roles []string
	for r := range roleSet {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	fmt.Printf("Roles: %s\n", strings.Join(roles, ", "))

	// Print each agent.
	for i, rc := range agents {
		fmt.Println()
		fmt.Printf("--- Agent %d/%d ---\n", i+1, len(agents))
		printDryRun(rc)
	}
}
