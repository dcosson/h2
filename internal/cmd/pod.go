package cmd

import (
	"fmt"
	"net"
	"os"

	"github.com/spf13/cobra"

	"h2/internal/bridgeservice"
	"h2/internal/config"
	"h2/internal/session/message"
	"h2/internal/socketdir"
	"h2/internal/tmpl"
)

func newPodCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pod",
		Short: "Manage agent pods",
	}

	cmd.AddCommand(newPodLaunchCmd())
	cmd.AddCommand(newPodStopCmd())
	cmd.AddCommand(newPodListCmd())
	return cmd
}

func newPodLaunchCmd() *cobra.Command {
	var podName string
	var dryRun bool
	var varFlags []string

	cmd := &cobra.Command{
		Use:   "launch <template>",
		Short: "Launch a pod from a template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			templateName := args[0]

			// Parse --var flags.
			cliVars, err := parseVarFlags(varFlags)
			if err != nil {
				return err
			}

			// Phase 1: Load and render pod template.
			rootDir, _ := config.RootDir()
			podCtx := &tmpl.Context{
				H2Dir:     config.ConfigDir(),
				H2RootDir: rootDir,
				Var:       cliVars,
			}
			pt, err := config.LoadPodTemplateRendered(templateName, podCtx)
			if err != nil {
				return fmt.Errorf("load template %q: %w", templateName, err)
			}

			// Reject unknown CLI variables (typo protection).
			if err := tmpl.ValidateNoUnknownVars(pt.Variables, cliVars); err != nil {
				return fmt.Errorf("pod template %q: %w", templateName, err)
			}

			// Use --pod flag, or template's pod_name, or template file name.
			pod := podName
			if pod == "" {
				pod = pt.PodName
			}
			if pod == "" {
				pod = templateName
			}
			podCtx.PodName = pod

			if err := config.ValidatePodName(pod); err != nil {
				return err
			}

			// Phase 2: Expand count groups.
			expanded, err := config.ExpandPodAgents(pt)
			if err != nil {
				return fmt.Errorf("expand template %q: %w", templateName, err)
			}

			if len(expanded) == 0 {
				return fmt.Errorf("template %q has no agents", templateName)
			}

			if dryRun {
				return podDryRun(templateName, pod, expanded, cliVars)
			}

			// Build a set of already-running agents in this pod.
			running := podRunningAgents(pod)

			var started, skipped int
			for _, agent := range expanded {
				if running[agent.Name] {
					fmt.Fprintf(os.Stderr, "  %s already running\n", agent.Name)
					skipped++
					continue
				}

				roleName := agent.Role
				if roleName == "" {
					roleName = "default"
				}

				// Merge vars: pod template agent vars < CLI vars.
				mergedVars := make(map[string]string)
				for k, v := range agent.Vars {
					mergedVars[k] = v
				}
				for k, v := range cliVars {
					mergedVars[k] = v
				}

				// Build per-agent template context.
				roleCtx := &tmpl.Context{
					AgentName: agent.Name,
					RoleName:  roleName,
					PodName:   pod,
					Index:     agent.Index,
					Count:     agent.Count,
					H2Dir:     config.ConfigDir(),
					H2RootDir: rootDir,
					Var:       mergedVars,
				}

				role, err := config.LoadRoleRendered(roleName, roleCtx)
				if err != nil {
					return fmt.Errorf("load role %q for agent %q: %w", roleName, agent.Name, err)
				}

				// Apply pod-level overrides to the role.
				overrideSlice := config.OverridesToSlice(agent.Overrides)
				if len(overrideSlice) > 0 {
					if err := config.ApplyOverrides(role, overrideSlice); err != nil {
						return fmt.Errorf("apply overrides for agent %q: %w", agent.Name, err)
					}
				}

				if err := setupAndForkAgentQuiet(agent.Name, role, pod, overrideSlice); err != nil {
					return fmt.Errorf("start agent %q: %w", agent.Name, err)
				}
				fmt.Fprintf(os.Stderr, "  %s started\n", agent.Name)
				started++
			}

			// Summary line.
			switch {
			case skipped == 0:
				fmt.Fprintf(os.Stderr, "Pod %q launched with %d agents\n", pod, started)
			case started == 0:
				fmt.Fprintf(os.Stderr, "Pod %q: all %d agents already running\n", pod, skipped)
			default:
				fmt.Fprintf(os.Stderr, "Pod %q: %d started, %d already running\n", pod, started, skipped)
			}

			// Phase 3: Launch bridges (after agents so concierge socket exists).
			if len(pt.Bridges) > 0 {
				bridgeErr := podLaunchBridges(pt.Bridges, pod)
				if bridgeErr != nil {
					return bridgeErr
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&podName, "pod", "", "Override pod name (default: template's pod_name or template name)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show resolved pod config without launching")
	cmd.Flags().StringArrayVar(&varFlags, "var", nil, "Set template variable (key=value, repeatable)")

	return cmd
}

// podLaunchBridges launches bridge daemons defined in a pod template.
// Returns an error only if all bridges fail; partial failures are warnings.
func podLaunchBridges(bridges []config.PodBridge, pod string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config for bridges: %w", err)
	}

	// Build agent name set for concierge validation.
	expanded := podRunningAgents(pod)

	// Validate bridge references.
	bridgeNames := make(map[string]bool)
	for name := range cfg.Bridges {
		bridgeNames[name] = true
	}
	agentNames := make(map[string]bool)
	for name := range expanded {
		agentNames[name] = true
	}
	if err := config.ValidatePodBridges(bridges, bridgeNames, agentNames); err != nil {
		return err
	}

	var bridgesFailed []string
	var bridgesStarted []string

	for _, pb := range bridges {
		// Check if bridge is already running.
		sockPath := socketdir.Path(socketdir.TypeBridge, pb.Bridge)
		if conn, err := net.DialTimeout("unix", sockPath, bridgeservice.ProbeTimeout); err == nil {
			// Bridge is running — check pod ownership.
			if err := message.SendRequest(conn, &message.Request{Type: "status"}); err == nil {
				if resp, err := message.ReadResponse(conn); err == nil && resp.OK && resp.Bridge != nil {
					conn.Close()
					if resp.Bridge.Pod != "" && resp.Bridge.Pod != pod {
						return fmt.Errorf("bridge %q is already owned by pod %q; stop it first or remove from this pod", pb.Bridge, resp.Bridge.Pod)
					}
					// Same pod or standalone — stop and wait for socket cleanup before re-launch.
					if _, err := stopExistingBridgeIfRunning(pb.Bridge); err != nil {
						return fmt.Errorf("stop bridge %q before relaunch: %w", pb.Bridge, err)
					}
				} else {
					conn.Close()
				}
			} else {
				conn.Close()
			}
		}

		if err := bridgeservice.ForkBridge(pb.Bridge, pb.Concierge, pod); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: bridge %q failed to start: %v\n", pb.Bridge, err)
			bridgesFailed = append(bridgesFailed, pb.Bridge)
			continue
		}
		fmt.Fprintf(os.Stderr, "  bridge %s started", pb.Bridge)
		if pb.Concierge != "" {
			fmt.Fprintf(os.Stderr, " (concierge: %s)", pb.Concierge)
		}
		fmt.Fprintln(os.Stderr)
		bridgesStarted = append(bridgesStarted, pb.Bridge)
	}

	if len(bridgesFailed) > 0 {
		fmt.Fprintf(os.Stderr, "\nPartial bridge failure: %d started, %d failed\n", len(bridgesStarted), len(bridgesFailed))
		fmt.Fprintf(os.Stderr, "  Failed: %v\n", bridgesFailed)
		fmt.Fprintf(os.Stderr, "  Started: %v\n", bridgesStarted)
		return fmt.Errorf("%d bridge(s) failed to start", len(bridgesFailed))
	}

	return nil
}

// podDryRun resolves all agent configs in a pod and prints them without launching.
func podDryRun(templateName string, pod string, expanded []config.ExpandedAgent, cliVars map[string]string) error {
	rootDir, _ := config.RootDir()
	var resolved []*ResolvedAgentConfig

	for _, agent := range expanded {
		roleName := agent.Role
		if roleName == "" {
			roleName = "default"
		}

		// Merge vars: pod template agent vars < CLI vars.
		mergedVars := make(map[string]string)
		for k, v := range agent.Vars {
			mergedVars[k] = v
		}
		for k, v := range cliVars {
			mergedVars[k] = v
		}

		// Build per-agent template context.
		roleCtx := &tmpl.Context{
			AgentName: agent.Name,
			RoleName:  roleName,
			PodName:   pod,
			Index:     agent.Index,
			Count:     agent.Count,
			H2Dir:     config.ConfigDir(),
			H2RootDir: rootDir,
			Var:       mergedVars,
		}

		role, err := config.LoadRoleRendered(roleName, roleCtx)
		if err != nil {
			return fmt.Errorf("load role %q for agent %q: %w", roleName, agent.Name, err)
		}

		// Apply pod-level overrides.
		overrideSlice := config.OverridesToSlice(agent.Overrides)
		if len(overrideSlice) > 0 {
			if err := config.ApplyOverrides(role, overrideSlice); err != nil {
				return fmt.Errorf("apply overrides for agent %q: %w", agent.Name, err)
			}
		}

		rc, err := resolveAgentConfig(agent.Name, role, pod, overrideSlice, nil)
		if err != nil {
			return fmt.Errorf("resolve agent %q: %w", agent.Name, err)
		}

		// Annotate with pod-specific info.
		rc.MergedVars = mergedVars
		rc.RoleScope = "global"

		resolved = append(resolved, rc)
	}

	printPodDryRun(templateName, pod, resolved)
	return nil
}

func newPodStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <pod-name>",
		Short: "Stop all agents and bridges in a pod",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			podName := args[0]

			// Stop agents.
			entries, err := socketdir.ListByType(socketdir.TypeAgent)
			if err != nil {
				return err
			}

			agentsStopped := 0
			for _, e := range entries {
				info := queryAgent(e.Path)
				if info == nil || info.Pod != podName {
					continue
				}

				conn, err := net.Dial("unix", e.Path)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: cannot connect to %q: %v\n", e.Name, err)
					continue
				}

				if err := message.SendRequest(conn, &message.Request{Type: "stop"}); err != nil {
					conn.Close()
					fmt.Fprintf(os.Stderr, "Warning: cannot stop %q: %v\n", e.Name, err)
					continue
				}

				resp, err := message.ReadResponse(conn)
				conn.Close()
				if err != nil || !resp.OK {
					fmt.Fprintf(os.Stderr, "Warning: stop failed for %q\n", e.Name)
					continue
				}

				fmt.Printf("Stopped agent %s\n", e.Name)
				agentsStopped++
			}

			// Stop bridges belonging to this pod.
			bridgeEntries, err := socketdir.ListByType(socketdir.TypeBridge)
			if err != nil {
				return err
			}

			bridgesStopped := 0
			for _, e := range bridgeEntries {
				info := queryBridge(e.Path)
				if info == nil || info.Pod != podName {
					continue
				}

				conn, err := net.Dial("unix", e.Path)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: cannot connect to bridge %q: %v\n", e.Name, err)
					continue
				}

				if err := message.SendRequest(conn, &message.Request{Type: "stop"}); err != nil {
					conn.Close()
					fmt.Fprintf(os.Stderr, "Warning: cannot stop bridge %q: %v\n", e.Name, err)
					continue
				}

				resp, err := message.ReadResponse(conn)
				conn.Close()
				if err != nil || !resp.OK {
					fmt.Fprintf(os.Stderr, "Warning: stop failed for bridge %q\n", e.Name)
					continue
				}

				fmt.Printf("Stopped bridge %s\n", e.Name)
				bridgesStopped++
			}

			total := agentsStopped + bridgesStopped
			if total == 0 {
				fmt.Printf("No agents or bridges found in pod %q\n", podName)
			} else {
				fmt.Printf("Stopped %d agents and %d bridges in pod %q\n", agentsStopped, bridgesStopped, podName)
			}
			return nil
		},
	}
}

// podRunningAgents returns a set of agent names currently running in the given pod.
func podRunningAgents(pod string) map[string]bool {
	running := make(map[string]bool)
	entries, err := socketdir.ListByType(socketdir.TypeAgent)
	if err != nil {
		return running
	}
	for _, e := range entries {
		info := queryAgent(e.Path)
		if info != nil && info.Pod == pod {
			running[info.Name] = true
		}
	}
	return running
}

func newPodListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available pod templates",
		RunE: func(cmd *cobra.Command, args []string) error {
			templates, err := config.ListPodTemplates()
			if err != nil {
				return err
			}

			if len(templates) == 0 {
				fmt.Printf("No pod templates found in %s\n", config.PodDir())
				return nil
			}

			w := cmd.OutOrStdout()
			for _, t := range templates {
				name := t.PodName
				if name == "" {
					name = "(unnamed)"
				}
				fmt.Fprintf(w, "%-20s %d agents\n", name, len(t.Agents))
				for _, a := range t.Agents {
					role := a.Role
					if role == "" {
						role = "default"
					}
					fmt.Fprintf(w, "  %-18s (role: %s)\n", a.Name, role)
				}
			}
			return nil
		},
	}
}
