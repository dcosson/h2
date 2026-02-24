package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"h2/internal/config"
)

const defaultConfigYAML = `# h2 configuration
# See https://github.com/dcosson/h2 for documentation.
#
# users:
#   yourname:
#     bridges:
#       telegram:
#         bot_token: "123456:ABC-DEF"
#         chat_id: 789
#       macos_notify:
#         enabled: true
`

// expectedRootDirFiles are files that live at H2_ROOT_DIR and may already
// exist when initializing a directory. If the target dir contains only
// these (plus the marker), we allow init to proceed.
var expectedRootDirFiles = map[string]bool{
	".h2-dir.txt":         true,
	"routes.jsonl":        true,
	"terminal-colors.json": true,
	"config.yaml":         true,
}

func newInitCmd() *cobra.Command {
	var global bool
	var prefix string
	var generate string
	var force bool

	cmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Initialize an h2 directory",
		Long: `Create an h2 directory with the standard structure.

Use --global or omit dir to initialize ~/.h2/.

Use --generate to regenerate specific config files in an existing h2 directory:
  h2 init <path> --generate roles         # regenerate roles/default.yaml
  h2 init <path> --generate instructions  # regenerate CLAUDE.md + AGENTS.md symlink
  h2 init <path> --generate config        # regenerate config.yaml
  h2 init <path> --generate all           # regenerate all generated files

Use --force with --generate to overwrite existing files.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var dir string
			switch {
			case global || len(args) == 0:
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("cannot determine home directory: %w", err)
				}
				dir = filepath.Join(home, ".h2")
			default:
				dir = args[0]
			}

			abs, err := filepath.Abs(dir)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()

			if generate != "" {
				return runGenerate(abs, generate, force, out)
			}

			return runFullInit(cmd, abs, prefix, out)
		},
	}

	cmd.Flags().BoolVar(&global, "global", false, "Initialize ~/.h2/ as the h2 directory")
	cmd.Flags().StringVar(&prefix, "prefix", "", "Custom prefix for this h2 directory in the routes registry")
	cmd.Flags().StringVar(&generate, "generate", "", "Regenerate specific config: roles, instructions, config, all")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing files when using --generate")
	return cmd
}

// runFullInit performs a full h2 directory initialization.
func runFullInit(cmd *cobra.Command, abs, prefix string, out io.Writer) error {
	if config.IsH2Dir(abs) {
		return fmt.Errorf("%s is already an h2 directory", abs)
	}

	// Safety check: if the directory already exists and has content,
	// only allow init if it contains only expected root-dir files.
	if err := checkDirSafeForInit(abs); err != nil {
		return err
	}

	fmt.Fprintf(out, "Creating h2 directory at %s...\n", abs)

	subdirs := []string{
		"roles",
		"sessions",
		"sockets",
		filepath.Join("claude-config", "default"),
		filepath.Join("codex-config", "default"),
		"projects",
		"worktrees",
		filepath.Join("pods", "roles"),
		filepath.Join("pods", "templates"),
	}
	for _, sub := range subdirs {
		d := filepath.Join(abs, sub)
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", d, err)
		}
		fmt.Fprintf(out, "  Created %s/\n", sub)
	}

	if err := config.WriteMarker(abs); err != nil {
		return fmt.Errorf("write marker: %w", err)
	}

	// Write config.yaml.
	configPath := filepath.Join(abs, "config.yaml")
	if err := os.WriteFile(configPath, []byte(defaultConfigYAML), 0o644); err != nil {
		return fmt.Errorf("write config.yaml: %w", err)
	}
	fmt.Fprintf(out, "  Wrote config.yaml\n")

	// Write CLAUDE.md and symlink AGENTS.md.
	if err := writeInstructions(abs); err != nil {
		return fmt.Errorf("write instructions: %w", err)
	}
	fmt.Fprintf(out, "  Wrote claude-config/default/CLAUDE.md\n")
	fmt.Fprintf(out, "  Symlinked codex-config/default/AGENTS.md -> ../../claude-config/default/CLAUDE.md\n")

	// Register this h2 directory in the routes registry.
	rootDir, err := config.RootDir()
	if err != nil {
		return fmt.Errorf("resolve root h2 dir: %w", err)
	}

	var explicitPrefix string
	if cmd.Flags().Changed("prefix") {
		explicitPrefix = prefix
	}

	resolvedPrefix, err := config.RegisterRouteWithAutoPrefix(rootDir, explicitPrefix, abs)
	if err != nil {
		return fmt.Errorf("register route: %w", err)
	}

	// Create the default role.
	rolesDir := filepath.Join(abs, "roles")
	rolePath, err := createRole(rolesDir, "default")
	if err != nil {
		return fmt.Errorf("create default role: %w", err)
	}
	fmt.Fprintf(out, "  Wrote roles/%s\n", filepath.Base(rolePath))

	fmt.Fprintf(out, "  Registered route (prefix: %s)\n", resolvedPrefix)
	fmt.Fprintf(out, "Initialized h2 directory at %s (prefix: %s)\n", abs, resolvedPrefix)
	return nil
}

// runGenerate regenerates specific config files in an existing h2 directory.
func runGenerate(abs, what string, force bool, out io.Writer) error {
	if !config.IsH2Dir(abs) {
		return fmt.Errorf("%s is not an h2 directory (--generate requires an existing h2 dir)", abs)
	}

	switch what {
	case "roles":
		return generateRoles(abs, force, out)
	case "instructions":
		return generateInstructions(abs, force, out)
	case "config":
		return generateConfig(abs, force, out)
	case "all":
		if err := generateConfig(abs, force, out); err != nil {
			return err
		}
		if err := generateInstructions(abs, force, out); err != nil {
			return err
		}
		return generateRoles(abs, force, out)
	default:
		return fmt.Errorf("unknown --generate type %q; valid: roles, instructions, config, all", what)
	}
}

// generateRoles regenerates the default role file.
func generateRoles(abs string, force bool, out io.Writer) error {
	content := config.RoleTemplate("default")
	ext := config.RoleFileExtension(content)
	fileName := "default" + ext
	rolePath := filepath.Join(abs, "roles", fileName)

	if !force {
		// Check both extensions.
		for _, e := range []string{".yaml", ".yaml.tmpl"} {
			p := filepath.Join(abs, "roles", "default"+e)
			if _, err := os.Stat(p); err == nil {
				fmt.Fprintf(out, "  Skipped roles/%s (already exists, use --force to overwrite)\n", filepath.Base(p))
				return nil
			}
		}
	} else {
		// Force: remove old file with either extension.
		for _, e := range []string{".yaml", ".yaml.tmpl"} {
			os.Remove(filepath.Join(abs, "roles", "default"+e))
		}
	}

	rolesDir := filepath.Join(abs, "roles")
	if err := os.MkdirAll(rolesDir, 0o755); err != nil {
		return fmt.Errorf("create roles dir: %w", err)
	}
	if err := os.WriteFile(rolePath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", fileName, err)
	}
	fmt.Fprintf(out, "  Wrote roles/%s\n", fileName)
	return nil
}

// generateInstructions regenerates CLAUDE.md and the AGENTS.md symlink.
func generateInstructions(abs string, force bool, out io.Writer) error {
	claudeMDPath := filepath.Join(abs, "claude-config", "default", "CLAUDE.md")
	agentsMDPath := filepath.Join(abs, "codex-config", "default", "AGENTS.md")

	// Ensure directories exist.
	if err := os.MkdirAll(filepath.Dir(claudeMDPath), 0o755); err != nil {
		return fmt.Errorf("create claude-config dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(agentsMDPath), 0o755); err != nil {
		return fmt.Errorf("create codex-config dir: %w", err)
	}

	if !force {
		if _, err := os.Stat(claudeMDPath); err == nil {
			fmt.Fprintf(out, "  Skipped claude-config/default/CLAUDE.md (already exists, use --force to overwrite)\n")
			// Still check the symlink.
			return ensureAgentsMDSymlink(abs, force, out)
		}
	}

	if err := os.WriteFile(claudeMDPath, []byte(config.InstructionsTemplate()), 0o644); err != nil {
		return fmt.Errorf("write CLAUDE.md: %w", err)
	}
	fmt.Fprintf(out, "  Wrote claude-config/default/CLAUDE.md\n")

	return ensureAgentsMDSymlink(abs, force, out)
}

// ensureAgentsMDSymlink creates or recreates the AGENTS.md symlink.
func ensureAgentsMDSymlink(abs string, force bool, out io.Writer) error {
	agentsMDPath := filepath.Join(abs, "codex-config", "default", "AGENTS.md")
	symlinkTarget := filepath.Join("..", "..", "claude-config", "default", "CLAUDE.md")

	// Check if symlink already exists and points to the right place.
	if existing, err := os.Readlink(agentsMDPath); err == nil {
		if existing == symlinkTarget {
			if !force {
				fmt.Fprintf(out, "  Skipped codex-config/default/AGENTS.md (symlink already correct)\n")
				return nil
			}
		}
		// Remove existing symlink (or file) before recreating.
		os.Remove(agentsMDPath)
	} else if !force {
		// Check if it's a regular file (not a symlink).
		if _, statErr := os.Stat(agentsMDPath); statErr == nil {
			fmt.Fprintf(out, "  Skipped codex-config/default/AGENTS.md (already exists, use --force to overwrite)\n")
			return nil
		}
	} else {
		// Force mode: remove whatever is there.
		os.Remove(agentsMDPath)
	}

	if err := os.Symlink(symlinkTarget, agentsMDPath); err != nil {
		return fmt.Errorf("symlink AGENTS.md: %w", err)
	}
	fmt.Fprintf(out, "  Symlinked codex-config/default/AGENTS.md -> %s\n", symlinkTarget)
	return nil
}

// generateConfig regenerates config.yaml.
func generateConfig(abs string, force bool, out io.Writer) error {
	configPath := filepath.Join(abs, "config.yaml")
	if !force {
		if _, err := os.Stat(configPath); err == nil {
			fmt.Fprintf(out, "  Skipped config.yaml (already exists, use --force to overwrite)\n")
			return nil
		}
	}
	if err := os.WriteFile(configPath, []byte(defaultConfigYAML), 0o644); err != nil {
		return fmt.Errorf("write config.yaml: %w", err)
	}
	fmt.Fprintf(out, "  Wrote config.yaml\n")
	return nil
}

// writeInstructions writes CLAUDE.md and creates the AGENTS.md symlink.
func writeInstructions(abs string) error {
	claudeMDPath := filepath.Join(abs, "claude-config", "default", "CLAUDE.md")
	if err := os.WriteFile(claudeMDPath, []byte(config.InstructionsTemplate()), 0o644); err != nil {
		return fmt.Errorf("write CLAUDE.md: %w", err)
	}

	agentsMDPath := filepath.Join(abs, "codex-config", "default", "AGENTS.md")
	symlinkTarget := filepath.Join("..", "..", "claude-config", "default", "CLAUDE.md")
	if err := os.Symlink(symlinkTarget, agentsMDPath); err != nil {
		return fmt.Errorf("symlink AGENTS.md: %w", err)
	}

	return nil
}

// checkDirSafeForInit checks whether the target directory is safe for init.
// If the dir doesn't exist, it's safe. If it exists but is empty, it's safe.
// If it contains files, they must all be expected root-dir files.
func checkDirSafeForInit(abs string) error {
	entries, err := os.ReadDir(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // doesn't exist yet — safe
		}
		return fmt.Errorf("read directory %s: %w", abs, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			return fmt.Errorf("directory %s already has content (found %s/); use an empty directory or one with only root-dir files", abs, entry.Name())
		}
		if !expectedRootDirFiles[entry.Name()] {
			return fmt.Errorf("directory %s already has content (found %s); use an empty directory or one with only root-dir files", abs, entry.Name())
		}
	}
	return nil
}
