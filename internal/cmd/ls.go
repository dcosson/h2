package cmd

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"h2/internal/session/message"
	"h2/internal/socketdir"
)

func newLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List running agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := socketdir.List()
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				fmt.Println("No running agents.")
				return nil
			}

			// Group by type.
			var agents, bridges []socketdir.Entry
			for _, e := range entries {
				switch e.Type {
				case socketdir.TypeAgent:
					agents = append(agents, e)
				case socketdir.TypeBridge:
					bridges = append(bridges, e)
				}
			}

			if len(bridges) > 0 {
				fmt.Printf("\033[1mBridges:\033[0m\n")
				for _, e := range bridges {
					fmt.Printf("  \033[32m●\033[0m %s\n", e.Name)
				}
			}

			if len(agents) > 0 {
				fmt.Printf("\033[1mAgents:\033[0m\n")
				for _, e := range agents {
					info := queryAgent(e.Path)
					if info != nil {
						printAgentLine(info)
					} else {
						fmt.Printf("  \033[31m✗\033[0m %s \033[2m(not responding)\033[0m\n", e.Name)
					}
				}
			}
			return nil
		},
	}
}

func printAgentLine(info *message.AgentInfo) {
	// Pick symbol and color based on state.
	var symbol, stateColor string
	switch info.State {
	case "active":
		symbol = "\033[32m●\033[0m" // green dot
		stateColor = "\033[32m"     // green
	case "idle":
		symbol = "\033[33m○\033[0m" // yellow circle
		stateColor = "\033[33m"     // yellow
	case "exited":
		symbol = "\033[31m●\033[0m" // red dot
		stateColor = "\033[31m"     // red
	default:
		symbol = "\033[37m○\033[0m"
		stateColor = "\033[37m"
	}

	// State label with duration.
	var stateLabel string
	if info.State != "" {
		stateLabel = fmt.Sprintf("%s%s %s\033[0m", stateColor, info.State, info.StateDuration)
	} else {
		stateLabel = fmt.Sprintf("\033[2mup %s\033[0m", info.Uptime)
	}

	// Queued suffix — only show if there are queued messages.
	queued := ""
	if info.QueuedCount > 0 {
		queued = fmt.Sprintf(", \033[36m%d queued\033[0m", info.QueuedCount)
	}

	if info.State != "" {
		fmt.Printf("  %s %s \033[2m%s\033[0m — %s, up %s%s\n",
			symbol, info.Name, info.Command, stateLabel, info.Uptime, queued)
	} else {
		fmt.Printf("  %s %s \033[2m%s\033[0m — %s%s\n",
			symbol, info.Name, info.Command, stateLabel, queued)
	}
}

// newLsAlias returns a hidden "ls" command that delegates to "list".
func newLsAlias(listCmd *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:    "ls",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return listCmd.RunE(listCmd, args)
		},
	}
}

// agentConnError returns an error for a failed agent connection that includes
// the list of available agents.
func agentConnError(name string, err error) error {
	agents, listErr := socketdir.ListByType(socketdir.TypeAgent)
	if listErr != nil || len(agents) == 0 {
		return fmt.Errorf("cannot connect to agent %q (no running agents)\n\nStart one with: h2 run --name <name> <command>", name)
	}
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name
	}
	return fmt.Errorf("cannot connect to agent %q\n\nAvailable agents: %s", name, strings.Join(names, ", "))
}

// queryAgent connects to a socket path and queries agent status.
func queryAgent(sockPath string) *message.AgentInfo {
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return nil
	}
	defer conn.Close()

	if err := message.SendRequest(conn, &message.Request{Type: "status"}); err != nil {
		return nil
	}

	resp, err := message.ReadResponse(conn)
	if err != nil || !resp.OK {
		return nil
	}
	return resp.Agent
}
