package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"h2/internal/config"
	"h2/internal/gateway"
	"h2/internal/git"
	"h2/internal/session"
	"h2/internal/session/agent/harness"
	"h2/internal/socketdir"
)

// forkDaemonFunc is the function used to fork daemon processes.
// Tests override this to avoid spawning real processes.
var forkDaemonFunc func(string, session.TerminalHints, bool) error = session.ForkDaemon
var gatewayEnsureRunningFunc = gateway.EnsureRunning
var gatewayStartSessionFunc = gateway.StartSession
var gatewayResumeSessionFunc = gateway.ResumeSession

// buildRoleRuntimeConfig builds a minimal RuntimeConfig from a Role, suitable
// for pre-launch harness resolution (command name, config dir validation).
// The full RuntimeConfig is constructed later with additional fields.
func buildRoleRuntimeConfig(role *config.Role) *config.RuntimeConfig {
	ht := harness.CanonicalName(role.GetHarnessType())
	command := role.GetAgentType()
	if command == "" {
		command = harness.DefaultCommand(ht)
	}
	var harnessConfigPathPrefix string
	switch ht {
	case "claude_code":
		harnessConfigPathPrefix = role.GetClaudeConfigPathPrefix()
	case "codex":
		harnessConfigPathPrefix = role.GetCodexConfigPathPrefix()
	}
	return &config.RuntimeConfig{
		HarnessType:             ht,
		Command:                 command,
		Model:                   role.GetModel(),
		HarnessConfigPathPrefix: harnessConfigPathPrefix,
		Profile:                 role.GetProfile(),
	}
}

// buildCommandRuntimeConfig builds a minimal RuntimeConfig from a raw command path.
// Used for non-role launches so daemon startup does not need to re-derive harness.
func buildCommandRuntimeConfig(command string) *config.RuntimeConfig {
	base := filepath.Base(command)
	ht := "generic"
	var configPrefix string
	switch base {
	case "claude":
		ht = "claude_code"
		configPrefix = filepath.Join(config.ConfigDir(), "claude-config")
	case "codex":
		ht = "codex"
		configPrefix = filepath.Join(config.ConfigDir(), "codex-config")
	}
	return &config.RuntimeConfig{
		HarnessType:             ht,
		Command:                 command,
		HarnessConfigPathPrefix: configPrefix,
		Profile:                 "default",
	}
}

// setupAndForkAgent sets up the agent session, forks the daemon,
// and optionally attaches to it. This is shared by both 'h2 run' and 'h2 bridge'.
// The caller is responsible for loading the role and applying any overrides.
// setupAndForkAgentQuiet is like setupAndForkAgent but suppresses output.
// Used by pod launch which handles its own output.
func setupAndForkAgentQuiet(name string, role *config.Role, pod string, podIndex int, overrides []string) error {
	return doSetupAndForkAgent(name, role, true, pod, podIndex, overrides, true)
}

func setupAndForkAgent(name string, role *config.Role, detach bool, pod string, podIndex int, overrides []string) error {
	return doSetupAndForkAgent(name, role, detach, pod, podIndex, overrides, false)
}

func doSetupAndForkAgent(name string, role *config.Role, detach bool, pod string, podIndex int, overrides []string, quiet bool) error {
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

	// Build a minimal RuntimeConfig for pre-launch harness resolution.
	minRC := buildRoleRuntimeConfig(role)

	// Resolve harness and ensure config directories exist.
	h, err := harness.Resolve(minRC, nil)
	if err != nil {
		return fmt.Errorf("resolve harness: %w", err)
	}
	if err := validateHarnessConfigDirExists(role, minRC); err != nil {
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
	if minRC.HarnessType == "claude_code" {
		harnessSessionID = sessionID
	}

	rc := &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               sessionID,
		HarnessSessionID:        harnessSessionID,
		RoleName:                role.RoleName,
		Pod:                     pod,
		PodIndex:                podIndex,
		HarnessType:             minRC.HarnessType,
		HarnessConfigPathPrefix: minRC.HarnessConfigPathPrefix,
		Profile:                 role.GetProfile(),
		Command:                 h.Command(),
		// Args is not set for role-based launches; the harness builds
		// the full command args via BuildCommandArgs.
		Model:                minRC.Model,
		CWD:                  agentCWD,
		Instructions:         role.GetInstructions(),
		SystemPrompt:         role.SystemPrompt,
		ClaudePermissionMode: role.ClaudePermissionMode,
		CodexSandboxMode:     role.CodexSandboxMode,
		CodexAskForApproval:  role.CodexAskForApproval,
		PermissionReview:     role.PermissionReview,
		AdditionalDirs:       additionalDirs,
		Overrides:            overrideMap,
		StartedAt:            time.Now().UTC().Format(time.RFC3339),
	}

	// Convert heartbeat config to a schedule (backwards compatibility).
	if role.Heartbeat != nil && role.Heartbeat.IdleTimeout != "" && role.Heartbeat.Message != "" {
		rc.Schedules = append(rc.Schedules, config.ScheduleYAMLSpec{
			ID:            "heartbeat",
			Name:          "heartbeat",
			RRule:         "FREQ=SECONDLY;INTERVAL=" + heartbeatIntervalFromDuration(role.Heartbeat.IdleTimeout),
			Condition:     role.Heartbeat.Condition,
			ConditionMode: "run_if",
			Message:       role.Heartbeat.Message,
			From:          "h2-heartbeat",
			Priority:      "idle",
		})
	}

	// Copy role-defined triggers and schedules.
	rc.Triggers = append(rc.Triggers, role.Triggers...)
	rc.Schedules = append(rc.Schedules, role.Schedules...)

	// Write RuntimeConfig before forking so the daemon can read it.
	if err := config.WriteRuntimeConfig(sessionDir, rc); err != nil {
		return fmt.Errorf("write runtime config: %w", err)
	}

	colorHints := detectTerminalHints()
	hints := session.TerminalHints{
		OscFg:     colorHints.OscFg,
		OscBg:     colorHints.OscBg,
		ColorFGBG: colorHints.ColorFGBG,
		Term:      colorHints.Term,
		ColorTerm: colorHints.ColorTerm,
	}

	if err := launchAgentSession(sessionDir, rc, hints, false, detach, role.Env); err != nil {
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

func launchAgentSession(sessionDir string, rc *config.RuntimeConfig, hints session.TerminalHints, resume bool, detach bool, roleEnv map[string]string) error {
	if !useGatewayLaunch(detach) {
		return forkDaemonFunc(sessionDir, hints, resume)
	}

	if err := annotateGatewayLaunchEnv(rc, roleEnv); err != nil {
		return err
	}
	if err := config.WriteRuntimeConfig(sessionDir, rc); err != nil {
		return fmt.Errorf("write runtime config: %w", err)
	}

	ctx := context.Background()
	if _, err := gatewayEnsureRunningFunc(ctx, gateway.EnsureOpts{
		H2Dir:      config.ConfigDir(),
		SocketPath: socketdir.GatewayPath(),
	}); err != nil {
		return err
	}

	req := gateway.StartSessionRequest{
		SessionDir:    sessionDir,
		RuntimeConfig: rc,
		Resume:        resume,
		LaunchEnv:     launchPassthroughEnv(),
		RoleEnv:       roleEnv,
	}
	if resume {
		_, err := gatewayResumeSessionFunc(ctx, socketdir.GatewayPath(), req)
		return err
	}
	_, err := gatewayStartSessionFunc(ctx, socketdir.GatewayPath(), req)
	return err
}

func useGatewayLaunch(detach bool) bool {
	return detach && os.Getenv("H2_GATEWAY") == "1"
}

func launchPassthroughEnv() map[string]string {
	return gateway.ExtractEnvPassthrough(os.Environ(), configuredEnvPassthrough())
}

func configuredEnvPassthrough() []string {
	cfg, err := config.Load()
	if err != nil || cfg.Runtime == nil {
		return nil
	}
	return cfg.Runtime.EnvPassthrough
}

func annotateGatewayLaunchEnv(rc *config.RuntimeConfig, roleEnv map[string]string) error {
	passthrough := launchPassthroughEnv()
	rc.PassthroughEnvKeys = gateway.PassthroughEnvKeys(passthrough)

	stableEnv := make(map[string]string)
	if cfg, err := config.Load(); err == nil && cfg.Runtime != nil {
		for key, value := range cfg.Runtime.Env {
			stableEnv[key] = value
		}
	}
	for key, value := range roleEnv {
		stableEnv[key] = value
	}
	rc.ResumeEnvWarning = gateway.ResumeEnvWarning(passthrough, stableEnv)
	return nil
}

// heartbeatIntervalFromDuration converts a Go duration string (e.g. "30s") to
// an RRULE INTERVAL in seconds for the backwards-compatible heartbeat→schedule
// conversion. Falls back to "30" if parsing fails.
func heartbeatIntervalFromDuration(durStr string) string {
	d, err := time.ParseDuration(durStr)
	if err != nil || d <= 0 {
		return "30"
	}
	secs := int(d.Seconds())
	if secs < 1 {
		secs = 1
	}
	return fmt.Sprintf("%d", secs)
}

func validateHarnessConfigDirExists(role *config.Role, rc *config.RuntimeConfig) error {
	configDir := rc.HarnessConfigDir()
	if configDir == "" {
		return nil
	}
	info, err := os.Stat(configDir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("harness config path is not a directory: %s", configDir)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("stat harness config dir %s: %w", configDir, err)
	}

	// Config dir is always derived from prefix + profile now.
	profile := role.GetProfile()
	return fmt.Errorf("profile %q not found (missing %s); h2 does not auto-create profiles on run, use 'h2 profile create %s' or choose an existing profile via 'h2 profile list'",
		profile, configDir, profile)
}
