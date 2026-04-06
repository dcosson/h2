package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"h2/internal/config"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate with external services",
		Long:  "Authenticate with external services like Claude Code",
	}

	cmd.AddCommand(newAuthClaudeCmd())
	return cmd
}

func newAuthClaudeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "claude [config-dir]",
		Short: "Authenticate a Claude config directory",
		Long: `Authenticate a Claude config directory for use with h2 agents.

If no config-dir is provided, authenticates the default shared config:
  ~/.h2/claude-config/default

You can also specify a custom config directory:
  h2 auth claude ~/.h2/claude-config/custom

This will launch an interactive Claude session where you can run /login.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runAuthClaude,
	}
}

func runAuthClaude(cmd *cobra.Command, args []string) error {
	var configDir string
	if len(args) > 0 {
		configDir = args[0]
	} else {
		configDir = config.DefaultClaudeConfigDir()
	}

	// Expand ~ if present
	if len(configDir) > 0 && configDir[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("get home dir: %w", err)
		}
		configDir = home + configDir[1:]
	}

	// Check if already authenticated
	isAuth, err := config.IsClaudeConfigAuthenticated(configDir)
	if err != nil {
		return fmt.Errorf("check authentication: %w", err)
	}

	if isAuth {
		// Check if this profile has a recorded auth error — if so, proceed
		// even though oauthAccount exists (the token may be expired).
		if authErr := config.IsProfileAuthError(configDir); authErr != nil {
			fmt.Printf("⚠ Auth error detected: %s\n", authErr.Message)
			fmt.Println("Proceeding with re-authentication...")
		} else {
			fmt.Printf("✓ Claude config already authenticated: %s\n", configDir)
			return nil
		}
	}

	// Create config directory if it doesn't exist
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	fmt.Printf("Launching Claude to authenticate: %s\n", configDir)
	fmt.Println()
	fmt.Println("In Claude, run:")
	fmt.Println("  /login")
	fmt.Println()
	fmt.Println("After logging in, you can exit Claude with Ctrl+D or /exit")
	fmt.Println()

	// Launch claude with CLAUDE_CONFIG_DIR set
	claudeCmd := exec.Command("claude")
	claudeCmd.Env = append(os.Environ(), fmt.Sprintf("CLAUDE_CONFIG_DIR=%s", configDir))
	claudeCmd.Stdin = os.Stdin
	claudeCmd.Stdout = os.Stdout
	claudeCmd.Stderr = os.Stderr

	if err := claudeCmd.Run(); err != nil {
		return fmt.Errorf("run claude: %w", err)
	}

	// Verify authentication succeeded
	isAuth, err = config.IsClaudeConfigAuthenticated(configDir)
	if err != nil {
		return fmt.Errorf("verify authentication: %w", err)
	}

	if !isAuth {
		fmt.Println()
		fmt.Println("⚠ Authentication not detected. Did you complete /login?")
		fmt.Println("Run 'h2 auth claude' again to retry.")
		return nil
	}

	// Clear any recorded auth error now that re-authentication succeeded.
	_ = config.ClearAuthError(configDir)

	fmt.Println()
	fmt.Printf("✓ Successfully authenticated: %s\n", configDir)
	return nil
}
