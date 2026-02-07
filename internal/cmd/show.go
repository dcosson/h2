package cmd

import (
	"fmt"
	"net"
	"time"

	"github.com/spf13/cobra"

	"h2/internal/session"
	"h2/internal/session/message"
)

func newShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <message-id>",
		Short: "Show message delivery status",
		Long:  "Query all running agents for a message by ID and show its status.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			messageID := args[0]

			names, err := session.ListAgents()
			if err != nil {
				return err
			}

			for _, name := range names {
				info := queryMessage(name, messageID)
				if info != nil {
					fmt.Printf("ID:          %s\n", info.ID)
					fmt.Printf("From:        %s\n", info.From)
					fmt.Printf("Priority:    %s\n", info.Priority)
					fmt.Printf("Status:      %s\n", info.Status)
					fmt.Printf("File:        %s\n", info.FilePath)
					fmt.Printf("Created:     %s\n", info.CreatedAt)
					if info.DeliveredAt != "" {
						fmt.Printf("Delivered:   %s\n", info.DeliveredAt)
					}
					return nil
				}
			}

			return fmt.Errorf("message %s not found", messageID)
		},
	}
}

func queryMessage(agentName, messageID string) *message.MessageInfo {
	sockPath := session.SocketPath(agentName)
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return nil
	}
	defer conn.Close()

	if err := message.SendRequest(conn, &message.Request{
		Type:      "show",
		MessageID: messageID,
	}); err != nil {
		return nil
	}

	resp, err := message.ReadResponse(conn)
	if err != nil || !resp.OK {
		return nil
	}
	return resp.Message
}
