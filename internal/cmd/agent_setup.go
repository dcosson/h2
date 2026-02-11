package cmd

import (
	"fmt"
	"os"

	"github.com/google/uuid"

	"h2/internal/config"
	"h2/internal/git"
	"h2/internal/session"
)

// setupAndForkAgent sets up the agent session, forks the daemon,
// and optionally attaches to it. This is shared by both 'h2 run' and 'h2 bridge'.
// The caller is responsible for loading the role and applying any overrides.
func setupAndForkAgent(name string, role *config.Role, detach bool) error {
	if name == "" {
		name = session.GenerateName()
	}

	sessionDir, err := config.SetupSessionDir(name, role)
	if err != nil {
		return fmt.Errorf("setup session dir: %w", err)
	}

	claudeConfigDir := role.GetClaudeConfigDir()
	if claudeConfigDir != "" {
		if err := config.EnsureClaudeConfigDir(claudeConfigDir); err != nil {
			return fmt.Errorf("ensure claude config dir: %w", err)
		}
	}

	cmdCommand := role.GetAgentType()
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
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	agentCWD, err := role.ResolveRootDir(cwd)
	if err != nil {
		return fmt.Errorf("resolve root_dir: %w", err)
	}

	// Create git worktree if enabled.
	if role.Worktree != nil && role.Worktree.Enabled {
		worktreePath, err := git.CreateWorktree(name, agentCWD, role.Worktree)
		if err != nil {
			return fmt.Errorf("create worktree: %w", err)
		}
		agentCWD = worktreePath
	}

	sessionID := uuid.New().String()

	// Fork the daemon.
	if err := session.ForkDaemon(session.ForkDaemonOpts{
		Name:            name,
		SessionID:       sessionID,
		Command:         cmdCommand,
		RoleName:        role.Name,
		SessionDir:      sessionDir,
		ClaudeConfigDir: claudeConfigDir,
		Heartbeat:       heartbeat,
		CWD:             agentCWD,
	}); err != nil {
		return err
	}

	if detach {
		fmt.Fprintf(os.Stderr, "Agent %q started (detached). Use 'h2 attach %s' to connect.\n", name, name)
		return nil
	}

	fmt.Fprintf(os.Stderr, "Agent %q started. Attaching...\n", name)
	return doAttach(name)
}
