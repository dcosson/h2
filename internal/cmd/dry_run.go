package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"h2/internal/config"
	"h2/internal/session"
)

// ResolvedAgentConfig holds all resolved values for an agent launch,
// computed without any side effects. Used by --dry-run display.
type ResolvedAgentConfig struct {
	Name            string
	Role            *config.Role
	Command         string
	SessionDir      string
	ClaudeConfigDir string
	WorkingDir      string
	IsWorktree      bool
	Heartbeat       session.DaemonHeartbeat
	Pod             string
	Overrides       []string
	EnvVars         map[string]string
	ChildArgs       []string
}

// resolveAgentConfig computes all values needed to launch an agent without
// performing any side effects (no dir creation, no worktree creation, no forking).
func resolveAgentConfig(name string, role *config.Role, pod string, overrides []string) (*ResolvedAgentConfig, error) {
	if name == "" {
		name = session.GenerateName()
	}

	claudeConfigDir := role.GetClaudeConfigDir()
	cmdCommand := role.GetAgentType()

	var heartbeat session.DaemonHeartbeat
	if role.Heartbeat != nil {
		d, err := role.Heartbeat.ParseIdleTimeout()
		if err != nil {
			return nil, fmt.Errorf("invalid heartbeat idle_timeout: %w", err)
		}
		heartbeat = session.DaemonHeartbeat{
			IdleTimeout: d,
			Message:     role.Heartbeat.Message,
			Condition:   role.Heartbeat.Condition,
		}
	}

	// Resolve the working directory without side effects.
	var agentCWD string
	var isWorktree bool
	if role.Worktree != nil {
		isWorktree = true
		agentCWD = filepath.Join(config.WorktreesDir(), role.Worktree.Name)
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
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
	if role.Name != "" {
		envVars["H2_ROLE"] = role.Name
	}
	if sessionDir != "" {
		envVars["H2_SESSION_DIR"] = sessionDir
	}
	if claudeConfigDir != "" {
		envVars["CLAUDE_CONFIG_DIR"] = claudeConfigDir
	}
	if pod != "" {
		envVars["H2_POD"] = pod
	}

	// Build child args: what the claude command would receive.
	var childArgs []string
	childArgs = append(childArgs, "--session-id", "<generated-uuid>")
	if role.Instructions != "" {
		childArgs = append(childArgs, "--append-system-prompt", role.Instructions)
	}

	return &ResolvedAgentConfig{
		Name:            name,
		Role:            role,
		Command:         cmdCommand,
		SessionDir:      sessionDir,
		ClaudeConfigDir: claudeConfigDir,
		WorkingDir:      agentCWD,
		IsWorktree:      isWorktree,
		Heartbeat:       heartbeat,
		Pod:             pod,
		Overrides:       overrides,
		EnvVars:         envVars,
		ChildArgs:       childArgs,
	}, nil
}

// printDryRun displays the resolved agent configuration without launching.
func printDryRun(rc *ResolvedAgentConfig) {
	role := rc.Role

	fmt.Printf("Agent: %s\n", rc.Name)
	fmt.Printf("Role: %s\n", role.Name)
	if role.Description != "" {
		fmt.Printf("Description: %s\n", role.Description)
	}
	if role.Model != "" {
		fmt.Printf("Model: %s\n", role.Model)
	}

	fmt.Println()

	// Instructions (truncated with line count).
	if role.Instructions != "" {
		lines := strings.Split(role.Instructions, "\n")
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
	fmt.Printf("Command: %s\n", rc.Command)
	if len(rc.ChildArgs) > 0 {
		// Show args with instructions value truncated for readability.
		var displayArgs []string
		for i := 0; i < len(rc.ChildArgs); i++ {
			if rc.ChildArgs[i] == "--append-system-prompt" && i+1 < len(rc.ChildArgs) {
				displayArgs = append(displayArgs, rc.ChildArgs[i])
				lines := strings.Count(rc.ChildArgs[i+1], "\n") + 1
				displayArgs = append(displayArgs, fmt.Sprintf("<instructions: %d lines>", lines))
				i++ // skip the value
			} else {
				displayArgs = append(displayArgs, rc.ChildArgs[i])
			}
		}
		fmt.Printf("Args: %s\n", strings.Join(displayArgs, " "))
	}

	fmt.Println()
	if rc.IsWorktree {
		fmt.Printf("Working Dir: %s (worktree)\n", rc.WorkingDir)
	} else {
		fmt.Printf("Working Dir: %s\n", rc.WorkingDir)
	}
	if rc.ClaudeConfigDir != "" {
		fmt.Printf("Claude Config Dir: %s\n", rc.ClaudeConfigDir)
	}
	fmt.Printf("Session Dir: %s\n", rc.SessionDir)

	// Environment variables.
	fmt.Println()
	fmt.Println("Environment:")
	envOrder := []string{"H2_DIR", "H2_ACTOR", "H2_ROLE", "H2_POD", "H2_SESSION_DIR", "CLAUDE_CONFIG_DIR"}
	for _, key := range envOrder {
		if val, ok := rc.EnvVars[key]; ok {
			fmt.Printf("  %s=%s\n", key, val)
		}
	}

	// Permissions.
	perms := role.Permissions
	if len(perms.Allow) > 0 || len(perms.Deny) > 0 || perms.Agent != nil {
		fmt.Println()
		fmt.Println("Permissions:")
		if len(perms.Allow) > 0 {
			fmt.Printf("  Allow: %s\n", strings.Join(perms.Allow, ", "))
		}
		if len(perms.Deny) > 0 {
			fmt.Printf("  Deny: %s\n", strings.Join(perms.Deny, ", "))
		}
		if perms.Agent != nil {
			fmt.Printf("  Agent Reviewer: %v\n", perms.Agent.IsEnabled())
		}
	}

	// Heartbeat.
	if rc.Heartbeat.IdleTimeout > 0 {
		fmt.Println()
		fmt.Println("Heartbeat:")
		fmt.Printf("  Idle Timeout: %s\n", rc.Heartbeat.IdleTimeout)
		if rc.Heartbeat.Message != "" {
			fmt.Printf("  Message: %s\n", rc.Heartbeat.Message)
		}
		if rc.Heartbeat.Condition != "" {
			fmt.Printf("  Condition: %s\n", rc.Heartbeat.Condition)
		}
	}

	// Overrides.
	if len(rc.Overrides) > 0 {
		fmt.Println()
		fmt.Printf("Overrides: %s\n", strings.Join(rc.Overrides, ", "))
	}
}
