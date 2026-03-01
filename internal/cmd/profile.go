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
	cmd.AddCommand(newProfileListCmd())
	cmd.AddCommand(newProfileShowCmd())
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

			if claudeExists {
				auth, err := config.IsClaudeConfigAuthenticated(claudeDir)
				if err != nil {
					fmt.Fprintf(out, "  Claude authenticated: error (%v)\n", err)
				} else {
					fmt.Fprintf(out, "  Claude authenticated: %s\n", yesNo(auth))
				}
			}
			return nil
		},
	}
}

func newProfileCreateCmd() *cobra.Command {
	var style string
	var copyFrom string
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

Use --copy to clone from an existing profile instead of generating from templates.
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

			copyFrom = strings.TrimSpace(copyFrom)
			symlinkSharedFrom = strings.TrimSpace(symlinkSharedFrom)
			if copyFrom != "" && symlinkSharedFrom != "" {
				return fmt.Errorf("--copy and --symlink-shared are mutually exclusive")
			}
			if copyFrom == name {
				return fmt.Errorf("--copy source must be different from target profile name")
			}
			if symlinkSharedFrom == name {
				return fmt.Errorf("--symlink-shared source must be different from target profile name")
			}
			if copyFrom != "" && strings.ContainsRune(copyFrom, os.PathSeparator) {
				return fmt.Errorf("--copy source must not contain path separators: %q", copyFrom)
			}
			if symlinkSharedFrom != "" && strings.ContainsRune(symlinkSharedFrom, os.PathSeparator) {
				return fmt.Errorf("--symlink-shared source must not contain path separators: %q", symlinkSharedFrom)
			}

			h2Dir := config.ConfigDir()
			return createProfile(h2Dir, name, resolvedStyle, copyFrom, symlinkSharedFrom, harnessType, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&style, "style", initStyleOpinionated, "Profile style: minimal, opinionated")
	cmd.Flags().StringVar(&copyFrom, "copy", "", "Copy from an existing profile name")
	cmd.Flags().StringVar(&symlinkSharedFrom, "symlink-shared", "", "Symlink shared profile content from an existing profile name")
	cmd.Flags().StringVar(&harnessType, "agent-harness", profileHarnessAll, "Harness profile to create: claude_code, codex, all")
	return cmd
}

func createProfile(h2Dir, name, style, copyFrom, symlinkSharedFrom, harnessType string, out io.Writer) error {
	return createOrUpdateProfile(h2Dir, name, style, copyFrom, symlinkSharedFrom, harnessType, true, true, out)
}

func createOrUpdateProfile(h2Dir, name, style, copyFrom, symlinkSharedFrom, harnessType string, requireNew, announce bool, out io.Writer) error {
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

	if copyFrom != "" {
		return copyProfile(h2Dir, name, copyFrom, harnessType, out)
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

func copyProfile(h2Dir, name, copyFrom, harnessType string, out io.Writer) error {
	srcShared := filepath.Join(h2Dir, "account-profiles-shared", copyFrom)
	dstShared := filepath.Join(h2Dir, "account-profiles-shared", name)

	if _, err := os.Stat(srcShared); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("copy source profile %q does not exist: missing %s", copyFrom, srcShared)
		}
		return fmt.Errorf("stat copy source profile %q: %w", copyFrom, err)
	}
	if err := copyPathFiltered(srcShared, dstShared, nil); err != nil {
		return fmt.Errorf("copy shared profile: %w", err)
	}
	fmt.Fprintf(out, "  Copied account-profiles-shared/%s -> account-profiles-shared/%s\n", copyFrom, name)

	if harnessType == profileHarnessAll || harnessType == profileHarnessClaude {
		srcClaude := filepath.Join(h2Dir, "claude-config", copyFrom)
		dstClaude := filepath.Join(h2Dir, "claude-config", name)
		if _, err := os.Stat(srcClaude); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("copy source profile %q missing claude config: %s", copyFrom, srcClaude)
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
		fmt.Fprintf(out, "  Copied claude-config/%s -> claude-config/%s\n", copyFrom, name)
		fmt.Fprintf(out, "  Skipped claude auth file: claude-config/%s/.claude.json\n", copyFrom)
	}

	if harnessType == profileHarnessAll || harnessType == profileHarnessCodex {
		srcCodex := filepath.Join(h2Dir, "codex-config", copyFrom)
		dstCodex := filepath.Join(h2Dir, "codex-config", name)
		if _, err := os.Stat(srcCodex); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("copy source profile %q missing codex config: %s", copyFrom, srcCodex)
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
		fmt.Fprintf(out, "  Copied codex-config/%s -> codex-config/%s\n", copyFrom, name)
		fmt.Fprintf(out, "  Skipped codex auth file: codex-config/%s/auth.json\n", copyFrom)
	}

	fmt.Fprintf(out, "Created profile %q from %q\n", name, copyFrom)
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
	return nil
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
