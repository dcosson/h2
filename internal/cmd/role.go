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

			if instr := role.GetInstructions(); instr != "" {
				fmt.Printf("\nInstructions:\n")
				for _, line := range strings.Split(strings.TrimRight(instr, "\n"), "\n") {
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
// created file. Uses .yaml.tmpl extension when the template contains template
// syntax, otherwise .yaml. Returns an error if the role already exists.
func createRole(rolesDir, name string) (string, error) {
	if err := os.MkdirAll(rolesDir, 0o755); err != nil {
		return "", fmt.Errorf("create roles dir: %w", err)
	}

	content := config.RoleTemplate(name)
	ext := config.RoleFileExtension(content)
	path := filepath.Join(rolesDir, name+ext)

	// Check both extensions to prevent duplicates.
	for _, e := range []string{".yaml", ".yaml.tmpl"} {
		p := filepath.Join(rolesDir, name+e)
		if _, err := os.Stat(p); err == nil {
			return "", fmt.Errorf("role %q already exists at %s", name, p)
		}
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write role file: %w", err)
	}

	return path, nil
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
