package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"h2/internal/config"
	"h2/internal/session"
	"h2/internal/session/agent/harness"
	"h2/internal/socketdir"
	"h2/internal/tmpl"
)

func newRunCmd() *cobra.Command {
	var name string
	var detach bool
	var dryRun bool
	var resume bool
	var roleName string
	var agentType string
	var command string
	var pod string
	var overrides []string
	var varFlags []string

	cmd := &cobra.Command{
		Use:   "run [name] [flags]",
		Short: "Start a new agent",
		Long: `Start a new agent, optionally configured from a role.

By default, uses the "default" role from ~/.h2/roles/default.yaml.

  h2 run                        Use the default role
  h2 run coder-1                Use explicit agent name
  h2 run --role concierge       Use a specific role
  h2 run coder-1 --role concierge
                                Use a specific role with explicit agent name
  h2 run --agent-type claude    Run an agent type without a role
  h2 run --command "vim"        Run an explicit command
  h2 run coder-1 --resume       Resume a previous agent session`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Safety check: when running inside a Claude Code session,
			// require --detach to prevent hijacking the parent's terminal.
			// Skip for --dry-run since it doesn't launch anything.
			if os.Getenv("CLAUDECODE") != "" && !detach && !dryRun {
				return fmt.Errorf("running inside a Claude Code session (CLAUDECODE is set); use --detach to avoid hijacking the parent terminal")
			}

			// Check mutual exclusivity of mode flags.
			modeFlags := 0
			if cmd.Flags().Changed("role") {
				modeFlags++
			}
			if cmd.Flags().Changed("agent-type") {
				modeFlags++
			}
			if cmd.Flags().Changed("command") {
				modeFlags++
			}
			if resume {
				modeFlags++
			}
			if modeFlags > 1 {
				return fmt.Errorf("--role, --agent-type, --command, and --resume are mutually exclusive")
			}

			// Handle --resume mode — it's a separate path from normal run.
			if resume {
				// Reject flags that don't apply to resume.
				for _, flag := range []string{"var", "override", "pod"} {
					if cmd.Flags().Changed(flag) {
						return fmt.Errorf("--%s cannot be used with --resume", flag)
					}
				}
				return runResume(cmd, args, detach, dryRun)
			}

			// Validate pod name if provided.
			if pod != "" {
				if err := config.ValidatePodName(pod); err != nil {
					return err
				}
			}

			var cmdCommand string
			var cmdArgs []string

			// Positional name is supported for role/agent-type modes.
			var positionalName string
			if cmd.Flags().Changed("command") {
				// Run explicit command without a role.
				cmdCommand = command
				cmdArgs = args
			} else {
				if len(args) > 1 {
					return fmt.Errorf("accepts at most one positional name argument, got %d", len(args))
				}
				if len(args) == 1 {
					positionalName = args[0]
				}
			}
			name = positionalName

			if cmd.Flags().Changed("command") {
				// command mode already resolved cmdCommand/cmdArgs above.
			} else if cmd.Flags().Changed("agent-type") {
				// Run agent type without a role.
				cmdCommand = agentType
			} else {
				// Use a role (specified or default).
				if roleName == "" {
					roleName = "default"
				}

				// Parse --var flags into a map.
				vars, err := parseVarFlags(varFlags)
				if err != nil {
					return err
				}

				// Build template context for role rendering.
				rootDir, _ := config.RootDir()
				ctx := &tmpl.Context{
					RoleName:  roleName,
					PodName:   pod,
					H2Dir:     config.ConfigDir(),
					H2RootDir: rootDir,
					Var:       vars,
				}

				// Create name template functions with collision avoidance.
				existingNames := getExistingAgentNames()
				nameFuncs := tmpl.NameFuncs(session.GenerateName, existingNames)

				// Load the role with two-pass agent name resolution.
				var role *config.Role
				if pod != "" {
					// Pod roles use existing flow — pods handle their own name resolution.
					agentName := name
					if agentName == "" {
						if dryRun {
							agentName = dryRunAgentNamePlaceholder
						} else {
							agentName = session.GenerateName()
						}
					}
					ctx.AgentName = agentName
					name = agentName
					role, err = config.LoadPodRoleRendered(roleName, ctx)
				} else {
					rolePath := config.ResolveRolePath(roleName)
					resolvedCLIName := name
					if dryRun && resolvedCLIName == "" {
						// Keep dry-run deterministic and avoid rendering random names.
						resolvedCLIName = dryRunAgentNamePlaceholder
					}
					role, name, err = config.LoadRoleWithNameResolution(
						rolePath, ctx, nameFuncs, resolvedCLIName, session.GenerateName,
					)
				}
				if err != nil {
					if errors.Is(err, os.ErrNotExist) {
						if roleName == "concierge" {
							return fmt.Errorf("concierge role not found; create one with: h2 role create concierge --template concierge")
						}
						if roleName == "default" {
							return fmt.Errorf("no default role found; create one with 'h2 role create default' or specify --role, --agent-type, or --command")
						}
					}
					return fmt.Errorf("load role %q: %w", roleName, err)
				}
				if len(overrides) > 0 {
					if err := config.ApplyOverrides(role, overrides); err != nil {
						return fmt.Errorf("apply overrides: %w", err)
					}
				}
				if dryRun {
					rc, err := resolveAgentConfig(name, role, pod, overrides, nil)
					if err != nil {
						return err
					}
					printDryRun(rc)
					return nil
				}
				return setupAndForkAgent(name, role, detach, pod, overrides)
			}

			// Agent-type or command mode: --dry-run requires a role.
			if dryRun {
				return fmt.Errorf("--dry-run requires a role (use --role or the default role)")
			}

			// Agent-type or command mode: fork without a role.
			if name == "" {
				name = session.GenerateName()
			}
			if err := ensureAgentSocketAvailable(name); err != nil {
				return err
			}
			hcfg := commandHarnessConfig(cmdCommand)

			sessionID := uuid.New().String()

			// Create session dir for command-mode launch.
			sessionDir := config.SessionDir(name)
			if err := os.MkdirAll(sessionDir, 0o755); err != nil {
				return fmt.Errorf("create session dir: %w", err)
			}

			// Build and write RuntimeConfig.
			rc := &config.RuntimeConfig{
				AgentName:        name,
				SessionID:        sessionID,
				HarnessType:      hcfg.HarnessType,
				HarnessConfigDir: hcfg.ConfigDir,
				Command:          cmdCommand,
				Args:             cmdArgs,
				CWD:              func() string { cwd, _ := os.Getwd(); return cwd }(),
				Pod:              pod,
			}
			if err := config.WriteRuntimeConfig(sessionDir, rc); err != nil {
				return fmt.Errorf("write runtime config: %w", err)
			}

			colorHints := detectTerminalHints()

			// Fork a daemon process.
			if err := forkDaemonFunc(sessionDir, session.TerminalHints{
				OscFg:     colorHints.OscFg,
				OscBg:     colorHints.OscBg,
				ColorFGBG: colorHints.ColorFGBG,
				Term:      colorHints.Term,
				ColorTerm: colorHints.ColorTerm,
			}); err != nil {
				return err
			}

			if detach {
				fmt.Fprintf(os.Stderr, "Agent %q started (detached). Use 'h2 attach %s' to connect.\n", name, name)
				return nil
			}

			fmt.Fprintf(os.Stderr, "Agent %q started. Attaching...\n", name)
			return doAttach(name)
		},
	}

	cmd.Flags().BoolVar(&detach, "detach", false, "Don't auto-attach after starting")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show resolved config without launching")
	cmd.Flags().BoolVar(&resume, "resume", false, "Resume a previous agent session")
	cmd.Flags().StringVar(&roleName, "role", "", "Role to use (defaults to 'default')")
	cmd.Flags().StringVar(&agentType, "agent-type", "", "Agent type to run without a role (e.g. claude)")
	cmd.Flags().StringVar(&command, "command", "", "Explicit command to run without a role")
	cmd.Flags().StringVar(&pod, "pod", "", "Pod name for the agent (sets H2_POD env var)")
	cmd.Flags().StringArrayVar(&overrides, "override", nil, "Override role field (key=value, e.g. worktree_enabled=true)")
	cmd.Flags().StringArrayVar(&varFlags, "var", nil, "Set template variable (key=value, repeatable)")

	return cmd
}

// runResume handles the `h2 run <name> --resume` flow. It reads the
// RuntimeConfig from a previous run, checks the agent isn't still alive,
// resolves the harness (to verify resume support), and forks a new daemon
// that passes --resume <session-id> to Claude Code instead of starting fresh.
func runResume(cmd *cobra.Command, args []string, detach bool, dryRun bool) error {
	if len(args) == 0 {
		return fmt.Errorf("--resume requires an agent name (e.g. h2 run <name> --resume)")
	}
	if len(args) > 1 {
		return fmt.Errorf("--resume accepts exactly one agent name, got %d", len(args))
	}
	name := args[0]

	// Read RuntimeConfig from the previous run.
	sessionDir := config.SessionDir(name)
	rc, err := config.ReadRuntimeConfig(sessionDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no session found for agent %q: %w", name, err)
		}
		return fmt.Errorf("session config for agent %q is invalid: %w", name, err)
	}

	// Check socket liveness: error if agent is still running, clean up stale socket.
	if err := ensureAgentSocketAvailable(name); err != nil {
		return fmt.Errorf("agent %q is still running; use 'h2 attach %s' instead", name, name)
	}

	// Resolve harness to check resume support.
	h, err := harness.Resolve(harness.HarnessConfig{
		HarnessType: rc.HarnessType,
		Command:     rc.Command,
	}, nil)
	if err != nil {
		return fmt.Errorf("resolve harness for resume: %w", err)
	}
	if !h.SupportsResume() {
		return fmt.Errorf("agent %q uses harness %q which does not support --resume", name, rc.HarnessType)
	}

	// Ensure the harness config dir exists (e.g. Claude's CLAUDE_CONFIG_DIR).
	if rc.HarnessConfigDir != "" {
		if err := config.EnsureClaudeConfigDir(rc.HarnessConfigDir); err != nil {
			return fmt.Errorf("ensure config dir: %w", err)
		}
	}

	// Determine the harness session ID to resume. HarnessSessionID is the
	// session as known by the harness (e.g. Claude Code). For Claude Code,
	// h2 passes --session-id on first run so SessionID and the harness session
	// are the same; fall back to SessionID when HarnessSessionID is unset.
	harnessResumeID := rc.HarnessSessionID
	if harnessResumeID == "" {
		harnessResumeID = rc.SessionID
	}

	if dryRun {
		// Build the command args that the harness will use.
		resumeArgs := h.BuildCommandArgs(harness.CommandArgsConfig{
			ResumeSessionID: harnessResumeID,
		})
		fmt.Printf("Resume Agent: %s\n", name)
		fmt.Printf("Config File: %s\n", filepath.Join(sessionDir, "session.metadata.json"))
		fmt.Printf("Session ID: %s\n", rc.SessionID)
		fmt.Printf("Harness Session ID: %s\n", rc.HarnessSessionID)
		fmt.Printf("Resume Harness Target: %s\n", harnessResumeID)
		fmt.Printf("Harness: %s\n", rc.HarnessType)
		fmt.Printf("Working Dir: %s\n", rc.CWD)
		if rc.Pod != "" {
			fmt.Printf("Pod: %s\n", rc.Pod)
		}
		fmt.Printf("\nCommand:\n")
		fmt.Printf("%s \\\n", rc.Command)
		for i, arg := range resumeArgs {
			if i < len(resumeArgs)-1 {
				fmt.Printf("  %s \\\n", arg)
			} else {
				fmt.Printf("  %s\n", arg)
			}
		}
		return nil
	}

	origResumeSessionID := rc.ResumeSessionID
	origStartedAt := rc.StartedAt

	rc.ResumeSessionID = harnessResumeID
	rc.StartedAt = "" // will be set by WriteRuntimeConfig

	// Write updated RuntimeConfig before forking.
	if err := config.WriteRuntimeConfig(sessionDir, rc); err != nil {
		return fmt.Errorf("write runtime config for resume: %w", err)
	}

	colorHints := detectTerminalHints()

	// Fork daemon with resume. If fork fails, restore the original config
	// so the session metadata isn't left in a corrupted state.
	if err := forkDaemonFunc(sessionDir, session.TerminalHints{
		OscFg:     colorHints.OscFg,
		OscBg:     colorHints.OscBg,
		ColorFGBG: colorHints.ColorFGBG,
		Term:      colorHints.Term,
		ColorTerm: colorHints.ColorTerm,
	}); err != nil {
		// Restore original config on fork failure.
		rc.ResumeSessionID = origResumeSessionID
		rc.StartedAt = origStartedAt
		_ = config.WriteRuntimeConfig(sessionDir, rc) // best-effort restore
		return err
	}

	if detach {
		fmt.Fprintf(os.Stderr, "Agent %q resumed (detached). Use 'h2 attach %s' to connect.\n", name, name)
		return nil
	}

	fmt.Fprintf(os.Stderr, "Agent %q resumed. Attaching...\n", name)
	return doAttach(name)
}

// getExistingAgentNames returns the names of currently running agents.
func getExistingAgentNames() []string {
	entries, err := socketdir.ListByType(socketdir.TypeAgent)
	if err != nil {
		return nil
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	return names
}
