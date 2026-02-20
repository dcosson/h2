package cmd

import (
	"fmt"
	"os"

	"github.com/google/uuid"

	"h2/internal/config"
	"h2/internal/git"
	"h2/internal/session"
	"h2/internal/session/agent"
)

// forkDaemonFunc is the function used to fork daemon processes.
// Tests override this to avoid spawning real processes.
var forkDaemonFunc = session.ForkDaemon

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

	sessionDir, err := config.SetupSessionDir(name, role)
	if err != nil {
		return fmt.Errorf("setup session dir: %w", err)
	}

	cmdCommand := role.GetAgentType()

	// Ensure agent-type-specific config directories exist.
	agentType := agent.ResolveAgentType(cmdCommand)
	if err := agentType.EnsureConfigDir(config.ConfigDir(), role.Name); err != nil {
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

	// Resolve the working directory for the agent.
	var agentCWD string
	if role.Worktree != nil {
		// Worktree mode: create/reuse worktree, CWD = worktree path.
		worktreePath, err := git.CreateWorktree(role.Worktree)
		if err != nil {
			return fmt.Errorf("create worktree: %w", err)
		}
		agentCWD = worktreePath
	} else {
		// Normal mode: resolve working_dir.
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		agentCWD, err = role.ResolveWorkingDir(cwd)
		if err != nil {
			return fmt.Errorf("resolve working_dir: %w", err)
		}
	}

	sessionID := uuid.New().String()

	// Fork the daemon.
	if err := forkDaemonFunc(session.ForkDaemonOpts{
		Name:            name,
		SessionID:       sessionID,
		Command:         cmdCommand,
		RoleName:        role.Name,
		SessionDir:      sessionDir,
		Instructions:    role.Instructions,
		SystemPrompt:    role.SystemPrompt,
		Model:           role.Model,
		PermissionMode:  role.PermissionMode,
		AllowedTools:    role.Permissions.Allow,
		DisallowedTools: role.Permissions.Deny,
		Heartbeat:       heartbeat,
		CWD:             agentCWD,
		Pod:             pod,
		Overrides:       overrides,
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
