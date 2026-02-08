package cmd

import (
	"fmt"
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
