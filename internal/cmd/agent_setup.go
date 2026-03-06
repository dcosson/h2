package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"h2/internal/config"
	"h2/internal/git"
	"h2/internal/session"
	"h2/internal/session/agent/harness"
)

// forkDaemonFunc is the function used to fork daemon processes.
// Tests override this to avoid spawning real processes.
var forkDaemonFunc func(string, session.TerminalHints, bool) error = session.ForkDaemon

// roleHarnessConfig builds a harness.HarnessConfig from a Role.
// Used by agent setup and dry-run to resolve the harness.
func roleHarnessConfig(role *config.Role) harness.HarnessConfig {
	ht := harness.CanonicalName(role.GetHarnessType())
	command := role.GetAgentType()
	if command == "" {
		command = harness.DefaultCommand(ht)
	}
	cfg := harness.HarnessConfig{
		HarnessType: ht,
		Command:     command,
		Model:       role.GetModel(),
	}
	switch ht {
	case "claude_code":
		cfg.ConfigDir = role.GetClaudeConfigDir()
	case "codex":
		cfg.ConfigDir = role.GetCodexConfigDir()
	}
	return cfg
}

// commandHarnessConfig builds a harness.HarnessConfig from a raw command path.
// Used for non-role launches so daemon startup does not need to re-derive harness.
func commandHarnessConfig(command string) harness.HarnessConfig {
	base := filepath.Base(command)
	ht := "generic"
	configDir := ""
	switch base {
	case "claude":
		ht = "claude_code"
		configDir = config.DefaultClaudeConfigDir()
	case "codex":
		ht = "codex"
	}
	return harness.HarnessConfig{
		HarnessType: ht,
		Command:     command,
		ConfigDir:   configDir,
	}
}

// setupAndForkAgent sets up the agent session, forks the daemon,
// and optionally attaches to it. This is shared by both 'h2 run' and 'h2 bridge'.
// The caller is responsible for loading the role and applying any overrides.
// setupAndForkAgentQuiet is like setupAndForkAgent but suppresses output.
// Used by pod launch which handles its own output.
func setupAndForkAgentQuiet(name string, role *config.Role, pod string, overrides []string) error {
	return doSetupAndForkAgent(name, role, true, pod, overrides, true)
}

func setupAndForkAgent(name string, role *config.Role, detach bool, pod string, overrides []string) error {
	return doSetupAndForkAgent(name, role, detach, pod, overrides, false)
}

func doSetupAndForkAgent(name string, role *config.Role, detach bool, pod string, overrides []string, quiet bool) error {
	if name == "" {
		name = session.GenerateName()
	}
	if err := ensureAgentSocketAvailable(name); err != nil {
		return err
	}

	sessionDir, err := config.SetupSessionDir(name, role)
	if err != nil {
		return fmt.Errorf("setup session dir: %w", err)
	}

	// Resolve harness and ensure config directories exist.
	roleCfg := roleHarnessConfig(role)
	h, err := harness.Resolve(roleCfg, nil)
	if err != nil {
		return fmt.Errorf("resolve harness: %w", err)
	}
	if err := validateHarnessConfigDirExists(role, roleCfg); err != nil {
		return err
	}
	if err := h.EnsureConfigDir(config.ConfigDir()); err != nil {
		return fmt.Errorf("ensure config dir: %w", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	// Resolve the working directory for the agent.
	var agentCWD string
	worktreeCfg, err := role.BuildWorktreeConfig(cwd, name)
	if err != nil {
		return fmt.Errorf("build worktree config: %w", err)
	}
	if worktreeCfg != nil {
		// Worktree mode: create/reuse worktree, CWD = worktree path.
		worktreePath, err := git.CreateWorktree(worktreeCfg)
		if err != nil {
			return fmt.Errorf("create worktree: %w", err)
		}
		agentCWD = worktreePath
	} else {
		// Normal mode: resolve working_dir.
		agentCWD, err = role.ResolveWorkingDir(cwd)
		if err != nil {
			return fmt.Errorf("resolve working_dir: %w", err)
		}
	}

	// Resolve additional dirs.
	additionalDirs, err := role.ResolveAdditionalDirs(cwd)
	if err != nil {
		return fmt.Errorf("resolve additional_dirs: %w", err)
	}

	// Parse overrides into a map for RuntimeConfig.
	var overrideMap map[string]string
	if len(overrides) > 0 {
		overrideMap, err = config.ParseOverrides(overrides)
		if err != nil {
			return fmt.Errorf("parse overrides: %w", err)
		}
	}

	sessionID := uuid.New().String()

	// Build RuntimeConfig with all resolved values.
	// For Claude Code, h2 passes --session-id so HarnessSessionID equals SessionID.
	// For other harnesses (Codex), the harness reports its own session ID async
	// via OTEL and the daemon writes it to HarnessSessionID when received.
	harnessSessionID := ""
	if roleCfg.HarnessType == "claude_code" {
		harnessSessionID = sessionID
	}
	// Resolve harness config path prefix from role.
	var harnessConfigPathPrefix string
	switch roleCfg.HarnessType {
	case "claude_code":
		harnessConfigPathPrefix = role.GetClaudeConfigPathPrefix()
	case "codex":
		harnessConfigPathPrefix = role.GetCodexConfigPathPrefix()
	}

	rc := &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               sessionID,
		HarnessSessionID:        harnessSessionID,
		RoleName:                role.RoleName,
		Pod:                     pod,
		HarnessType:             roleCfg.HarnessType,
		HarnessConfigPathPrefix: harnessConfigPathPrefix,
		Profile:                 role.GetProfile(),
		Command:                 h.Command(),
		// Args is not set for role-based launches; the harness builds
		// the full command args via BuildCommandArgs.
		Model:                roleCfg.Model,
		CWD:                  agentCWD,
		Instructions:         role.GetInstructions(),
		SystemPrompt:         role.SystemPrompt,
		ClaudePermissionMode: role.ClaudePermissionMode,
		CodexSandboxMode:     role.CodexSandboxMode,
		CodexAskForApproval:  role.CodexAskForApproval,
		AdditionalDirs:       additionalDirs,
		Overrides:            overrideMap,
	}

	// Set heartbeat config.
	if role.Heartbeat != nil {
		if role.Heartbeat.IdleTimeout != "" {
			rc.HeartbeatIdleTimeout = role.Heartbeat.IdleTimeout
		}
		rc.HeartbeatMessage = role.Heartbeat.Message
		rc.HeartbeatCondition = role.Heartbeat.Condition
	}

	// Write RuntimeConfig before forking so the daemon can read it.
	if err := config.WriteRuntimeConfig(sessionDir, rc); err != nil {
		return fmt.Errorf("write runtime config: %w", err)
	}

	colorHints := detectTerminalHints()

	// Fork the daemon.
	if err := forkDaemonFunc(sessionDir, session.TerminalHints{
		OscFg:     colorHints.OscFg,
		OscBg:     colorHints.OscBg,
		ColorFGBG: colorHints.ColorFGBG,
		Term:      colorHints.Term,
		ColorTerm: colorHints.ColorTerm,
	}, false); err != nil {
		return err
	}

	if detach {
		if !quiet {
			fmt.Fprintf(os.Stderr, "Agent %q started (detached). Use 'h2 attach %s' to connect.\n", name, name)
		}
		return nil
	}

	if !quiet {
		fmt.Fprintf(os.Stderr, "Agent %q started. Attaching...\n", name)
	}
	return doAttach(name)
}

func validateHarnessConfigDirExists(role *config.Role, hcfg harness.HarnessConfig) error {
	if hcfg.ConfigDir == "" {
		return nil
	}
	info, err := os.Stat(hcfg.ConfigDir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("harness config path is not a directory: %s", hcfg.ConfigDir)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("stat harness config dir %s: %w", hcfg.ConfigDir, err)
	}

	// Config dir is always derived from prefix + profile now.
	profile := role.GetProfile()
	return fmt.Errorf("profile %q not found (missing %s); h2 does not auto-create profiles on run, use 'h2 profile create %s' or choose an existing profile via 'h2 profile list'",
		profile, hcfg.ConfigDir, profile)
}
