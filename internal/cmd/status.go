package cmd

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/spf13/cobra"

	"h2/internal/session/message"
	"h2/internal/socketdir"
)

func newStatusCmd() *cobra.Command {
	var idleFlag bool
	var podFlag string

	cmd := &cobra.Command{
		Use:   "status [name]",
		Short: "Show agent status",
		Long: `Query agent status.

Without flags, queries a single agent by name and prints JSON.

With --idle, checks whether all agents are idle and prints "idle" or "active".
This is designed for machine consumption (e.g. benchmark runners polling for completion).
Use --pod to check only agents in a specific pod.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if idleFlag {
				return runStatusIdle(cmd, podFlag)
			}

			if len(args) == 0 {
				return fmt.Errorf("agent name required (or use --idle to check all agents)")
			}
			return runStatusSingle(cmd, args[0])
		},
	}

	cmd.Flags().BoolVar(&idleFlag, "idle", false, "Check if all agents are idle (prints 'idle' or 'active')")
	cmd.Flags().StringVar(&podFlag, "pod", "", "Filter by pod name (only with --idle)")

	return cmd
}

// runStatusSingle queries a single agent and prints JSON.
func runStatusSingle(cmd *cobra.Command, name string) error {
	sockPath, err := socketdir.Find(name)
	if err != nil {
		return agentConnError(name, err)
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return agentConnError(name, err)
	}
	defer conn.Close()

	if err := message.SendRequest(conn, &message.Request{Type: "status"}); err != nil {
		return fmt.Errorf("send request: %w", err)
	}

	resp, err := message.ReadResponse(conn)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("status failed: %s", resp.Error)
	}
	if resp.Agent == nil {
		return fmt.Errorf("no agent info in response")
	}

	out, err := json.MarshalIndent(resp.Agent, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(out))
	return nil
}

// runStatusIdle checks if all agents are idle and prints "idle" or "active".
// An agent is considered idle if its state is "idle" or "exited".
// If no agents are running, prints "idle".
func runStatusIdle(cmd *cobra.Command, podFilter string) error {
	entries, err := socketdir.ListByType(socketdir.TypeAgent)
	if err != nil {
		return fmt.Errorf("list agents: %w", err)
	}

	var agents []*message.AgentInfo
	for _, e := range entries {
		info := queryAgent(e.Path)
		if info == nil {
			// Unresponsive agent — can't confirm it's idle.
			fmt.Fprintln(cmd.OutOrStdout(), "active")
			return nil
		}
		agents = append(agents, info)
	}

	if checkAgentsIdle(agents, podFilter) {
		fmt.Fprintln(cmd.OutOrStdout(), "idle")
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "active")
	}
	return nil
}

// checkAgentsIdle determines if all agents matching the pod filter are idle.
// Returns true if there are no matching agents or all matching agents are idle/exited.
func checkAgentsIdle(agents []*message.AgentInfo, podFilter string) bool {
	for _, info := range agents {
		if podFilter != "" && info.Pod != podFilter {
			continue
		}
		if !isIdleState(info.State) {
			return false
		}
	}
	return true
}

// isIdleState returns true if the agent state indicates the agent is not
// actively working. Both "idle" and "exited" agents are considered idle.
func isIdleState(state string) bool {
	return state == "idle" || state == "exited"
}
