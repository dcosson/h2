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
)

const (
	profileHarnessAll    = "all"
	profileHarnessClaude = "claude_code"
	profileHarnessCodex  = "codex"
)

func newProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage account profiles",
	}
	cmd.AddCommand(newProfileCreateCmd())
	cmd.AddCommand(newProfileResetCmd())
	cmd.AddCommand(newProfileListCmd())
	cmd.AddCommand(newProfileShowCmd())
	return cmd
}

func newProfileResetCmd() *cobra.Command {
	var style string
	var includeAuth bool
	var includeSkills bool
	var includeInstructions bool
	var includeSettings bool

	cmd := &cobra.Command{
		Use:   "reset <name>",
		Short: "Reset an account profile to generated defaults",
		Long: `Reset profile content to h2-generated defaults.

By default, reset updates instructions, managed skills, and settings, while
preserving auth files.

Managed skills are updated non-destructively: h2 updates only template-managed
skill files and leaves user-added skills untouched.`,
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
			h2Dir := config.ConfigDir()
			return resetProfile(h2Dir, name, resolvedStyle, includeAuth, includeSkills, includeInstructions, includeSettings, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&style, "style", initStyleOpinionated, "Profile style: minimal, opinionated")
	cmd.Flags().BoolVar(&includeAuth, "include-auth", false, "Include auth files (.claude.json, auth.json) in reset")
	cmd.Flags().BoolVar(&includeSkills, "include-skills", true, "Reset managed shared skills")
	cmd.Flags().BoolVar(&includeInstructions, "include-instructions", true, "Reset shared instructions file")
	cmd.Flags().BoolVar(&includeSettings, "include-settings", true, "Reset harness settings/config files and profile symlinks")
	return cmd
}

func newProfileListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List account profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			h2Dir := config.ConfigDir()
			profiles, err := discoverProfiles(h2Dir)
			if err != nil {
				return err
			}
			if len(profiles) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No profiles found.")
				return nil
			}
			for _, profile := range profiles {
				fmt.Fprintln(cmd.OutOrStdout(), profile)
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
			sharedDir := filepath.Join(h2Dir, "account-profiles-shared", name)
			claudeDir := filepath.Join(h2Dir, "claude-config", name)
			codexDir := filepath.Join(h2Dir, "codex-config", name)

			sharedExists := pathExists(sharedDir)
			claudeExists := pathExists(claudeDir)
			codexExists := pathExists(codexDir)

			if !sharedExists && !claudeExists && !codexExists {
				return fmt.Errorf("profile %q not found", name)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Profile: %s\n", name)
			fmt.Fprintf(out, "  Shared: %s (%s)\n", sharedDir, yesNo(sharedExists))
			fmt.Fprintf(out, "  Claude: %s (%s)\n", claudeDir, yesNo(claudeExists))
			fmt.Fprintf(out, "  Codex:  %s (%s)\n", codexDir, yesNo(codexExists))
			fmt.Fprintf(out, "  Symlink account-profiles-shared/%s: %s\n", name, symlinkStatus(sharedDir))
			fmt.Fprintf(out, "  Symlink claude-config/%s/CLAUDE.md: %s\n", name, symlinkStatus(filepath.Join(claudeDir, "CLAUDE.md")))
			fmt.Fprintf(out, "  Symlink claude-config/%s/skills: %s\n", name, symlinkStatus(filepath.Join(claudeDir, "skills")))
			fmt.Fprintf(out, "  Symlink codex-config/%s/AGENTS.md: %s\n", name, symlinkStatus(filepath.Join(codexDir, "AGENTS.md")))
			fmt.Fprintf(out, "  Symlink codex-config/%s/skills: %s\n", name, symlinkStatus(filepath.Join(codexDir, "skills")))

			if claudeExists {
				auth, err := config.IsClaudeConfigAuthenticated(claudeDir)
				if err != nil {
					fmt.Fprintf(out, "  Claude authenticated: error (%v)\n", err)
				} else {
					fmt.Fprintf(out, "  Claude authenticated: %s\n", yesNo(auth))
				}
			}
			if err := printContentMeta(out, "account-profiles-shared/"+name, sharedDir); err != nil {
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
		Short: "Create a new account profile",
		Long: `Create a named account profile scaffold under the h2 directory.

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

func resetProfile(h2Dir, name, style string, includeAuth, includeSkills, includeInstructions, includeSettings bool, out io.Writer) error {
	sharedDir := filepath.Join(h2Dir, "account-profiles-shared", name)
	sharedSkillsDir := filepath.Join(sharedDir, "skills")
	claudeDir := filepath.Join(h2Dir, "claude-config", name)
	codexDir := filepath.Join(h2Dir, "codex-config", name)

	sharedExists := pathExists(sharedDir)
	claudeExists := pathExists(claudeDir)
	codexExists := pathExists(codexDir)
	if !sharedExists && !claudeExists && !codexExists {
		return fmt.Errorf("profile %q not found", name)
	}

	if includeInstructions || includeSkills {
		if err := os.MkdirAll(sharedDir, 0o755); err != nil {
			return fmt.Errorf("create shared profile dir: %w", err)
		}
	}

	if includeInstructions {
		if err := os.WriteFile(filepath.Join(sharedDir, "CLAUDE_AND_AGENTS.md"), []byte(config.InstructionsTemplateWithStyle(style)), 0o644); err != nil {
			return fmt.Errorf("write CLAUDE_AND_AGENTS.md: %w", err)
		}
		if err := config.UpsertContentMeta(sharedDir, style, []string{"CLAUDE_AND_AGENTS.md"}); err != nil {
			return fmt.Errorf("update shared metadata: %w", err)
		}
		fmt.Fprintf(out, "  Wrote account-profiles-shared/%s/CLAUDE_AND_AGENTS.md\n", name)
	}

	if includeSkills {
		skillPaths, err := managedSkillRelativePaths(style)
		if err != nil {
			return err
		}
		if err := writeManagedSkillsTemplateNonDestructive(style, sharedSkillsDir); err != nil {
			return fmt.Errorf("write shared skills: %w", err)
		}
		if len(skillPaths) > 0 {
			if err := config.UpsertContentMeta(sharedDir, style, skillPaths); err != nil {
				return fmt.Errorf("update shared metadata: %w", err)
			}
		}
		fmt.Fprintf(out, "  Updated managed account-profiles-shared/%s/skills/\n", name)
	}

	if includeSettings {
		if claudeExists {
			if err := ensureClaudeProfileScaffold(claudeDir, name, style, out); err != nil {
				return err
			}
		}
		if codexExists {
			if err := ensureCodexProfileScaffold(codexDir, name, style, out); err != nil {
				return err
			}
		}
	}

	if includeAuth {
		if claudeExists {
			authPath := filepath.Join(claudeDir, ".claude.json")
			if err := os.Remove(authPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove claude auth: %w", err)
			}
			if pathExists(authPath) {
				return fmt.Errorf("remove claude auth: %s still exists", authPath)
			}
			fmt.Fprintf(out, "  Cleared claude-config/%s/.claude.json\n", name)
		}
		if codexExists {
			authPath := filepath.Join(codexDir, "auth.json")
			if err := os.Remove(authPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove codex auth: %w", err)
			}
			if pathExists(authPath) {
				return fmt.Errorf("remove codex auth: %s still exists", authPath)
			}
			fmt.Fprintf(out, "  Cleared codex-config/%s/auth.json\n", name)
		}
	}

	fmt.Fprintf(out, "Reset profile %q\n", name)
	return nil
}

func createOrUpdateProfile(h2Dir, name, style, symlinkSharedFrom, harnessType string, requireNew, announce bool, out io.Writer) error {
	sharedDir := filepath.Join(h2Dir, "account-profiles-shared", name)
	claudeDir := filepath.Join(h2Dir, "claude-config", name)
	codexDir := filepath.Join(h2Dir, "codex-config", name)

	if requireNew {
		switch harnessType {
		case profileHarnessAll:
			if err := ensurePathMissing(sharedDir, "account-profiles-shared/"+name); err != nil {
				return err
			}
			if err := ensurePathMissing(claudeDir, "claude-config/"+name); err != nil {
				return err
			}
			if err := ensurePathMissing(codexDir, "codex-config/"+name); err != nil {
				return err
			}
		case profileHarnessClaude:
			if err := ensurePathMissing(sharedDir, "account-profiles-shared/"+name); err != nil {
				return err
			}
			if err := ensurePathMissing(claudeDir, "claude-config/"+name); err != nil {
				return err
			}
		case profileHarnessCodex:
			if err := ensurePathMissing(sharedDir, "account-profiles-shared/"+name); err != nil {
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
	sharedDir := filepath.Join(h2Dir, "account-profiles-shared", name)
	sharedSkillsDir := filepath.Join(sharedDir, "skills")
	claudeDir := filepath.Join(h2Dir, "claude-config", name)
	codexDir := filepath.Join(h2Dir, "codex-config", name)

	if err := os.MkdirAll(sharedSkillsDir, 0o755); err != nil {
		return fmt.Errorf("create shared profile skills dir: %w", err)
	}
	if err := config.WriteSkillsTemplate(style, sharedSkillsDir, false); err != nil {
		return fmt.Errorf("write shared skills: %w", err)
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
	fmt.Fprintf(out, "  Wrote account-profiles-shared/%s/CLAUDE_AND_AGENTS.md\n", name)
	fmt.Fprintf(out, "  Wrote account-profiles-shared/%s/skills/\n", name)

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
	srcShared := filepath.Join(h2Dir, "account-profiles-shared", sourceProfile)
	dstShared := filepath.Join(h2Dir, "account-profiles-shared", name)

	if _, err := os.Stat(srcShared); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("symlink source profile %q does not exist: missing %s", sourceProfile, srcShared)
		}
		return fmt.Errorf("stat symlink source profile %q: %w", sourceProfile, err)
	}
	if err := os.MkdirAll(filepath.Dir(dstShared), 0o755); err != nil {
		return fmt.Errorf("create account-profiles-shared dir: %w", err)
	}
	if err := os.Symlink(sourceProfile, dstShared); err != nil {
		return fmt.Errorf("symlink shared profile: %w", err)
	}
	fmt.Fprintf(out, "  Symlinked account-profiles-shared/%s -> account-profiles-shared/%s\n", name, sourceProfile)

	if harnessType == profileHarnessAll || harnessType == profileHarnessClaude {
		srcClaude := filepath.Join(h2Dir, "claude-config", sourceProfile)
		dstClaude := filepath.Join(h2Dir, "claude-config", name)
		if _, err := os.Stat(srcClaude); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("symlink source profile %q missing claude config: %s", sourceProfile, srcClaude)
			}
			return fmt.Errorf("stat source claude config: %w", err)
		}
		if err := copyPathFiltered(srcClaude, dstClaude, func(_ string, info os.FileInfo) bool {
			return !info.IsDir() && info.Name() == ".claude.json"
		}); err != nil {
			return fmt.Errorf("copy claude profile: %w", err)
		}
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
		if err := copyPathFiltered(srcCodex, dstCodex, func(_ string, info os.FileInfo) bool {
			return !info.IsDir() && info.Name() == "auth.json"
		}); err != nil {
			return fmt.Errorf("copy codex profile: %w", err)
		}
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

func discoverProfiles(h2Dir string) ([]string, error) {
	seen := map[string]struct{}{}
	for _, root := range []string{
		filepath.Join(h2Dir, "account-profiles-shared"),
		filepath.Join(h2Dir, "claude-config"),
		filepath.Join(h2Dir, "codex-config"),
	} {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", root, err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				seen[entry.Name()] = struct{}{}
			}
		}
	}
	profiles := make([]string, 0, len(seen))
	for profile := range seen {
		profiles = append(profiles, profile)
	}
	sort.Strings(profiles)
	return profiles, nil
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
	mdTarget := filepath.Join("..", "..", "account-profiles-shared", profileName, "CLAUDE_AND_AGENTS.md")
	skillsTarget := filepath.Join("..", "..", "account-profiles-shared", profileName, "skills")
	if err := ensureSymlink(filepath.Join(claudeDir, "CLAUDE.md"), mdTarget, true, out, "claude-config/"+profileName+"/CLAUDE.md"); err != nil {
		return err
	}
	if err := ensureSymlink(filepath.Join(claudeDir, "skills"), skillsTarget, true, out, "claude-config/"+profileName+"/skills"); err != nil {
		return err
	}
	return nil
}

func ensureCodexProfileLinks(codexDir, profileName string, out io.Writer) error {
	mdTarget := filepath.Join("..", "..", "account-profiles-shared", profileName, "CLAUDE_AND_AGENTS.md")
	skillsTarget := filepath.Join("..", "..", "account-profiles-shared", profileName, "skills")
	if err := ensureSymlink(filepath.Join(codexDir, "AGENTS.md"), mdTarget, true, out, "codex-config/"+profileName+"/AGENTS.md"); err != nil {
		return err
	}
	if err := ensureSymlink(filepath.Join(codexDir, "skills"), skillsTarget, true, out, "codex-config/"+profileName+"/skills"); err != nil {
		return err
	}
	return nil
}
