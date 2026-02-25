package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"h2/internal/config"
	s "h2/internal/termstyle"
	"h2/internal/tmpl"
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
					printRoleList(globalRoles, config.RolesDir())
				}
				fmt.Printf("%s\n", s.Bold("Pod roles"))
				printRoleList(podRoles, config.PodRolesDir())
			} else {
				// No pod roles — flat output (backward compatible).
				printRoleList(globalRoles, config.RolesDir())
			}
			return nil
		},
	}
}

func printRoleList(roles []*config.Role, rolesDir string) {
	for _, r := range roles {
		desc := r.Description
		if desc == "" {
			desc = "(no description)"
		}
		varInfo := ""
		if n := len(r.Variables); n > 0 {
			if n == 1 {
				varInfo = " (1 variable)"
			} else {
				varInfo = fmt.Sprintf(" (%d variables)", n)
			}
		}
		inheritInfo := ""
		if parent := directParentFromRoleFile(rolesDir, r.RoleName); parent != "" {
			inheritInfo = fmt.Sprintf(" (inherits: %s)", parent)
		}
		fmt.Printf("  %-16s %s%s%s\n", r.RoleName, desc, varInfo, inheritInfo)
	}
}

func newRoleShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Display a role's configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			role, _, err := config.LoadRoleForDisplay(args[0])
			if err != nil {
				return err
			}
			meta, err := config.GetRoleInheritanceMetadata(args[0])
			if err != nil {
				return err
			}

			fmt.Printf("Role:        %s\n", role.RoleName)
			if meta.DirectParent != "" {
				fmt.Printf("Inherits:    %s\n", meta.DirectParent)
			}
			if len(meta.Chain) > 1 {
				fmt.Printf("Chain:       %s\n", strings.Join(meta.Chain, " -> "))
			}
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

			if len(role.AdditionalDirs) > 0 {
				fmt.Printf("Additional Dirs: %s\n", strings.Join(role.AdditionalDirs, ", "))
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

			if len(role.Variables) > 0 {
				printVariables(role.Variables, meta.ExposedVarOrigins)
			}
			if len(meta.HiddenVarOrigins) > 0 {
				printHiddenInheritedVariables(meta.HiddenVarOrigins)
			}

			return nil
		},
	}
}

// printVariables displays variable definitions in a formatted section.
func printVariables(defs map[string]tmpl.VarDef, origins map[string]string) {
	// Sort variable names for deterministic output.
	names := make([]string, 0, len(defs))
	for name := range defs {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Printf("\nVariables:\n")
	for _, name := range names {
		def := defs[name]
		desc := def.Description
		if desc != "" {
			desc = fmt.Sprintf("%q", desc)
		}
		var defVal string
		if def.Default != nil {
			defVal = fmt.Sprintf("(default: %q)", *def.Default)
		} else {
			defVal = "(required)"
		}
		origin := ""
		if from := origins[name]; from != "" {
			origin = fmt.Sprintf(" [from: %s]", from)
		}
		if desc != "" {
			fmt.Printf("  %-16s %s %s%s\n", name, desc, defVal, origin)
		} else {
			fmt.Printf("  %-16s %s%s\n", name, defVal, origin)
		}
	}
}

func printHiddenInheritedVariables(origins map[string]string) {
	names := make([]string, 0, len(origins))
	for name := range origins {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Printf("\nInherited Defaults (not settable via --var on this role):\n")
	fmt.Printf("  (Pinned from parent role templates; child must redefine under variables: to expose.)\n")
	for _, name := range names {
		fmt.Printf("  %-16s [from: %s]\n", name, origins[name])
	}
}

func directParentFromRoleFile(rolesDir, roleName string) string {
	path, ok := resolveRolePathForDir(rolesDir, roleName)
	if !ok {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	parent, _, err := tmpl.ParseInherits(string(data))
	if err != nil {
		return ""
	}
	return parent
}

func resolveRolePathForDir(dir, roleName string) (string, bool) {
	for _, ext := range []string{".yaml.tmpl", ".yaml"} {
		path := filepath.Join(dir, roleName+ext)
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}
	return "", false
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
			meta, err := config.GetRoleInheritanceMetadata(args[0])
			if err != nil {
				return fmt.Errorf("role %q inheritance validation failed: %w", args[0], err)
			}

			role, _, err := config.LoadRoleForDisplay(args[0])
			if err != nil {
				return err
			}

			fmt.Printf("Role %q is valid.\n", role.RoleName)
			if meta.DirectParent != "" {
				fmt.Printf("  Inherits:    %s\n", meta.DirectParent)
			}
			if len(meta.Chain) > 1 {
				fmt.Printf("  Chain:       %s\n", strings.Join(meta.Chain, " -> "))
			}

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
