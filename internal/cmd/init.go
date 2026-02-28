package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"h2/internal/config"
)

const (
	initStyleOpinionated = "opinionated"
	initStyleMinimal     = "minimal"
)

var validInitStyles = map[string]struct{}{
	initStyleOpinionated: {},
	initStyleMinimal:     {},
}

// expectedRootDirFiles are files that live at H2_ROOT_DIR and may already
// exist when initializing a directory. If the target dir contains only
// these (plus the marker), we allow init to proceed.
var expectedRootDirFiles = map[string]bool{
	".h2-dir.txt":          true,
	"routes.jsonl":         true,
	"terminal-colors.json": true,
	"terminal.json":        true,
	"config.yaml":          true,
}

func newInitCmd() *cobra.Command {
	var global bool
	var prefix string
	var generate string
	var force bool
	var style string

	cmd := &cobra.Command{
		Use:   "init <dir>",
		Short: "Initialize an h2 directory",
		Long: `Create an h2 directory with the standard structure.

Use --global to initialize ~/.h2/, or pass a directory path.

Use --generate to regenerate specific config files in an existing h2 directory:
  h2 init <path> --generate role          # regenerate the default role file
  h2 init <path> --generate profile       # regenerate default account profile files
  h2 init <path> --generate config        # regenerate config.yaml
  h2 init <path> --generate all           # regenerate all generated files

Use --force with --generate to overwrite existing files.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !global && len(args) == 0 {
				return fmt.Errorf("specify a directory path or use --global")
			}

			var dir string
			switch {
			case global:
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
			resolvedStyle, err := resolveInitStyle(style)
			if err != nil {
				return err
			}

			if generate != "" {
				return runGenerate(abs, generate, resolvedStyle, force, out)
			}

			return runFullInit(cmd, abs, prefix, resolvedStyle, out)
		},
	}

	cmd.Flags().BoolVar(&global, "global", false, "Initialize ~/.h2/ as the h2 directory")
	cmd.Flags().StringVar(&prefix, "prefix", "", "Custom prefix for this h2 directory in the routes registry")
	cmd.Flags().StringVar(&generate, "generate", "", "Regenerate specific config: role, profile, config, all")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing files when using --generate")
	cmd.Flags().StringVar(&style, "style", initStyleOpinionated, "Generation style: minimal, opinionated")
	return cmd
}

func resolveInitStyle(style string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(style))
	if s == "" {
		s = initStyleOpinionated
	}
	if _, ok := validInitStyles[s]; !ok {
		return "", fmt.Errorf("unknown --style %q; valid: minimal, opinionated", style)
	}
	return s, nil
}

// runFullInit performs a full h2 directory initialization.
func runFullInit(cmd *cobra.Command, abs, prefix, style string, out io.Writer) error {
	// --- Pre-flight validation (all checks before any writes) ---

	if config.IsH2Dir(abs) {
		return fmt.Errorf("%s is already an h2 directory", abs)
	}

	// Safety check: if the directory already exists and has content,
	// only allow init if it contains only expected root-dir files.
	if err := checkDirSafeForInit(abs); err != nil {
		return err
	}

	rootDir, err := config.RootDir()
	if err != nil {
		return fmt.Errorf("resolve root h2 dir: %w", err)
	}

	var explicitPrefix string
	if cmd.Flags().Changed("prefix") {
		explicitPrefix = prefix
		if err := config.ValidatePrefix(explicitPrefix); err != nil {
			return fmt.Errorf("invalid prefix: %w", err)
		}
	}

	// Verify the route can be registered before creating any files.
	if err := config.CheckRouteAvailable(rootDir, explicitPrefix, abs); err != nil {
		return err
	}

	// --- All validation passed, start writing ---

	fmt.Fprintf(out, "Creating h2 directory at %s...\n", abs)

	subdirs := []string{
		"roles",
		"sessions",
		"sockets",
		filepath.Join("claude-config", "default"),
		filepath.Join("codex-config", "default"),
		filepath.Join("account-profiles-shared", "default", "skills"),
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
	if err := os.WriteFile(configPath, []byte(config.ConfigTemplate(style)), 0o644); err != nil {
		return fmt.Errorf("write config.yaml: %w", err)
	}
	fmt.Fprintf(out, "  Wrote config.yaml\n")

	// Write CLAUDE.md and symlink AGENTS.md.
	if err := scaffoldProfile(abs, "default", style, profileHarnessAll, out, false); err != nil {
		return fmt.Errorf("scaffold default profile: %w", err)
	}

	// Register this h2 directory in the routes registry (pre-flight check already passed).
	resolvedPrefix, err := config.RegisterRouteWithAutoPrefix(rootDir, explicitPrefix, abs)
	if err != nil {
		return fmt.Errorf("register route: %w", err)
	}

	// Create the default role.
	rolePath, err := upsertDefaultRole(abs, style, true, out)
	if err != nil {
		return fmt.Errorf("create default role: %w", err)
	}
	_ = rolePath

	fmt.Fprintf(out, "  Registered route (prefix: %s)\n", resolvedPrefix)
	fmt.Fprintf(out, "Initialized h2 directory at %s (prefix: %s)\n", abs, resolvedPrefix)
	return nil
}

// runGenerate regenerates specific config files in an existing h2 directory.
func runGenerate(abs, what, style string, force bool, out io.Writer) error {
	if !config.IsH2Dir(abs) {
		return fmt.Errorf("%s is not an h2 directory (--generate requires an existing h2 dir)", abs)
	}

	switch what {
	case "role":
		return generateDefaultRole(abs, style, force, out)
	case "profile":
		return generateDefaultProfile(abs, style, force, out)
	// Legacy aliases kept for compatibility; prefer role/profile.
	case "roles":
		return generateRoles(abs, style, force, out)
	case "instructions":
		return generateInstructions(abs, style, force, out)
	case "skills":
		return generateSkills(abs, style, force, out)
	case "harness-config":
		return generateHarnessPolicyFiles(abs, style, force, out)
	case "config":
		return generateConfig(abs, style, force, out)
	case "all":
		if err := generateConfig(abs, style, force, out); err != nil {
			return err
		}
		if err := generateDefaultProfile(abs, style, force, out); err != nil {
			return err
		}
		return generateRoles(abs, style, force, out)
	default:
		return fmt.Errorf("unknown --generate type %q; valid: role, profile, config, all", what)
	}
}

func generateDefaultProfile(abs, style string, force bool, out io.Writer) error {
	if !force {
		items := []generatePreflightItem{
			checkFilePreflight(filepath.Join(abs, "account-profiles-shared", "default", "CLAUDE_AND_AGENTS.md"), "account-profiles-shared/default/CLAUDE_AND_AGENTS.md")[0],
			checkDirPreflight(filepath.Join(abs, "account-profiles-shared", "default", "skills"), "account-profiles-shared/default/skills"),
			checkSymlinkPreflight(filepath.Join(abs, "claude-config", "default", "CLAUDE.md"), filepath.Join("..", "..", "account-profiles-shared", "default", "CLAUDE_AND_AGENTS.md"), "claude-config/default/CLAUDE.md"),
			checkSymlinkPreflight(filepath.Join(abs, "claude-config", "default", "skills"), filepath.Join("..", "..", "account-profiles-shared", "default", "skills"), "claude-config/default/skills"),
			checkFilePreflight(filepath.Join(abs, "claude-config", "default", "settings.json"), "claude-config/default/settings.json")[0],
			checkSymlinkPreflight(filepath.Join(abs, "codex-config", "default", "AGENTS.md"), filepath.Join("..", "..", "account-profiles-shared", "default", "CLAUDE_AND_AGENTS.md"), "codex-config/default/AGENTS.md"),
			checkSymlinkPreflight(filepath.Join(abs, "codex-config", "default", "skills"), filepath.Join("..", "..", "account-profiles-shared", "default", "skills"), "codex-config/default/skills"),
			checkFilePreflight(filepath.Join(abs, "codex-config", "default", "config.toml"), "codex-config/default/config.toml")[0],
			checkFilePreflight(filepath.Join(abs, "codex-config", "default", "requirements.toml"), "codex-config/default/requirements.toml")[0],
		}
		if err := summarizePreflight("profile", items); err != nil {
			return err
		}
	}
	return scaffoldProfile(abs, "default", style, profileHarnessAll, out, false)
}

func generateDefaultRole(abs, style string, force bool, out io.Writer) error {
	_, err := upsertDefaultRole(abs, style, force, out)
	return err
}

func upsertDefaultRole(abs, style string, force bool, out io.Writer) (string, error) {
	rolesDir := filepath.Join(abs, "roles")
	if err := os.MkdirAll(rolesDir, 0o755); err != nil {
		return "", fmt.Errorf("create roles dir: %w", err)
	}
	content := config.RoleTemplateWithStyle("default", style)
	ext := config.RoleFileExtension(content)
	targetPath := filepath.Join(rolesDir, "default"+ext)
	targetLabel := filepath.Join("roles", "default"+ext)
	if !force {
		items := []generatePreflightItem{}
		for _, e := range []string{".yaml", ".yaml.tmpl"} {
			path := filepath.Join(rolesDir, "default"+e)
			label := filepath.Join("roles", "default"+e)
			if _, err := os.Stat(path); err == nil {
				items = append(items, generatePreflightItem{Label: label, Status: "exists", Conflict: true})
			} else if err != nil && !os.IsNotExist(err) {
				items = append(items, generatePreflightItem{Label: label, Status: fmt.Sprintf("error: %v", err), Conflict: true})
			}
		}
		if len(items) == 0 {
			items = append(items, checkFilePreflight(targetPath, targetLabel)[0])
		}
		if err := summarizePreflight("role", items); err != nil {
			return "", err
		}
	}
	if force {
		_ = os.Remove(filepath.Join(rolesDir, "default.yaml"))
		_ = os.Remove(filepath.Join(rolesDir, "default.yaml.tmpl"))
	}
	if err := os.WriteFile(targetPath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", targetLabel, err)
	}
	fmt.Fprintf(out, "  Wrote %s\n", targetLabel)
	return targetPath, nil
}

// generateRoles regenerates role files for the selected style.
func generateRoles(abs, style string, force bool, out io.Writer) error {
	rolesDir := filepath.Join(abs, "roles")
	if err := os.MkdirAll(rolesDir, 0o755); err != nil {
		return fmt.Errorf("create roles dir: %w", err)
	}

	roleNames := config.RoleTemplateNamesWithStyle(style)
	if !force {
		var items []generatePreflightItem
		for _, roleName := range roleNames {
			content := config.RoleTemplateWithStyle(roleName, style)
			ext := config.RoleFileExtension(content)
			target := filepath.Join("roles", roleName+ext)
			items = append(items, checkFilePreflight(filepath.Join(abs, target), target)...)
			for _, e := range []string{".yaml", ".yaml.tmpl"} {
				p := filepath.Join(abs, "roles", roleName+e)
				label := filepath.Join("roles", roleName+e)
				if _, err := os.Stat(p); err == nil {
					items = append(items, generatePreflightItem{
						Label:    label,
						Status:   "exists",
						Conflict: true,
					})
				} else if err != nil && !os.IsNotExist(err) {
					items = append(items, generatePreflightItem{
						Label:    label,
						Status:   fmt.Sprintf("error: %v", err),
						Conflict: true,
					})
				}
			}
		}
		if err := summarizePreflight("roles", items); err != nil {
			return err
		}
	}

	for _, roleName := range roleNames {
		content := config.RoleTemplateWithStyle(roleName, style)
		ext := config.RoleFileExtension(content)
		fileName := roleName + ext
		rolePath := filepath.Join(abs, "roles", fileName)

		if force {
			// Force: remove old file with either extension.
			for _, e := range []string{".yaml", ".yaml.tmpl"} {
				_ = os.Remove(filepath.Join(abs, "roles", roleName+e))
			}
		}

		if err := os.WriteFile(rolePath, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", fileName, err)
		}
		fmt.Fprintf(out, "  Wrote roles/%s\n", fileName)
	}
	return nil
}

// generateInstructions regenerates CLAUDE.md and the AGENTS.md symlink.
func generateInstructions(abs, style string, force bool, out io.Writer) error {
	sharedDir := filepath.Join(abs, "account-profiles-shared", "default")
	sharedMDPath := filepath.Join(sharedDir, "CLAUDE_AND_AGENTS.md")
	claudeDir := filepath.Join(abs, "claude-config", "default")
	codexDir := filepath.Join(abs, "codex-config", "default")
	claudeMDPath := filepath.Join(claudeDir, "CLAUDE.md")
	agentsMDPath := filepath.Join(codexDir, "AGENTS.md")

	// Ensure directories exist.
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return fmt.Errorf("create claude-config dir: %w", err)
	}
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		return fmt.Errorf("create codex-config dir: %w", err)
	}
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		return fmt.Errorf("create shared profile dir: %w", err)
	}

	sharedMDTarget := filepath.Join("..", "..", "account-profiles-shared", "default", "CLAUDE_AND_AGENTS.md")
	if !force {
		items := []generatePreflightItem{
			checkFilePreflight(sharedMDPath, "account-profiles-shared/default/CLAUDE_AND_AGENTS.md")[0],
		}
		items = append(items, checkSymlinkPreflight(claudeMDPath, sharedMDTarget, "claude-config/default/CLAUDE.md"))
		items = append(items, checkSymlinkPreflight(agentsMDPath, sharedMDTarget, "codex-config/default/AGENTS.md"))
		if err := summarizePreflight("instructions", items); err != nil {
			return err
		}
	}

	if err := os.WriteFile(sharedMDPath, []byte(config.InstructionsTemplateWithStyle(style)), 0o644); err != nil {
		return fmt.Errorf("write CLAUDE_AND_AGENTS.md: %w", err)
	}
	fmt.Fprintf(out, "  Wrote account-profiles-shared/default/CLAUDE_AND_AGENTS.md\n")

	if err := ensureSymlink(claudeMDPath, sharedMDTarget, force, out, "claude-config/default/CLAUDE.md"); err != nil {
		return err
	}
	if err := ensureSymlink(agentsMDPath, sharedMDTarget, force, out, "codex-config/default/AGENTS.md"); err != nil {
		return err
	}
	return nil
}

func generateSkills(abs, style string, force bool, out io.Writer) error {
	claudeDir := filepath.Join(abs, "claude-config", "default")
	codexDir := filepath.Join(abs, "codex-config", "default")
	claudeSkillsPath := filepath.Join(claudeDir, "skills")
	codexSkillsPath := filepath.Join(codexDir, "skills")
	sharedSkillsDir := filepath.Join(abs, "account-profiles-shared", "default", "skills")
	sharedSkillsTarget := filepath.Join("..", "..", "account-profiles-shared", "default", "skills")

	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return fmt.Errorf("create claude-config dir: %w", err)
	}
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		return fmt.Errorf("create codex-config dir: %w", err)
	}

	if !force {
		items := []generatePreflightItem{
			checkDirPreflight(sharedSkillsDir, "account-profiles-shared/default/skills"),
		}
		items = append(items, checkSymlinkPreflight(claudeSkillsPath, sharedSkillsTarget, "claude-config/default/skills"))
		items = append(items, checkSymlinkPreflight(codexSkillsPath, sharedSkillsTarget, "codex-config/default/skills"))
		if err := summarizePreflight("skills", items); err != nil {
			return err
		}
	}

	if err := config.WriteSkillsTemplate(style, sharedSkillsDir, force); err != nil {
		return fmt.Errorf("write shared skills: %w", err)
	}
	if err := ensureSymlink(claudeSkillsPath, sharedSkillsTarget, force, out, "claude-config/default/skills"); err != nil {
		return err
	}
	if err := ensureSymlink(codexSkillsPath, sharedSkillsTarget, force, out, "codex-config/default/skills"); err != nil {
		return err
	}
	fmt.Fprintf(out, "  Wrote account-profiles-shared/default/skills/\n")
	return nil
}

// ensureSymlink creates or recreates a symlink.
func ensureSymlink(path, target string, force bool, out io.Writer, label string) error {
	if existing, err := os.Readlink(path); err == nil {
		if existing == target {
			if !force {
				fmt.Fprintf(out, "  Skipped %s (symlink already correct)\n", label)
				return nil
			}
		}
		_ = os.Remove(path)
	} else if !force {
		if _, statErr := os.Stat(path); statErr == nil {
			fmt.Fprintf(out, "  Skipped %s (already exists, use --force to overwrite)\n", label)
			return nil
		}
	} else {
		_ = os.Remove(path)
	}

	if err := os.Symlink(target, path); err != nil {
		return fmt.Errorf("symlink %s: %w", label, err)
	}
	fmt.Fprintf(out, "  Symlinked %s -> %s\n", label, target)
	return nil
}

// generateConfig regenerates config.yaml.
func generateConfig(abs, style string, force bool, out io.Writer) error {
	configPath := filepath.Join(abs, "config.yaml")
	if !force {
		if err := summarizePreflight("config", []generatePreflightItem{
			checkFilePreflight(configPath, "config.yaml")[0],
		}); err != nil {
			return err
		}
	}
	if err := os.WriteFile(configPath, []byte(config.ConfigTemplate(style)), 0o644); err != nil {
		return fmt.Errorf("write config.yaml: %w", err)
	}
	fmt.Fprintf(out, "  Wrote config.yaml\n")
	return nil
}

func generateHarnessPolicyFiles(abs, style string, force bool, out io.Writer) error {
	claudeSettingsPath := filepath.Join(abs, "claude-config", "default", "settings.json")
	codexConfigPath := filepath.Join(abs, "codex-config", "default", "config.toml")
	codexRequirementsPath := filepath.Join(abs, "codex-config", "default", "requirements.toml")
	if !force {
		if err := summarizePreflight("harness-config", []generatePreflightItem{
			checkFilePreflight(claudeSettingsPath, "claude-config/default/settings.json")[0],
			checkFilePreflight(codexConfigPath, "codex-config/default/config.toml")[0],
			checkFilePreflight(codexRequirementsPath, "codex-config/default/requirements.toml")[0],
		}); err != nil {
			return err
		}
	}

	if err := writeGeneratedFile(claudeSettingsPath, config.ClaudeSettingsTemplate(style), force, out, "claude-config/default/settings.json"); err != nil {
		return err
	}
	if err := writeGeneratedFile(codexConfigPath, config.CodexConfigTemplate(style), force, out, "codex-config/default/config.toml"); err != nil {
		return err
	}
	if err := writeGeneratedFile(codexRequirementsPath, config.CodexRequirementsTemplate(style), force, out, "codex-config/default/requirements.toml"); err != nil {
		return err
	}
	return nil
}

func writeGeneratedFile(path, content string, force bool, out io.Writer, label string) error {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", label, err)
	}
	fmt.Fprintf(out, "  Wrote %s\n", label)
	return nil
}

type generatePreflightItem struct {
	Label    string
	Status   string
	Conflict bool
}

func summarizePreflight(target string, items []generatePreflightItem) error {
	conflicts := 0
	sort.Slice(items, func(i, j int) bool { return items[i].Label < items[j].Label })
	for _, item := range items {
		if item.Conflict {
			conflicts++
		}
	}
	if conflicts == 0 {
		return nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--generate %s preflight failed (%d conflict(s)); use --force to overwrite\n", target, conflicts)
	fmt.Fprintf(&b, "Planned targets:\n")
	for _, item := range items {
		fmt.Fprintf(&b, "  %s: %s\n", item.Label, item.Status)
	}
	return fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
}

func checkFilePreflight(path, label string) []generatePreflightItem {
	if _, err := os.Stat(path); err == nil {
		return []generatePreflightItem{{Label: label, Status: "exists", Conflict: true}}
	} else if os.IsNotExist(err) {
		return []generatePreflightItem{{Label: label, Status: "missing"}}
	} else {
		return []generatePreflightItem{{Label: label, Status: fmt.Sprintf("error: %v", err), Conflict: true}}
	}
}

func checkDirPreflight(path, label string) generatePreflightItem {
	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return generatePreflightItem{Label: label, Status: "missing"}
		}
		return generatePreflightItem{Label: label, Status: fmt.Sprintf("error: %v", err), Conflict: true}
	}
	if len(entries) == 0 {
		return generatePreflightItem{Label: label, Status: "exists (empty dir)"}
	}
	return generatePreflightItem{Label: label, Status: "exists (non-empty dir)", Conflict: true}
}

func checkSymlinkPreflight(path, target, label string) generatePreflightItem {
	if existing, err := os.Readlink(path); err == nil {
		if existing == target {
			return generatePreflightItem{Label: label, Status: "ok (correct symlink)"}
		}
		return generatePreflightItem{
			Label:    label,
			Status:   fmt.Sprintf("exists (symlink -> %q, expected %q)", existing, target),
			Conflict: true,
		}
	}

	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return generatePreflightItem{Label: label, Status: "missing"}
		}
		return generatePreflightItem{
			Label:    label,
			Status:   fmt.Sprintf("error: %v", err),
			Conflict: true,
		}
	}

	if info.IsDir() {
		return generatePreflightItem{Label: label, Status: "exists (directory)", Conflict: true}
	}
	return generatePreflightItem{Label: label, Status: "exists (file)", Conflict: true}
}

// writeInstructions writes shared CLAUDE_AND_AGENTS.md and creates profile symlinks.
func writeInstructions(abs, style string) error {
	sharedDir := filepath.Join(abs, "account-profiles-shared", "default")
	sharedMDPath := filepath.Join(sharedDir, "CLAUDE_AND_AGENTS.md")
	sharedSkillsDir := filepath.Join(sharedDir, "skills")
	claudeDir := filepath.Join(abs, "claude-config", "default")
	codexDir := filepath.Join(abs, "codex-config", "default")
	claudeMDPath := filepath.Join(claudeDir, "CLAUDE.md")
	agentsMDPath := filepath.Join(codexDir, "AGENTS.md")
	claudeSkillsPath := filepath.Join(claudeDir, "skills")
	codexSkillsPath := filepath.Join(codexDir, "skills")

	if err := os.MkdirAll(sharedSkillsDir, 0o755); err != nil {
		return fmt.Errorf("create shared profile skills dir: %w", err)
	}
	if err := config.WriteSkillsTemplate(style, sharedSkillsDir, false); err != nil {
		return fmt.Errorf("write shared skills: %w", err)
	}
	if err := os.WriteFile(sharedMDPath, []byte(config.InstructionsTemplateWithStyle(style)), 0o644); err != nil {
		return fmt.Errorf("write CLAUDE_AND_AGENTS.md: %w", err)
	}

	sharedMDTarget := filepath.Join("..", "..", "account-profiles-shared", "default", "CLAUDE_AND_AGENTS.md")
	sharedSkillsTarget := filepath.Join("..", "..", "account-profiles-shared", "default", "skills")
	if err := os.Symlink(sharedMDTarget, claudeMDPath); err != nil {
		return fmt.Errorf("symlink CLAUDE.md: %w", err)
	}
	if err := os.Symlink(sharedMDTarget, agentsMDPath); err != nil {
		return fmt.Errorf("symlink AGENTS.md: %w", err)
	}
	if err := os.Symlink(sharedSkillsTarget, claudeSkillsPath); err != nil {
		return fmt.Errorf("symlink claude skills dir: %w", err)
	}
	if err := os.Symlink(sharedSkillsTarget, codexSkillsPath); err != nil {
		return fmt.Errorf("symlink codex skills dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(config.ClaudeSettingsTemplate(style)), 0o644); err != nil {
		return fmt.Errorf("write claude settings.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "requirements.toml"), []byte(config.CodexRequirementsTemplate(style)), 0o644); err != nil {
		return fmt.Errorf("write codex requirements.toml: %w", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(config.CodexConfigTemplate(style)), 0o644); err != nil {
		return fmt.Errorf("write codex config.toml: %w", err)
	}

	return nil
}

func createRoleWithStyle(rolesDir, name, style string) (string, error) {
	content := config.RoleTemplateWithStyle(name, style)
	ext := config.RoleFileExtension(content)
	path := filepath.Join(rolesDir, name+ext)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
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
