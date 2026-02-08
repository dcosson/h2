package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"h2/internal/config"
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
			roles, err := config.ListRoles()
			if err != nil {
				return err
			}
			if len(roles) == 0 {
				fmt.Printf("No roles found in %s\n", config.RolesDir())
				return nil
			}
			for _, r := range roles {
				desc := r.Description
				if desc == "" {
					desc = "(no description)"
				}
				fmt.Printf("%-16s %s\n", r.Name, desc)
			}
			return nil
		},
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

			fmt.Printf("Name:        %s\n", role.Name)
			if role.Model != "" {
				fmt.Printf("Model:       %s\n", role.Model)
			}
			if role.Description != "" {
				fmt.Printf("Description: %s\n", role.Description)
			}

			fmt.Printf("\nInstructions:\n")
			for _, line := range strings.Split(strings.TrimRight(role.Instructions, "\n"), "\n") {
				fmt.Printf("  %s\n", line)
			}

			if len(role.Permissions.Allow) > 0 || len(role.Permissions.Deny) > 0 {
				fmt.Printf("\nPermissions:\n")
				if len(role.Permissions.Allow) > 0 {
					fmt.Printf("  Allow: %s\n", strings.Join(role.Permissions.Allow, ", "))
				}
				if len(role.Permissions.Deny) > 0 {
					fmt.Printf("  Deny:  %s\n", strings.Join(role.Permissions.Deny, ", "))
				}
				if role.Permissions.Agent != nil && role.Permissions.Agent.IsEnabled() {
					fmt.Printf("  Agent: enabled\n")
				}
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
			name := args[0]
			dir := config.RolesDir()

			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create roles dir: %w", err)
			}

			path := filepath.Join(dir, name+".yaml")
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("role %q already exists at %s", name, path)
			}

			template := fmt.Sprintf(`name: %s
description: ""

# Model selection (opus, sonnet, haiku)
# model: sonnet

instructions: |
  You are a %s agent.
  # Add role-specific instructions here.

permissions:
  allow:
    - "Read"
    - "Glob"
    - "Grep"
  # deny:
  #   - "Bash(rm -rf *)"

  # AI permission reviewer (optional)
  # agent:
  #   instructions: |
  #     You are reviewing permissions for a %s agent.
  #     ALLOW: read-only tools, standard dev commands
  #     DENY: destructive operations
  #     ASK_USER: anything uncertain
`, name, name, name)

			if err := os.WriteFile(path, []byte(template), 0o644); err != nil {
				return fmt.Errorf("write role file: %w", err)
			}

			fmt.Printf("Created %s\n", path)
			return nil
		},
	}
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

			fmt.Printf("Role %q is valid.\n", role.Name)

			if role.Model != "" {
				fmt.Printf("  Model:       %s\n", role.Model)
			}
			fmt.Printf("  Allow rules: %d\n", len(role.Permissions.Allow))
			fmt.Printf("  Deny rules:  %d\n", len(role.Permissions.Deny))
			if role.Permissions.Agent != nil && role.Permissions.Agent.IsEnabled() {
				fmt.Printf("  Agent:       enabled\n")
			}
			return nil
		},
	}
}
