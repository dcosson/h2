package cmd

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"h2/internal/daemon"
	"h2/internal/message"
)

func newLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List running agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			names, err := daemon.ListAgents()
			if err != nil {
				return err
			}
			if len(names) == 0 {
				fmt.Println("No running agents.")
				return nil
			}
			fmt.Printf("\033[1mOpen Sessions:\033[0m\n")
			for _, name := range names {
				info := queryAgent(name)
				if info != nil {
					printAgentLine(info)
				} else {
					// Red X for unresponsive agents.
					fmt.Printf("  \033[31m✗\033[0m %s \033[2m(not responding)\033[0m\n", name)
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
	agents, listErr := daemon.ListAgents()
	if listErr != nil || len(agents) == 0 {
		return fmt.Errorf("cannot connect to agent %q (no running agents)\n\nStart one with: h2 run --name <name> <command>", name)
	}
	return fmt.Errorf("cannot connect to agent %q\n\nAvailable agents: %s", name, strings.Join(agents, ", "))
}

func queryAgent(name string) *message.AgentInfo {
	sockPath := daemon.SocketPath(name)
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
