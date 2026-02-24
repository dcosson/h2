package cmd

import (
	"fmt"
	"net"
	"os"

	"github.com/spf13/cobra"

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

				role, err := config.LoadPodRoleRendered(roleName, roleCtx)
				if err != nil {
					return fmt.Errorf("load role %q for agent %q: %w", roleName, agent.Name, err)
				}

				if err := setupAndForkAgentQuiet(agent.Name, role, pod, nil); err != nil {
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
			return nil
		},
	}

	cmd.Flags().StringVar(&podName, "pod", "", "Override pod name (default: template's pod_name or template name)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show resolved pod config without launching")
	cmd.Flags().StringArrayVar(&varFlags, "var", nil, "Set template variable (key=value, repeatable)")

	return cmd
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

		role, err := config.LoadPodRoleRendered(roleName, roleCtx)
		if err != nil {
			return fmt.Errorf("load role %q for agent %q: %w", roleName, agent.Name, err)
		}

		rc, err := resolveAgentConfig(agent.Name, role, pod, nil, nil)
		if err != nil {
			return fmt.Errorf("resolve agent %q: %w", agent.Name, err)
		}

		// Annotate with pod-specific info.
		rc.MergedVars = mergedVars
		if config.IsPodScopedRole(roleName) {
			rc.RoleScope = "pod"
		} else {
			rc.RoleScope = "global"
		}

		resolved = append(resolved, rc)
	}

	printPodDryRun(templateName, pod, resolved)
	return nil
}

func newPodStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <pod-name>",
		Short: "Stop all agents in a pod",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			podName := args[0]

			entries, err := socketdir.ListByType(socketdir.TypeAgent)
			if err != nil {
				return err
			}

			stopped := 0
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

				fmt.Printf("Stopped %s\n", e.Name)
				stopped++
			}

			if stopped == 0 {
				fmt.Printf("No agents found in pod %q\n", podName)
			} else {
				fmt.Printf("Stopped %d agents in pod %q\n", stopped, podName)
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
				fmt.Printf("No pod templates found in %s\n", config.PodTemplatesDir())
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
