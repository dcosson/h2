package cmd

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"h2/internal/config"
	"h2/internal/termstyle"
)

const (
	profileHarnessAll    = "all"
	profileHarnessClaude = "claude_code"
	profileHarnessCodex  = "codex"
)

func newProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage profiles",
	}
	cmd.AddCommand(newProfileCreateCmd())
	cmd.AddCommand(newProfileUpdateCmd())
	cmd.AddCommand(newProfileListCmd())
	cmd.AddCommand(newProfileShowCmd())
	return cmd
}

func newProfileUpdateCmd() *cobra.Command {
	var style string
	var includeAuth bool
	var includeSkills bool
	var includeInstructions bool
	var includeSettings bool
	var all bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "update [<name>]",
		Short: "Update a profile to generated defaults",
		Long: `Update profile content to h2-generated defaults.

By default, update refreshes instructions, managed skills, and settings, while
preserving auth files.

Managed skills are updated non-destructively: h2 updates only template-managed
skill files and leaves user-added skills untouched.

Use --all to update every profile in the profiles-shared directory.
Use --dry-run to preview what would be added or changed without writing.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedStyle, err := resolveInitStyle(style)
			if err != nil {
				return err
			}
			h2Dir := config.ConfigDir()
			opts := resetProfileOpts{
				includeAuth:         includeAuth,
				includeSkills:       includeSkills,
				includeInstructions: includeInstructions,
				includeSettings:     includeSettings,
				dryRun:              dryRun,
			}

			if all {
				if len(args) > 0 {
					return fmt.Errorf("cannot specify both --all and a profile name")
				}
				profiles, err := discoverProfiles(h2Dir)
				if err != nil {
					return err
				}
				if len(profiles) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "No profiles found.")
					return nil
				}
				for _, name := range profiles {
					fmt.Fprintf(cmd.OutOrStdout(), "Updating profile %q:\n", name)
					if err := resetProfile(h2Dir, name, resolvedStyle, opts, cmd.OutOrStdout()); err != nil {
						return fmt.Errorf("update profile %q: %w", name, err)
					}
					fmt.Fprintln(cmd.OutOrStdout())
				}
				return nil
			}

			if len(args) < 1 {
				return fmt.Errorf("profile name is required (or use --all)")
			}
			name := strings.TrimSpace(args[0])
			if name == "" {
				return fmt.Errorf("profile name is required")
			}
			if strings.ContainsRune(name, os.PathSeparator) {
				return fmt.Errorf("profile name must not contain path separators: %q", name)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Updating profile %q:\n", name)
			return resetProfile(h2Dir, name, resolvedStyle, opts, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&style, "style", initStyleOpinionated, "Profile style: minimal, opinionated")
	cmd.Flags().BoolVar(&includeAuth, "include-auth", false, "Include auth files (.claude.json, auth.json) in update")
	cmd.Flags().BoolVar(&includeSkills, "include-skills", true, "Update managed shared skills")
	cmd.Flags().BoolVar(&includeInstructions, "include-instructions", true, "Update shared instructions file")
	cmd.Flags().BoolVar(&includeSettings, "include-settings", true, "Update harness settings/config files and profile symlinks")
	cmd.Flags().BoolVar(&all, "all", false, "Update all installed profiles")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview changes without writing")
	return cmd
}

func newProfileListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			h2Dir := config.ConfigDir()
			profiles, err := discoverProfilesWithHarness(h2Dir)
			if err != nil {
				return err
			}
			if len(profiles) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No profiles found.")
				return nil
			}
			for _, p := range profiles {
				fmt.Fprintf(cmd.OutOrStdout(), "%s (%s)\n", p.Name, formatHarnessLabels(p))
			}
			return nil
		},
	}
}

func newProfileShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show profile details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return fmt.Errorf("profile name is required")
			}

			h2Dir := config.ConfigDir()
			sharedDir := filepath.Join(h2Dir, "profiles-shared", name)
			claudeDir := filepath.Join(h2Dir, "claude-config", name)
			codexDir := filepath.Join(h2Dir, "codex-config", name)

			sharedExists := pathExists(sharedDir)
			claudeExists := pathExists(claudeDir)
			codexExists := pathExists(codexDir)

			if !sharedExists && !claudeExists && !codexExists {
				return fmt.Errorf("profile %q not found", name)
			}

			out := cmd.OutOrStdout()

			// Build harness list for header with rate limit info.
			rlMap := map[string]*config.RateLimitInfo{}
			var harnesses []string
			if claudeExists {
				harnesses = append(harnesses, profileHarnessClaude)
				if rl := config.IsProfileRateLimited(claudeDir); rl != nil {
					rlMap[profileHarnessClaude] = rl
				}
			}
			if codexExists {
				harnesses = append(harnesses, profileHarnessCodex)
				if rl := config.IsProfileRateLimited(codexDir); rl != nil {
					rlMap[profileHarnessCodex] = rl
				}
			}
			if len(harnesses) > 0 {
				p := profileInfo{Name: name, Harnesses: harnesses, RateLimitedMap: rlMap}
				fmt.Fprintf(out, "Profile: %s (%s)\n", name, formatHarnessLabels(p))
			} else {
				fmt.Fprintf(out, "Profile: %s (shared only)\n", name)
			}
			fmt.Fprintf(out, "  Shared: %s (%s)\n", sharedDir, yesNo(sharedExists))
			fmt.Fprintf(out, "  Claude: %s (%s)\n", claudeDir, yesNo(claudeExists))
			fmt.Fprintf(out, "  Codex:  %s (%s)\n", codexDir, yesNo(codexExists))
			fmt.Fprintf(out, "  Symlink profiles-shared/%s: %s\n", name, symlinkStatus(sharedDir))
			fmt.Fprintf(out, "  Symlink claude-config/%s/CLAUDE.md: %s\n", name, symlinkStatus(filepath.Join(claudeDir, "CLAUDE.md")))
			fmt.Fprintf(out, "  Symlink claude-config/%s/skills: %s\n", name, symlinkStatus(filepath.Join(claudeDir, "skills")))
			fmt.Fprintf(out, "  Symlink claude-config/%s/shared-skill-scripts: %s\n", name, symlinkStatus(filepath.Join(claudeDir, "shared-skill-scripts")))
			fmt.Fprintf(out, "  Symlink codex-config/%s/AGENTS.md: %s\n", name, symlinkStatus(filepath.Join(codexDir, "AGENTS.md")))
			fmt.Fprintf(out, "  Symlink codex-config/%s/skills: %s\n", name, symlinkStatus(filepath.Join(codexDir, "skills")))
			fmt.Fprintf(out, "  Symlink codex-config/%s/shared-skill-scripts: %s\n", name, symlinkStatus(filepath.Join(codexDir, "shared-skill-scripts")))

			if claudeExists {
				auth, err := config.IsClaudeConfigAuthenticated(claudeDir)
				if err != nil {
					fmt.Fprintf(out, "  Claude authenticated: error (%v)\n", err)
				} else {
					fmt.Fprintf(out, "  Claude authenticated: %s\n", yesNo(auth))
				}
			}
			if err := printContentMeta(out, "profiles-shared/"+name, sharedDir); err != nil {
				return err
			}
			if err := printContentMeta(out, "claude-config/"+name, claudeDir); err != nil {
				return err
			}
			if err := printContentMeta(out, "codex-config/"+name, codexDir); err != nil {
				return err
			}
			return nil
		},
	}
}

func newProfileCreateCmd() *cobra.Command {
	var style string
	var symlinkSharedFrom string
	var harnessType string

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new profile",
		Long: `Create a named profile scaffold under the h2 directory.

By default this creates both Claude and Codex profile files. Use --agent-harness
to create only one harness profile:
  --agent-harness claude_code
  --agent-harness codex
  --agent-harness all

Use --symlink-shared to link shared profile content from an existing profile.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return fmt.Errorf("profile name is required")
			}
			if strings.ContainsRune(name, os.PathSeparator) {
				return fmt.Errorf("profile name must not contain path separators: %q", name)
			}

			resolvedStyle, err := resolveInitStyle(style)
			if err != nil {
				return err
			}

			harnessType = strings.TrimSpace(harnessType)
			switch harnessType {
			case profileHarnessAll, profileHarnessClaude, profileHarnessCodex:
			default:
				return fmt.Errorf("invalid --agent-harness %q; valid: %s, %s, %s",
					harnessType, profileHarnessClaude, profileHarnessCodex, profileHarnessAll)
			}

			symlinkSharedFrom = strings.TrimSpace(symlinkSharedFrom)
			if symlinkSharedFrom == name {
				return fmt.Errorf("--symlink-shared source must be different from target profile name")
			}
			if symlinkSharedFrom != "" && strings.ContainsRune(symlinkSharedFrom, os.PathSeparator) {
				return fmt.Errorf("--symlink-shared source must not contain path separators: %q", symlinkSharedFrom)
			}

			h2Dir := config.ConfigDir()
			return createProfile(h2Dir, name, resolvedStyle, symlinkSharedFrom, harnessType, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&style, "style", initStyleOpinionated, "Profile style: minimal, opinionated")
	cmd.Flags().StringVar(&symlinkSharedFrom, "symlink-shared", "", "Symlink shared profile content from an existing profile name")
	cmd.Flags().StringVar(&harnessType, "agent-harness", profileHarnessAll, "Harness profile to create: claude_code, codex, all")
	return cmd
}

func createProfile(h2Dir, name, style, symlinkSharedFrom, harnessType string, out io.Writer) error {
	return createOrUpdateProfile(h2Dir, name, style, symlinkSharedFrom, harnessType, true, true, out)
}

// resetProfileOpts holds options for resetProfile.
type resetProfileOpts struct {
	includeAuth         bool
	includeSkills       bool
	includeInstructions bool
	includeSettings     bool
	dryRun              bool
}

// fileStatus describes the dry-run status of a file.
type fileStatus int

const (
	fileUnchanged fileStatus = iota
	fileUpdated
	fileAdded
)

// compareFileContent returns the status of a file relative to new content.
func compareFileContent(path, newContent string) fileStatus {
	existing, err := os.ReadFile(path)
	if err != nil {
		return fileAdded
	}
	if string(existing) == newContent {
		return fileUnchanged
	}
	return fileUpdated
}

// fileStatusLabel returns a human-readable label for a file status.
func fileStatusLabel(s fileStatus) string {
	switch s {
	case fileUnchanged:
		return "unchanged"
	case fileUpdated:
		return "updated"
	case fileAdded:
		return "added"
	}
	return "unknown"
}

func resetProfile(h2Dir, name, style string, opts resetProfileOpts, out io.Writer) error {
	sharedDir := filepath.Join(h2Dir, "profiles-shared", name)
	sharedSkillsDir := filepath.Join(sharedDir, "skills")
	claudeDir := filepath.Join(h2Dir, "claude-config", name)
	codexDir := filepath.Join(h2Dir, "codex-config", name)

	sharedExists := pathExists(sharedDir)
	claudeExists := pathExists(claudeDir)
	codexExists := pathExists(codexDir)
	if !sharedExists && !claudeExists && !codexExists {
		return fmt.Errorf("profile %q not found", name)
	}

	if !opts.dryRun && (opts.includeInstructions || opts.includeSkills) {
		if err := os.MkdirAll(sharedDir, 0o755); err != nil {
			return fmt.Errorf("create shared profile dir: %w", err)
		}
	}

	if opts.includeInstructions {
		label := fmt.Sprintf("profiles-shared/%s/CLAUDE_AND_AGENTS.md", name)
		content := config.InstructionsTemplateWithStyle(style)
		status := compareFileContent(filepath.Join(sharedDir, "CLAUDE_AND_AGENTS.md"), content)
		if opts.dryRun {
			fmt.Fprintf(out, "  %s: %s\n", label, fileStatusLabel(status))
		} else {
			if err := os.WriteFile(filepath.Join(sharedDir, "CLAUDE_AND_AGENTS.md"), []byte(content), 0o644); err != nil {
				return fmt.Errorf("write CLAUDE_AND_AGENTS.md: %w", err)
			}
			if err := config.UpsertContentMeta(sharedDir, style, []string{"CLAUDE_AND_AGENTS.md"}); err != nil {
				return fmt.Errorf("update shared metadata: %w", err)
			}
			fmt.Fprintf(out, "  %s: %s\n", label, fileStatusLabel(status))
		}
	}

	if opts.includeSkills {
		if err := resetProfileSkills(h2Dir, name, style, sharedDir, sharedSkillsDir, opts.dryRun, out); err != nil {
			return err
		}
	}

	if opts.includeSettings {
		if claudeExists {
			if err := resetProfileClaudeSettings(claudeDir, name, style, opts.dryRun, out); err != nil {
				return err
			}
		}
		if codexExists {
			if err := resetProfileCodexSettings(codexDir, name, style, opts.dryRun, out); err != nil {
				return err
			}
		}
	}

	if opts.includeAuth {
		if claudeExists {
			authPath := filepath.Join(claudeDir, ".claude.json")
			label := fmt.Sprintf("claude-config/%s/.claude.json", name)
			if opts.dryRun {
				if pathExists(authPath) {
					fmt.Fprintf(out, "  %s: would clear\n", label)
				} else {
					fmt.Fprintf(out, "  %s: not present\n", label)
				}
			} else {
				if err := os.Remove(authPath); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("remove claude auth: %w", err)
				}
				if pathExists(authPath) {
					return fmt.Errorf("remove claude auth: %s still exists", authPath)
				}
				fmt.Fprintf(out, "  %s: cleared\n", label)
			}
		}
		if codexExists {
			authPath := filepath.Join(codexDir, "auth.json")
			label := fmt.Sprintf("codex-config/%s/auth.json", name)
			if opts.dryRun {
				if pathExists(authPath) {
					fmt.Fprintf(out, "  %s: would clear\n", label)
				} else {
					fmt.Fprintf(out, "  %s: not present\n", label)
				}
			} else {
				if err := os.Remove(authPath); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("remove codex auth: %w", err)
				}
				if pathExists(authPath) {
					return fmt.Errorf("remove codex auth: %s still exists", authPath)
				}
				fmt.Fprintf(out, "  %s: cleared\n", label)
			}
		}
	}

	if opts.dryRun {
		fmt.Fprintf(out, "Dry run for profile %q (no changes written)\n", name)
	} else {
		fmt.Fprintf(out, "Updated profile %q\n", name)
	}
	return nil
}

// resetProfileSkills handles the skills and shared-skill-scripts portion of a profile update.
func resetProfileSkills(h2Dir, name, style, sharedDir, sharedSkillsDir string, dryRun bool, out io.Writer) error {
	skillPaths, err := managedSkillRelativePaths(style)
	if err != nil {
		return err
	}

	if dryRun {
		for _, relPath := range skillPaths {
			label := fmt.Sprintf("profiles-shared/%s/%s", name, relPath)
			templatePath := fmt.Sprintf("templates/styles/%s/%s", style, relPath)
			data, readErr := config.Templates.ReadFile(templatePath)
			if readErr != nil {
				continue
			}
			status := compareFileContent(filepath.Join(sharedDir, relPath), string(data))
			fmt.Fprintf(out, "  %s: %s\n", label, fileStatusLabel(status))
		}
	} else {
		if err := writeManagedSkillsTemplateNonDestructive(style, sharedSkillsDir); err != nil {
			return fmt.Errorf("write shared skills: %w", err)
		}
		if len(skillPaths) > 0 {
			if err := config.UpsertContentMeta(sharedDir, style, skillPaths); err != nil {
				return fmt.Errorf("update shared metadata: %w", err)
			}
		}
		for _, relPath := range skillPaths {
			fmt.Fprintf(out, "  profiles-shared/%s/%s: updated\n", name, relPath)
		}
	}

	sharedScriptsDir := filepath.Join(sharedDir, "shared-skill-scripts")
	scriptPaths, err := managedSharedSkillScriptRelativePaths(style)
	if err != nil {
		return err
	}

	if dryRun {
		for _, relPath := range scriptPaths {
			label := fmt.Sprintf("profiles-shared/%s/%s", name, relPath)
			templatePath := fmt.Sprintf("templates/styles/%s/%s", style, relPath)
			data, readErr := config.Templates.ReadFile(templatePath)
			if readErr != nil {
				continue
			}
			status := compareFileContent(filepath.Join(sharedDir, relPath), string(data))
			fmt.Fprintf(out, "  %s: %s\n", label, fileStatusLabel(status))
		}
	} else {
		if err := writeManagedSharedSkillScriptsNonDestructive(style, sharedScriptsDir); err != nil {
			return fmt.Errorf("write shared-skill-scripts: %w", err)
		}
		if len(scriptPaths) > 0 {
			if err := config.UpsertContentMeta(sharedDir, style, scriptPaths); err != nil {
				return fmt.Errorf("update shared metadata: %w", err)
			}
		}
		for _, relPath := range scriptPaths {
			fmt.Fprintf(out, "  profiles-shared/%s/%s: updated\n", name, relPath)
		}
	}
	return nil
}

// resetProfileClaudeSettings handles Claude harness settings/symlinks for a profile update.
func resetProfileClaudeSettings(claudeDir, name, style string, dryRun bool, out io.Writer) error {
	if dryRun {
		// Check symlinks.
		for _, link := range []struct{ file, target string }{
			{"CLAUDE.md", filepath.Join("..", "..", "profiles-shared", name, "CLAUDE_AND_AGENTS.md")},
			{"skills", filepath.Join("..", "..", "profiles-shared", name, "skills")},
			{"shared-skill-scripts", filepath.Join("..", "..", "profiles-shared", name, "shared-skill-scripts")},
		} {
			label := fmt.Sprintf("claude-config/%s/%s", name, link.file)
			existing, err := os.Readlink(filepath.Join(claudeDir, link.file))
			if err == nil && existing == link.target {
				fmt.Fprintf(out, "  %s: unchanged (symlink)\n", label)
			} else if err == nil {
				fmt.Fprintf(out, "  %s: updated (symlink)\n", label)
			} else {
				fmt.Fprintf(out, "  %s: added (symlink)\n", label)
			}
		}
		// Check settings.json.
		label := fmt.Sprintf("claude-config/%s/settings.json", name)
		status := compareFileContent(filepath.Join(claudeDir, "settings.json"), config.ClaudeSettingsTemplate(style))
		fmt.Fprintf(out, "  %s: %s\n", label, fileStatusLabel(status))
		return nil
	}
	return ensureClaudeProfileScaffold(claudeDir, name, style, out)
}

// resetProfileCodexSettings handles Codex harness settings/symlinks for a profile update.
func resetProfileCodexSettings(codexDir, name, style string, dryRun bool, out io.Writer) error {
	if dryRun {
		// Check symlinks.
		for _, link := range []struct{ file, target string }{
			{"AGENTS.md", filepath.Join("..", "..", "profiles-shared", name, "CLAUDE_AND_AGENTS.md")},
			{"skills", filepath.Join("..", "..", "profiles-shared", name, "skills")},
			{"shared-skill-scripts", filepath.Join("..", "..", "profiles-shared", name, "shared-skill-scripts")},
		} {
			label := fmt.Sprintf("codex-config/%s/%s", name, link.file)
			existing, err := os.Readlink(filepath.Join(codexDir, link.file))
			if err == nil && existing == link.target {
				fmt.Fprintf(out, "  %s: unchanged (symlink)\n", label)
			} else if err == nil {
				fmt.Fprintf(out, "  %s: updated (symlink)\n", label)
			} else {
				fmt.Fprintf(out, "  %s: added (symlink)\n", label)
			}
		}
		// Check config files.
		for _, f := range []struct {
			file    string
			content string
		}{
			{"config.toml", config.CodexConfigTemplate(style)},
			{"requirements.toml", config.CodexRequirementsTemplate(style)},
		} {
			label := fmt.Sprintf("codex-config/%s/%s", name, f.file)
			status := compareFileContent(filepath.Join(codexDir, f.file), f.content)
			fmt.Fprintf(out, "  %s: %s\n", label, fileStatusLabel(status))
		}
		return nil
	}
	return ensureCodexProfileScaffold(codexDir, name, style, out)
}

func createOrUpdateProfile(h2Dir, name, style, symlinkSharedFrom, harnessType string, requireNew, announce bool, out io.Writer) error {
	sharedDir := filepath.Join(h2Dir, "profiles-shared", name)
	claudeDir := filepath.Join(h2Dir, "claude-config", name)
	codexDir := filepath.Join(h2Dir, "codex-config", name)

	if requireNew {
		switch harnessType {
		case profileHarnessAll:
			if err := ensurePathMissing(sharedDir, "profiles-shared/"+name); err != nil {
				return err
			}
			if err := ensurePathMissing(claudeDir, "claude-config/"+name); err != nil {
				return err
			}
			if err := ensurePathMissing(codexDir, "codex-config/"+name); err != nil {
				return err
			}
		case profileHarnessClaude:
			if err := ensurePathMissing(sharedDir, "profiles-shared/"+name); err != nil {
				return err
			}
			if err := ensurePathMissing(claudeDir, "claude-config/"+name); err != nil {
				return err
			}
		case profileHarnessCodex:
			if err := ensurePathMissing(sharedDir, "profiles-shared/"+name); err != nil {
				return err
			}
			if err := ensurePathMissing(codexDir, "codex-config/"+name); err != nil {
				return err
			}
		}
	}

	if symlinkSharedFrom != "" {
		return createProfileWithSharedSymlink(h2Dir, name, symlinkSharedFrom, harnessType, out)
	}
	return scaffoldProfile(h2Dir, name, style, harnessType, out, announce)
}

func scaffoldProfile(h2Dir, name, style, harnessType string, out io.Writer, announce bool) error {
	sharedDir := filepath.Join(h2Dir, "profiles-shared", name)
	sharedSkillsDir := filepath.Join(sharedDir, "skills")
	sharedScriptsDir := filepath.Join(sharedDir, "shared-skill-scripts")
	claudeDir := filepath.Join(h2Dir, "claude-config", name)
	codexDir := filepath.Join(h2Dir, "codex-config", name)

	if err := os.MkdirAll(sharedSkillsDir, 0o755); err != nil {
		return fmt.Errorf("create shared profile skills dir: %w", err)
	}
	if err := config.WriteSkillsTemplate(style, sharedSkillsDir, false); err != nil {
		return fmt.Errorf("write shared skills: %w", err)
	}
	if err := os.MkdirAll(sharedScriptsDir, 0o755); err != nil {
		return fmt.Errorf("create shared profile shared-skill-scripts dir: %w", err)
	}
	if err := config.WriteSharedSkillScriptsTemplate(style, sharedScriptsDir, false); err != nil {
		return fmt.Errorf("write shared-skill-scripts: %w", err)
	}
	if err := os.WriteFile(filepath.Join(sharedDir, "CLAUDE_AND_AGENTS.md"), []byte(config.InstructionsTemplateWithStyle(style)), 0o644); err != nil {
		return fmt.Errorf("write CLAUDE_AND_AGENTS.md: %w", err)
	}
	if err := config.UpsertContentMeta(sharedDir, style, []string{"CLAUDE_AND_AGENTS.md"}); err != nil {
		return fmt.Errorf("update shared metadata: %w", err)
	}
	managedSkills, err := managedSkillRelativePaths(style)
	if err != nil {
		return err
	}
	if len(managedSkills) > 0 {
		if err := config.UpsertContentMeta(sharedDir, style, managedSkills); err != nil {
			return fmt.Errorf("update shared metadata: %w", err)
		}
	}
	managedScripts, err := managedSharedSkillScriptRelativePaths(style)
	if err != nil {
		return err
	}
	if len(managedScripts) > 0 {
		if err := config.UpsertContentMeta(sharedDir, style, managedScripts); err != nil {
			return fmt.Errorf("update shared metadata: %w", err)
		}
	}
	fmt.Fprintf(out, "  Wrote profiles-shared/%s/CLAUDE_AND_AGENTS.md\n", name)
	fmt.Fprintf(out, "  Wrote profiles-shared/%s/skills/\n", name)
	fmt.Fprintf(out, "  Wrote profiles-shared/%s/shared-skill-scripts/\n", name)

	if harnessType == profileHarnessAll || harnessType == profileHarnessClaude {
		if err := ensureClaudeProfileScaffold(claudeDir, name, style, out); err != nil {
			return err
		}
	}

	if harnessType == profileHarnessAll || harnessType == profileHarnessCodex {
		if err := ensureCodexProfileScaffold(codexDir, name, style, out); err != nil {
			return err
		}
	}

	if announce {
		fmt.Fprintf(out, "Created profile %q\n", name)
	}
	return nil
}

func createProfileWithSharedSymlink(h2Dir, name, sourceProfile, harnessType string, out io.Writer) error {
	srcShared := filepath.Join(h2Dir, "profiles-shared", sourceProfile)
	dstShared := filepath.Join(h2Dir, "profiles-shared", name)

	if _, err := os.Stat(srcShared); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("symlink source profile %q does not exist: missing %s", sourceProfile, srcShared)
		}
		return fmt.Errorf("stat symlink source profile %q: %w", sourceProfile, err)
	}
	if err := os.MkdirAll(filepath.Dir(dstShared), 0o755); err != nil {
		return fmt.Errorf("create profiles dir: %w", err)
	}
	if err := os.Symlink(sourceProfile, dstShared); err != nil {
		return fmt.Errorf("symlink shared profile: %w", err)
	}
	fmt.Fprintf(out, "  Symlinked profiles-shared/%s -> profiles-shared/%s\n", name, sourceProfile)

	if harnessType == profileHarnessAll || harnessType == profileHarnessClaude {
		srcClaude := filepath.Join(h2Dir, "claude-config", sourceProfile)
		dstClaude := filepath.Join(h2Dir, "claude-config", name)
		if _, err := os.Stat(srcClaude); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("symlink source profile %q missing claude config: %s", sourceProfile, srcClaude)
			}
			return fmt.Errorf("stat source claude config: %w", err)
		}
		fmt.Fprintf(out, "  Copying claude-config/%s -> claude-config/%s ...", sourceProfile, name)
		if err := copyPathFiltered(srcClaude, dstClaude, func(_ string, info os.FileInfo) bool {
			return !info.IsDir() && info.Name() == ".claude.json"
		}); err != nil {
			return fmt.Errorf("copy claude profile: %w", err)
		}
		fmt.Fprintln(out, " Done")
		if err := ensureClaudeProfileLinks(dstClaude, name, out); err != nil {
			return err
		}
		fmt.Fprintf(out, "  Copied claude-config/%s -> claude-config/%s\n", sourceProfile, name)
		fmt.Fprintf(out, "  Skipped claude auth file: claude-config/%s/.claude.json\n", sourceProfile)
	}

	if harnessType == profileHarnessAll || harnessType == profileHarnessCodex {
		srcCodex := filepath.Join(h2Dir, "codex-config", sourceProfile)
		dstCodex := filepath.Join(h2Dir, "codex-config", name)
		if _, err := os.Stat(srcCodex); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("symlink source profile %q missing codex config: %s", sourceProfile, srcCodex)
			}
			return fmt.Errorf("stat source codex config: %w", err)
		}
		fmt.Fprintf(out, "  Copying codex-config/%s -> codex-config/%s ...", sourceProfile, name)
		if err := copyPathFiltered(srcCodex, dstCodex, func(_ string, info os.FileInfo) bool {
			return !info.IsDir() && info.Name() == "auth.json"
		}); err != nil {
			return fmt.Errorf("copy codex profile: %w", err)
		}
		fmt.Fprintln(out, " Done")
		if err := ensureCodexProfileLinks(dstCodex, name, out); err != nil {
			return err
		}
		fmt.Fprintf(out, "  Copied codex-config/%s -> codex-config/%s\n", sourceProfile, name)
		fmt.Fprintf(out, "  Skipped codex auth file: codex-config/%s/auth.json\n", sourceProfile)
	}

	fmt.Fprintf(out, "Created profile %q from shared profile symlink to %q\n", name, sourceProfile)
	return nil
}

func copyPathFiltered(src, dst string, skip func(rel string, info os.FileInfo) bool) error {
	return copyPathFilteredRel(src, dst, "", skip)
}

func writeManagedSkillsTemplateNonDestructive(style, targetDir string) error {
	style = strings.TrimSpace(strings.ToLower(style))
	if style == "" {
		style = initStyleOpinionated
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("create skills target dir: %w", err)
	}
	root := fmt.Sprintf("templates/styles/%s/skills", style)
	err := fs.WalkDir(config.Templates, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, root)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return nil
		}
		dst := filepath.Join(targetDir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if filepath.Base(dst) == ".gitkeep" {
			return nil
		}
		data, readErr := config.Templates.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("materialize managed skills template: %w", err)
	}
	return nil
}

func managedSkillRelativePaths(style string) ([]string, error) {
	style = strings.TrimSpace(strings.ToLower(style))
	if style == "" {
		style = initStyleOpinionated
	}
	root := fmt.Sprintf("templates/styles/%s/skills", style)
	paths := []string{}
	err := fs.WalkDir(config.Templates, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(path, root)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" || filepath.Base(rel) == ".gitkeep" {
			return nil
		}
		paths = append(paths, filepath.ToSlash(filepath.Join("skills", filepath.FromSlash(rel))))
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list managed skills: %w", err)
	}
	sort.Strings(paths)
	return paths, nil
}

func writeManagedSharedSkillScriptsNonDestructive(style, targetDir string) error {
	style = strings.TrimSpace(strings.ToLower(style))
	if style == "" {
		style = initStyleOpinionated
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("create shared-skill-scripts target dir: %w", err)
	}
	root := fmt.Sprintf("templates/styles/%s/shared-skill-scripts", style)
	err := fs.WalkDir(config.Templates, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, root)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return nil
		}
		dst := filepath.Join(targetDir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if filepath.Base(dst) == ".gitkeep" {
			return nil
		}
		data, readErr := config.Templates.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("materialize managed shared-skill-scripts template: %w", err)
	}
	return nil
}

func managedSharedSkillScriptRelativePaths(style string) ([]string, error) {
	style = strings.TrimSpace(strings.ToLower(style))
	if style == "" {
		style = initStyleOpinionated
	}
	root := fmt.Sprintf("templates/styles/%s/shared-skill-scripts", style)
	paths := []string{}
	err := fs.WalkDir(config.Templates, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(path, root)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" || filepath.Base(rel) == ".gitkeep" {
			return nil
		}
		paths = append(paths, filepath.ToSlash(filepath.Join("shared-skill-scripts", filepath.FromSlash(rel))))
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list managed shared-skill-scripts: %w", err)
	}
	sort.Strings(paths)
	return paths, nil
}

func copyPathFilteredRel(src, dst, rel string, skip func(rel string, info os.FileInfo) bool) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if skip != nil && skip(rel, info) {
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}
	if !info.IsDir() {
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, info.Mode().Perm())
	}
	if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcChild := filepath.Join(src, entry.Name())
		dstChild := filepath.Join(dst, entry.Name())
		childRel := entry.Name()
		if rel != "" {
			childRel = filepath.Join(rel, entry.Name())
		}
		if err := copyPathFilteredRel(srcChild, dstChild, childRel, skip); err != nil {
			return err
		}
	}
	return nil
}

func ensurePathMissing(path, label string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists; choose a different profile name", label)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check %s: %w", label, err)
	}
	return nil
}

// profileInfo holds a profile name and which harnesses it's available in.
type profileInfo struct {
	Name           string
	Harnesses      []string                         // e.g. ["claude_code", "codex"]
	RateLimitedMap map[string]*config.RateLimitInfo // harness -> rate limit info (nil if not limited)
}

// formatHarnessLabels builds a comma-separated harness list, appending
// red "rate limited until <time>" for any harness that is currently limited.
func formatHarnessLabels(p profileInfo) string {
	labels := make([]string, len(p.Harnesses))
	for i, h := range p.Harnesses {
		if rl, ok := p.RateLimitedMap[h]; ok && rl != nil {
			resetStr := rl.ResetsAt.Local().Format("Jan 2 3:04 PM")
			labels[i] = termstyle.Red(h + " rate limited until " + resetStr)
		} else {
			labels[i] = h
		}
	}
	return strings.Join(labels, ", ")
}

// formatHarnessLabelsPlain is like formatHarnessLabels but without ANSI colors.
// Used in tests.
func formatHarnessLabelsPlain(p profileInfo) string {
	labels := make([]string, len(p.Harnesses))
	for i, h := range p.Harnesses {
		if rl, ok := p.RateLimitedMap[h]; ok && rl != nil {
			resetStr := rl.ResetsAt.Local().Format("Jan 2 3:04 PM")
			labels[i] = h + " rate limited until " + resetStr
		} else {
			labels[i] = h
		}
	}
	return strings.Join(labels, ", ")
}

// discoverProfilesWithHarness scans harness-specific config directories
// (claude-config/, codex-config/) and returns profiles with their harness
// availability. profiles-shared/ is an implementation detail and not scanned.
func discoverProfilesWithHarness(h2Dir string) ([]profileInfo, error) {
	type harnessDir struct {
		harness string
		dir     string
	}
	harnessDirs := []harnessDir{
		{profileHarnessClaude, filepath.Join(h2Dir, "claude-config")},
		{profileHarnessCodex, filepath.Join(h2Dir, "codex-config")},
	}

	// Collect which harnesses each profile appears in, plus rate limit info.
	profileHarnesses := map[string][]string{}
	profileRateLimits := map[string]map[string]*config.RateLimitInfo{}
	for _, hd := range harnessDirs {
		entries, err := os.ReadDir(hd.dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", hd.dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				name := entry.Name()
				profileHarnesses[name] = append(profileHarnesses[name], hd.harness)
				rl := config.IsProfileRateLimited(filepath.Join(hd.dir, name))
				if rl != nil {
					if profileRateLimits[name] == nil {
						profileRateLimits[name] = map[string]*config.RateLimitInfo{}
					}
					profileRateLimits[name][hd.harness] = rl
				}
			}
		}
	}

	// Sort by name.
	names := make([]string, 0, len(profileHarnesses))
	for name := range profileHarnesses {
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([]profileInfo, len(names))
	for i, name := range names {
		result[i] = profileInfo{
			Name:           name,
			Harnesses:      profileHarnesses[name],
			RateLimitedMap: profileRateLimits[name],
		}
	}
	return result, nil
}

// discoverProfiles returns sorted profile names found across all config directories.
// Used by profile update --all which needs to iterate all profile names.
func discoverProfiles(h2Dir string) ([]string, error) {
	infos, err := discoverProfilesWithHarness(h2Dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(infos))
	for i, info := range infos {
		names[i] = info.Name
	}
	return names, nil
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func ensureClaudeProfileScaffold(claudeDir, profileName, style string, out io.Writer) error {
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return fmt.Errorf("create claude profile dir: %w", err)
	}
	if err := ensureClaudeProfileLinks(claudeDir, profileName, out); err != nil {
		return err
	}
	if err := writeGeneratedFile(filepath.Join(claudeDir, "settings.json"), config.ClaudeSettingsTemplate(style), true, out, "claude-config/"+profileName+"/settings.json"); err != nil {
		return err
	}
	if err := config.UpsertContentMeta(claudeDir, style, []string{"settings.json"}); err != nil {
		return fmt.Errorf("update claude metadata: %w", err)
	}
	return nil
}

func ensureCodexProfileScaffold(codexDir, profileName, style string, out io.Writer) error {
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		return fmt.Errorf("create codex profile dir: %w", err)
	}
	if err := ensureCodexProfileLinks(codexDir, profileName, out); err != nil {
		return err
	}
	if err := writeGeneratedFile(filepath.Join(codexDir, "config.toml"), config.CodexConfigTemplate(style), true, out, "codex-config/"+profileName+"/config.toml"); err != nil {
		return err
	}
	if err := writeGeneratedFile(filepath.Join(codexDir, "requirements.toml"), config.CodexRequirementsTemplate(style), true, out, "codex-config/"+profileName+"/requirements.toml"); err != nil {
		return err
	}
	if err := config.UpsertContentMeta(codexDir, style, []string{"config.toml", "requirements.toml"}); err != nil {
		return fmt.Errorf("update codex metadata: %w", err)
	}
	return nil
}

func printContentMeta(out io.Writer, label, dir string) error {
	metaPath := filepath.Join(dir, config.ContentMetaFileName)
	if !pathExists(metaPath) {
		fmt.Fprintf(out, "  Metadata %s: none\n", label)
		return nil
	}
	meta, err := config.ReadContentMeta(dir)
	if err != nil {
		return fmt.Errorf("read metadata for %s: %w", label, err)
	}
	fmt.Fprintf(out, "  Metadata %s: %s\n", label, metaPath)
	keys := make([]string, 0, len(meta.Files))
	for k := range meta.Files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		entry := meta.Files[k]
		style := entry.Style
		if style == "" {
			style = "-"
		}
		fmt.Fprintf(out, "    %s | %s | %s | %s\n", k, entry.H2Version, style, entry.WrittenAt)
	}
	return nil
}

func symlinkStatus(path string) string {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "missing"
		}
		return "error: " + err.Error()
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "no"
	}
	target, err := os.Readlink(path)
	if err != nil {
		return "yes (unreadable target)"
	}
	return "yes -> " + target
}

func ensureClaudeProfileLinks(claudeDir, profileName string, out io.Writer) error {
	mdTarget := filepath.Join("..", "..", "profiles-shared", profileName, "CLAUDE_AND_AGENTS.md")
	skillsTarget := filepath.Join("..", "..", "profiles-shared", profileName, "skills")
	sharedScriptsTarget := filepath.Join("..", "..", "profiles-shared", profileName, "shared-skill-scripts")
	if err := ensureSymlink(filepath.Join(claudeDir, "CLAUDE.md"), mdTarget, true, out, "claude-config/"+profileName+"/CLAUDE.md"); err != nil {
		return err
	}
	if err := ensureSymlink(filepath.Join(claudeDir, "skills"), skillsTarget, true, out, "claude-config/"+profileName+"/skills"); err != nil {
		return err
	}
	if err := ensureSymlink(filepath.Join(claudeDir, "shared-skill-scripts"), sharedScriptsTarget, true, out, "claude-config/"+profileName+"/shared-skill-scripts"); err != nil {
		return err
	}
	return nil
}

func ensureCodexProfileLinks(codexDir, profileName string, out io.Writer) error {
	mdTarget := filepath.Join("..", "..", "profiles-shared", profileName, "CLAUDE_AND_AGENTS.md")
	skillsTarget := filepath.Join("..", "..", "profiles-shared", profileName, "skills")
	sharedScriptsTarget := filepath.Join("..", "..", "profiles-shared", profileName, "shared-skill-scripts")
	if err := ensureSymlink(filepath.Join(codexDir, "AGENTS.md"), mdTarget, true, out, "codex-config/"+profileName+"/AGENTS.md"); err != nil {
		return err
	}
	if err := ensureSymlink(filepath.Join(codexDir, "skills"), skillsTarget, true, out, "codex-config/"+profileName+"/skills"); err != nil {
		return err
	}
	if err := ensureSymlink(filepath.Join(codexDir, "shared-skill-scripts"), sharedScriptsTarget, true, out, "codex-config/"+profileName+"/shared-skill-scripts"); err != nil {
		return err
	}
	return nil
}
