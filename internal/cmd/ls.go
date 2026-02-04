package cmd

import (
	"fmt"
	"net"
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
			for _, name := range names {
				info := queryAgent(name)
				if info != nil {
					fmt.Printf("%-20s  cmd=%-20s  uptime=%-8s  queued=%d\n",
						info.Name, info.Command, info.Uptime, info.QueuedCount)
				} else {
					fmt.Printf("%-20s  (not responding)\n", name)
				}
			}
			return nil
		},
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
