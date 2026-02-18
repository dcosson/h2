package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"h2/internal/sandbox"
)

func newSandboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Manage isolated h2 environments for benchmarks and testing",
	}

	cmd.AddCommand(
		newSandboxCreateCmd(),
		newSandboxListCmd(),
		newSandboxResetCmd(),
		newSandboxDestroyCmd(),
		newSandboxShellCmd(),
		newSandboxExecCmd(),
	)
	return cmd
}

func newSandboxCreateCmd() *cobra.Command {
	var preset string
	var authFrom string

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new sandbox environment",
		Long: `Create a new isolated h2 environment with the given name.

The sandbox is stored at ~/.h2/sandboxes/<name>/ and contains its own
roles, sessions, sockets, and settings. Auth credentials are copied
from the default h2 config (or --auth-from) and survive resets.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sb, err := sandbox.Create(args[0], preset, authFrom, "")
			if err != nil {
				return err
			}
			fmt.Printf("Created sandbox %q (preset: %s) at %s\n", sb.Name, sb.Preset, sb.Dir)
			return nil
		},
	}

	cmd.Flags().StringVar(&preset, "preset", "hooks", fmt.Sprintf("preset to use (%s)", strings.Join(sandbox.ValidPresets, ", ")))
	cmd.Flags().StringVar(&authFrom, "auth-from", "", "directory to copy .claude.json from (default: h2 default config)")

	return cmd
}

func newSandboxListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all sandboxes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			infos, err := sandbox.List("")
			if err != nil {
				return err
			}

			if len(infos) == 0 {
				fmt.Println("No sandboxes found.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tPRESET\tAUTH\tCREATED")
			for _, info := range infos {
				auth := "none"
				if info.HasAuth {
					auth = "valid"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", info.Name, info.Preset, auth, info.CreatedAt)
			}
			w.Flush()
			return nil
		},
	}
}

func newSandboxResetCmd() *cobra.Command {
	var preset string

	cmd := &cobra.Command{
		Use:   "reset <name>",
		Short: "Reset a sandbox, preserving auth credentials",
		Long: `Wipe all sandbox state except .claude.json and reapply the preset.

Stops any running agents, deletes sessions/sockets/worktrees, and
regenerates roles, settings.json, and CLAUDE.md from the preset.
Optionally changes the preset with --preset.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := sandbox.Reset(args[0], preset, ""); err != nil {
				return err
			}
			fmt.Printf("Reset sandbox %q.\n", args[0])
			return nil
		},
	}

	cmd.Flags().StringVar(&preset, "preset", "", fmt.Sprintf("change preset (%s)", strings.Join(sandbox.ValidPresets, ", ")))

	return cmd
}

func newSandboxDestroyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "destroy <name>",
		Short: "Remove a sandbox and all its data",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := sandbox.Destroy(args[0], ""); err != nil {
				return err
			}
			fmt.Printf("Destroyed sandbox %q.\n", args[0])
			return nil
		},
	}
}

func newSandboxShellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell <name>",
		Short: "Open a shell with H2_DIR set to the sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sb, err := sandbox.Get(args[0], "")
			if err != nil {
				return err
			}

			shell := os.Getenv("SHELL")
			if shell == "" {
				shell = "/bin/sh"
			}

			c := exec.Command(shell)
			c.Env = sandboxShellEnv(sb.Dir)
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr

			fmt.Printf("Entering sandbox %q (H2_DIR=%s)\n", sb.Name, sb.Dir)
			fmt.Println("Type 'exit' to leave the sandbox shell.")
			return c.Run()
		},
	}
}

func newSandboxExecCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exec <name> -- <command...>",
		Short: "Run a command with H2_DIR set to the sandbox",
		Long: `Run a command with H2_DIR pointing at the sandbox directory.

Stdout and stderr from the command are passed through separately.
Use -- to separate sandbox arguments from the command:

  h2 sandbox exec bench-1 -- h2 run --role default --name worker
  h2 sandbox exec bench-1 -- h2 send worker "solve this issue..."`,
		Args:               cobra.MinimumNArgs(2),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse: <name> [-- <command...>]
			name, cmdArgs := parseSandboxExecArgs(args)
			if len(cmdArgs) == 0 {
				return fmt.Errorf("no command specified; use: h2 sandbox exec <name> -- <command...>")
			}

			sb, err := sandbox.Get(name, "")
			if err != nil {
				return err
			}

			c := exec.Command(cmdArgs[0], cmdArgs[1:]...)
			c.Env = sandboxShellEnv(sb.Dir)
			c.Stdin = os.Stdin
			c.Stdout = cmd.OutOrStdout()
			c.Stderr = cmd.ErrOrStderr()
			return c.Run()
		},
	}
}

// parseSandboxExecArgs splits args into sandbox name and command.
// Handles both "name -- cmd args" and "name cmd args" forms.
func parseSandboxExecArgs(args []string) (name string, cmdArgs []string) {
	if len(args) == 0 {
		return "", nil
	}
	name = args[0]
	rest := args[1:]

	// If the first arg after name is "--", skip it.
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	return name, rest
}

// sandboxShellEnv returns the current environment with H2_DIR set to the sandbox.
func sandboxShellEnv(dir string) []string {
	env := os.Environ()
	found := false
	for i, e := range env {
		if strings.HasPrefix(e, "H2_DIR=") {
			env[i] = "H2_DIR=" + dir
			found = true
			break
		}
	}
	if !found {
		env = append(env, "H2_DIR="+dir)
	}
	return env
}
