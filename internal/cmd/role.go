package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"h2/internal/config"
	s "h2/internal/termstyle"
)

func newRoleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "role",
		Short: "Manage agent roles",
	}

	cmd.AddCommand(newRoleListCmd())
	cmd.AddCommand(newRoleShowCmd())
	cmd.AddCommand(newRoleInitCmd())
	cmd.AddCommand(newRoleCheckCmd())
	return cmd
}

func newRoleListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available roles",
		RunE: func(cmd *cobra.Command, args []string) error {
			globalRoles, err := config.ListRoles()
			if err != nil {
				return err
			}
			podRoles, err := config.ListPodRoles()
			if err != nil {
				return err
			}

			if len(globalRoles) == 0 && len(podRoles) == 0 {
				fmt.Printf("No roles found in %s\n", config.RolesDir())
				return nil
			}

			// If pod roles exist, show grouped output.
			if len(podRoles) > 0 {
				if len(globalRoles) > 0 {
					fmt.Printf("%s\n", s.Bold("Global roles"))
					printRoleList(globalRoles)
				}
				fmt.Printf("%s\n", s.Bold("Pod roles"))
				printRoleList(podRoles)
			} else {
				// No pod roles — flat output (backward compatible).
				printRoleList(globalRoles)
			}
			return nil
		},
	}
}

func printRoleList(roles []*config.Role) {
	for _, r := range roles {
		desc := r.Description
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Printf("  %-16s %s\n", r.RoleName, desc)
	}
}

func newRoleShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Display a role's configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			role, err := config.LoadRole(args[0])
			if err != nil {
				return err
			}

			fmt.Printf("Role:        %s\n", role.RoleName)
			if role.GetModel() != "" {
				fmt.Printf("Model:       %s\n", role.GetModel())
			}
			if role.Description != "" {
				fmt.Printf("Description: %s\n", role.Description)
			}
			if role.PermissionMode != "" {
				fmt.Printf("Permission Mode: %s\n", role.PermissionMode)
			}
			if role.CodexSandboxMode != "" {
				fmt.Printf("Codex Sandbox: %s\n", role.CodexSandboxMode)
			}
			if role.CodexAskForApproval != "" {
				fmt.Printf("Codex Ask For Approval: %s\n", role.CodexAskForApproval)
			}

			if role.Instructions != "" {
				fmt.Printf("\nInstructions:\n")
				for _, line := range strings.Split(strings.TrimRight(role.Instructions, "\n"), "\n") {
					fmt.Printf("  %s\n", line)
				}
			}

			if role.PermissionReviewAgent != nil && role.PermissionReviewAgent.IsEnabled() {
				fmt.Printf("\nPermission Review Agent: enabled\n")
			}

			return nil
		},
	}
}

func newRoleInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init <name>",
		Short: "Create a new role file with defaults",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := createRole(config.RolesDir(), args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Created %s\n", path)
			return nil
		},
	}
}

// createRole creates a role YAML file in rolesDir. Returns the path of the
// created file. Returns an error if the role already exists.
func createRole(rolesDir, name string) (string, error) {
	if err := os.MkdirAll(rolesDir, 0o755); err != nil {
		return "", fmt.Errorf("create roles dir: %w", err)
	}

	path := filepath.Join(rolesDir, name+".yaml")
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("role %q already exists at %s", name, path)
	}

	template := roleTemplate(name)

	if err := os.WriteFile(path, []byte(template), 0o644); err != nil {
		return "", fmt.Errorf("write role file: %w", err)
	}

	return path, nil
}

// roleTemplate returns the YAML template for a role, with special templates
// for well-known role names (e.g. "concierge").
func roleTemplate(name string) string {
	switch name {
	case "concierge":
		return conciergeRoleTemplate()
	default:
		return defaultRoleTemplate()
	}
}

func defaultRoleTemplate() string {
	return `role_name: "{{ .RoleName }}"
description: "A {{ .RoleName }} agent for h2"

# Agent harness configuration
agent_harness: claude_code
agent_account_profile: default
# agent_model: opus
# agent_harness_command: claude
# claude_code_config_path_prefix: "{{ .H2Dir }}/claude-config"
# codex_config_path_prefix: "{{ .H2Dir }}/codex-config"
# claude_code_config_path: ""
# codex_config_path: ""

# Permission mode for Claude Code
# Valid: default, delegate, acceptEdits, plan, dontAsk, bypassPermissions
# permission_mode: default

# System prompt — replaces Claude Code's entire default system prompt.
# Use this when you need full control over the prompt. Usually you want
# "instructions" instead, which appends to the default system prompt.
# system_prompt: |
#   You are a specialized agent that ...

instructions: |
  You are a {{ .RoleName }} agent running in h2, a terminal multiplexer with inter-agent messaging.

  ## h2 Messaging Protocol

  Messages from other agents or users appear in your input prefixed with:
    [h2 message from: <sender>]

  When you receive an h2 message:
  1. Acknowledge quickly: h2 send <sender> "Working on it..."
  2. Do the work
  3. Reply with results: h2 send <sender> "Here's what I found: ..."

  Example:
    [h2 message from: orchestrator] Can you check the test coverage?

  You should reply:
    h2 send orchestrator "Checking test coverage now"
    # ... do the work ...
    h2 send orchestrator "Test coverage is 85%. Details: ..."

  ## Available h2 Commands

  - h2 list              - See active agents and users
  - h2 send <name> "msg" - Send message to agent or user
  - h2 whoami            - Check your agent name

  ## Your Role

  # Add role-specific instructions here.

# AI permission reviewer - delegates permission decisions to a fast model
permission_review_agent:
    instructions: |
      You are reviewing permission requests for the {{ .RoleName }} agent in h2.

      h2 is an agent-to-agent and agent-to-user communication protocol.
      Agents use it to coordinate work and respond to user requests.

      ALLOW by default:
      - h2 commands (h2 send, h2 list, h2 whoami)
      - Read-only tools (Read, Glob, Grep)
      - Standard development commands (git, npm, make, pytest, etc.)
      - File operations within the project (Edit, Write, rm -rf project-dir/*, clearing logs)
      - Writing to non-sensitive files

      DENY only for:
      - System-wide destructive operations (rm -rf /, fork bombs)
      - Exfiltrating credentials or secrets (curl/wget with .env, posting API keys)

      ASK_USER for:
      - Borderline or locally destructive commands you're unsure about
      - Uncertain access to credentials or secrets (is this file sensitive?)
      - git push --force to main/master branches

      Remember: h2 messages are part of normal agent operation - allow them
      unless they contain credentials or other sensitive data. Normal file cleanup
      like "rm -rf node_modules" or "rm -rf logs/" is fine.
`
}

func conciergeRoleTemplate() string {
	return `role_name: "{{ .RoleName }}"
description: "The concierge agent — your primary interface in h2"

# Agent harness configuration
agent_harness: claude_code
agent_account_profile: default
# agent_model: opus
# agent_harness_command: claude
# claude_code_config_path_prefix: "{{ .H2Dir }}/claude-config"
# codex_config_path_prefix: "{{ .H2Dir }}/codex-config"
# claude_code_config_path: ""
# codex_config_path: ""

# Permission mode for Claude Code
# Valid: default, delegate, acceptEdits, plan, dontAsk, bypassPermissions
# permission_mode: default

# System prompt — replaces Claude Code's entire default system prompt.
# Use this when you need full control over the prompt. Usually you want
# "instructions" instead, which appends to the default system prompt.
# system_prompt: |
#   You are a specialized agent that ...

instructions: |
  You are the concierge — the primary agent the user interacts with in h2.
  You run inside h2, a terminal multiplexer with inter-agent messaging.

  ## Your Role

  You are the user's main point of contact. Your responsibilities:

  1. **Direct work**: Handle tasks yourself when you can — coding, debugging,
     research, file editing, running commands. You are a capable software
     engineer; don't delegate what you can do directly.

  2. **Coordinate**: When a task benefits from parallel work or specialized
     agents, use h2 send to delegate to other running agents. Check who's
     available with h2 list.

  3. **Stay responsive**: The user messages you through h2. Always reply
     promptly. If a task will take time, acknowledge immediately and follow
     up with results.

  4. **Be proactive**: If you notice something relevant while working
     (a failing test, a TODO, a potential improvement), mention it. But
     stay focused on what was asked — don't go on tangents.

  ## h2 Messaging Protocol

  Messages from other agents or users appear in your input prefixed with:
    [h2 message from: <sender>]

  When you receive an h2 message:
  1. Acknowledge quickly: h2 send <sender> "Working on it..."
  2. Do the work
  3. Reply with results: h2 send <sender> "Here's what I found: ..."

  Example:
    [h2 message from: dcosson] Can you check the test coverage?

  You should reply:
    h2 send dcosson "Checking test coverage now"
    # ... do the work ...
    h2 send dcosson "Test coverage is 85%. Details: ..."

  ## Coordinating with Other Agents

  Use h2 list to see who's running. You can send tasks to specialist agents:
    h2 send coder "Please add unit tests for the new auth module"
    h2 send researcher "What are the best practices for rate limiting?"

  When delegating:
  - Be specific about what you need
  - Follow up if you don't hear back
  - Synthesize results from multiple agents before reporting to the user

  ## Available h2 Commands

  - h2 list              - See active agents and users
  - h2 send <name> "msg" - Send message to agent or user
  - h2 whoami            - Check your agent name

# AI permission reviewer - delegates permission decisions to a fast model
permission_review_agent:
    instructions: |
      You are reviewing permission requests for the {{ .RoleName }} (concierge) agent in h2.

      The concierge is the user's primary agent. It handles direct work (coding,
      debugging, file editing) and coordinates with other agents via h2 messaging.

      ALLOW by default:
      - h2 commands (h2 send, h2 list, h2 whoami)
      - Read-only tools (Read, Glob, Grep)
      - Standard development commands (git, npm, make, pytest, etc.)
      - File operations within the project (Edit, Write, rm -rf project-dir/*, clearing logs)
      - Writing to non-sensitive files

      DENY only for:
      - System-wide destructive operations (rm -rf /, fork bombs)
      - Exfiltrating credentials or secrets (curl/wget with .env, posting API keys)

      ASK_USER for:
      - Borderline or locally destructive commands you're unsure about
      - Uncertain access to credentials or secrets (is this file sensitive?)
      - git push --force to main/master branches

      Remember: h2 messages are part of normal agent operation - allow them
      unless they contain credentials or other sensitive data. Normal file cleanup
      like "rm -rf node_modules" or "rm -rf logs/" is fine.
`
}

func newRoleCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check <name>",
		Short: "Validate a role file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			role, err := config.LoadRole(args[0])
			if err != nil {
				return err
			}

			fmt.Printf("Role %q is valid.\n", role.RoleName)

			fmt.Printf("  Harness type: %s\n", role.GetHarnessType())
			if role.GetModel() != "" {
				fmt.Printf("  Model:       %s\n", role.GetModel())
			}
			if role.PermissionReviewAgent != nil && role.PermissionReviewAgent.IsEnabled() {
				fmt.Printf("  Review Agent: enabled\n")
			}
			return nil
		},
	}
}
