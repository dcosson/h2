package cmd

import (
	"errors"
	"fmt"
	"os"

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
			var heartbeat session.DaemonHeartbeat

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
			colorHints := detectTerminalHints()

			// Fork a daemon process.
			if err := forkDaemonFunc(session.ForkDaemonOpts{
				Name:             name,
				SessionID:        sessionID,
				Command:          cmdCommand,
				HarnessType:      hcfg.HarnessType,
				HarnessConfigDir: hcfg.ConfigDir,
				Args:             cmdArgs,
				Heartbeat:        heartbeat,
				Pod:              pod,
				OscFg:            colorHints.OscFg,
				OscBg:            colorHints.OscBg,
				ColorFGBG:        colorHints.ColorFGBG,
				Term:             colorHints.Term,
				ColorTerm:        colorHints.ColorTerm,
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

// runResume handles the `h2 run <name> --resume` flow. It reads session
// metadata from a previous run, checks the agent isn't still alive, resolves
// the harness (to verify resume support), and forks a new daemon that passes
// --resume <session-id> to Claude Code instead of starting a fresh session.
func runResume(cmd *cobra.Command, args []string, detach bool, dryRun bool) error {
	if len(args) == 0 {
		return fmt.Errorf("--resume requires an agent name (e.g. h2 run <name> --resume)")
	}
	if len(args) > 1 {
		return fmt.Errorf("--resume accepts exactly one agent name, got %d", len(args))
	}
	name := args[0]

	// Read session metadata from the previous run.
	sessionDir := config.SessionDir(name)
	meta, err := config.ReadSessionMetadata(sessionDir)
	if err != nil {
		return fmt.Errorf("no session found for agent %q: %w", name, err)
	}
	if meta.SessionID == "" {
		return fmt.Errorf("session metadata for %q has no session ID", name)
	}

	// Check socket liveness: error if agent is still running, clean up stale socket.
	if err := ensureAgentSocketAvailable(name); err != nil {
		return fmt.Errorf("agent %q is still running; use 'h2 attach %s' instead", name, name)
	}

	// Resolve harness type to check resume support.
	harnessType := meta.HarnessType
	if harnessType == "" {
		// Fall back to inferring from command for older metadata that didn't store harness_type.
		harnessType = inferHarnessType(meta.Command)
	}
	h, err := harness.Resolve(harness.HarnessConfig{
		HarnessType: harnessType,
		Command:     meta.Command,
	}, nil)
	if err != nil {
		return fmt.Errorf("resolve harness for resume: %w", err)
	}
	if !h.SupportsResume() {
		return fmt.Errorf("agent %q uses harness %q which does not support --resume", name, harnessType)
	}

	// Ensure the harness config dir exists (e.g. Claude's CLAUDE_CONFIG_DIR).
	if meta.ClaudeConfigDir != "" {
		if err := config.EnsureClaudeConfigDir(meta.ClaudeConfigDir); err != nil {
			return fmt.Errorf("ensure config dir: %w", err)
		}
	}

	// Build the command args that the harness will use.
	resumeArgs := h.BuildCommandArgs(harness.CommandArgsConfig{
		ResumeSessionID: meta.SessionID,
	})

	if dryRun {
		fmt.Printf("Resume Agent: %s\n", name)
		fmt.Printf("Previous Session ID: %s\n", meta.SessionID)
		fmt.Printf("Harness: %s\n", harnessType)
		fmt.Printf("Working Dir: %s\n", meta.CWD)
		if meta.Pod != "" {
			fmt.Printf("Pod: %s\n", meta.Pod)
		}
		fmt.Printf("\nCommand:\n")
		fmt.Printf("%s \\\n", meta.Command)
		for i, arg := range resumeArgs {
			if i < len(resumeArgs)-1 {
				fmt.Printf("  %s \\\n", arg)
			} else {
				fmt.Printf("  %s\n", arg)
			}
		}
		return nil
	}

	// Generate a new session ID for this h2 daemon instance.
	sessionID := uuid.New().String()
	colorHints := detectTerminalHints()

	// Fork daemon with resume.
	if err := forkDaemonFunc(session.ForkDaemonOpts{
		Name:             name,
		SessionID:        sessionID,
		ResumeSessionID:  meta.SessionID,
		Command:          meta.Command,
		SessionDir:       sessionDir,
		HarnessType:      harnessType,
		HarnessConfigDir: meta.ClaudeConfigDir,
		CWD:              meta.CWD,
		Pod:              meta.Pod,
		OscFg:            colorHints.OscFg,
		OscBg:            colorHints.OscBg,
		ColorFGBG:        colorHints.ColorFGBG,
		Term:             colorHints.Term,
		ColorTerm:        colorHints.ColorTerm,
	}); err != nil {
		return err
	}

	if detach {
		fmt.Fprintf(os.Stderr, "Agent %q resumed (detached). Use 'h2 attach %s' to connect.\n", name, name)
		return nil
	}

	fmt.Fprintf(os.Stderr, "Agent %q resumed. Attaching...\n", name)
	return doAttach(name)
}

// inferHarnessType maps a command name to a harness type for older metadata
// that didn't store harness_type explicitly.
func inferHarnessType(command string) string {
	switch command {
	case "claude":
		return "claude_code"
	case "codex":
		return "codex"
	default:
		return "generic"
	}
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
