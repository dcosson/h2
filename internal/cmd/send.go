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
	var allowSelf bool

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
				body = cleanLLMEscapes(strings.Join(args[1:], " "))
			} else {
				return fmt.Errorf("message body is required (provide as arguments or --file)")
			}

			if priority == "" {
				priority = "normal"
			}

			from := resolveActor()

			if !allowSelf {
				if actor := os.Getenv("H2_ACTOR"); actor != "" && actor == name {
					return fmt.Errorf("cannot send a message to yourself (%s); use --allow-self to override", name)
				}
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
	cmd.Flags().BoolVar(&allowSelf, "allow-self", false, "Allow sending a message to yourself")

	return cmd
}

// cleanLLMEscapes removes spurious backslash escapes that LLMs insert into
// shell command arguments. For example, Claude Code often writes \! or \?
// in strings even though these characters don't need escaping. We only strip
// backslashes before characters that are never meaningful escape sequences
// in plain text. Loops until stable to handle double-escaped backslashes
// (e.g. \\! → \! → !) which occur when the Bash tool layer escapes
// backslashes before bash processes them.
func cleanLLMEscapes(s string) string {
	for {
		cleaned := stripBackslashPunctuation(s)
		if cleaned == s {
			return cleaned
		}
		s = cleaned
	}
}

func stripBackslashPunctuation(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			next := s[i+1]
			// Strip backslash before punctuation that never needs escaping
			// in plain text messages.
			switch next {
			case '!', '?', '.', ',', ':', ';', ')', '(', ']', '[', '{', '}',
				'#', '+', '-', '=', '|', '>', '<', '~', '^', '@', '&', '%',
				'$', '\'', '"', '`', '/':
				b.WriteByte(next)
				i++ // skip the backslash
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
