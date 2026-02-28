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
var forkDaemonFunc = session.ForkDaemon

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
	h, err := harness.Resolve(roleHarnessConfig(role), nil)
	if err != nil {
		return fmt.Errorf("resolve harness: %w", err)
	}
	if err := h.EnsureConfigDir(config.ConfigDir()); err != nil {
		return fmt.Errorf("ensure config dir: %w", err)
	}

	var heartbeat session.DaemonHeartbeat
	if role.Heartbeat != nil {
		d, err := role.Heartbeat.ParseIdleTimeout()
		if err != nil {
			return fmt.Errorf("invalid heartbeat idle_timeout: %w", err)
		}
		heartbeat = session.DaemonHeartbeat{
			IdleTimeout: d,
			Message:     role.Heartbeat.Message,
			Condition:   role.Heartbeat.Condition,
		}
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

	sessionID := uuid.New().String()
	colorHints := detectTerminalHints()

	// Fork the daemon.
	roleCfg := roleHarnessConfig(role)
	if err := forkDaemonFunc(session.ForkDaemonOpts{
		Name:                 name,
		SessionID:            sessionID,
		Command:              h.Command(),
		RoleName:             role.RoleName,
		SessionDir:           sessionDir,
		Instructions:         role.GetInstructions(),
		SystemPrompt:         role.SystemPrompt,
		Model:                roleCfg.Model,
		HarnessType:          roleCfg.HarnessType,
		HarnessConfigDir:     roleCfg.ConfigDir,
		ClaudePermissionMode: role.ClaudePermissionMode,
		CodexSandboxMode:     role.CodexSandboxMode,
		CodexAskForApproval:  role.CodexAskForApproval,
		AdditionalDirs:       additionalDirs,
		Heartbeat:            heartbeat,
		CWD:                  agentCWD,
		Pod:                  pod,
		Overrides:            overrides,
		OscFg:                colorHints.OscFg,
		OscBg:                colorHints.OscBg,
		ColorFGBG:            colorHints.ColorFGBG,
		Term:                 colorHints.Term,
		ColorTerm:            colorHints.ColorTerm,
	}); err != nil {
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
