package cmd

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"h2/internal/session/message"
	"h2/internal/socketdir"
)

func newSendCmd() *cobra.Command {
	var priority string
	var file string

	cmd := &cobra.Command{
		Use:   "send <name> [--priority=normal] [--file=path] [message...]",
		Short: "Send a message to an agent",
		Long:  "Send a message to a running agent. The message body can be provided as arguments or read from a file.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			var body string
			if file != "" {
				data, err := os.ReadFile(file)
				if err != nil {
					return fmt.Errorf("read file: %w", err)
				}
				body = string(data)
			} else if len(args) > 1 {
				body = strings.Join(args[1:], " ")
			} else {
				return fmt.Errorf("message body is required (provide as arguments or --file)")
			}

			if priority == "" {
				priority = "normal"
			}

			from := resolveActor()

			if actor := os.Getenv("H2_ACTOR"); actor != "" && actor == name {
				return fmt.Errorf("cannot send a message to yourself (%s)", name)
			}

			sockPath, findErr := socketdir.Find(name)
			if findErr != nil {
				return agentConnError(name, findErr)
			}
			conn, err := net.Dial("unix", sockPath)
			if err != nil {
				return agentConnError(name, err)
			}
			defer conn.Close()

			if err := message.SendRequest(conn, &message.Request{
				Type:     "send",
				Priority: priority,
				From:     from,
				Body:     body,
			}); err != nil {
				return fmt.Errorf("send request: %w", err)
			}

			resp, err := message.ReadResponse(conn)
			if err != nil {
				return fmt.Errorf("read response: %w", err)
			}
			if !resp.OK {
				return fmt.Errorf("send failed: %s", resp.Error)
			}

			fmt.Println(resp.MessageID)
			return nil
		},
	}

	cmd.Flags().StringVar(&priority, "priority", "normal", "Message priority (interrupt|normal|idle-first|idle)")
	cmd.Flags().StringVar(&file, "file", "", "Read message body from file")

	return cmd
}
