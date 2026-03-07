package cmd

import (
	"crypto/rand"
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
	var raw bool
	var expectsResponse bool
	var respondsTo string

	cmd := &cobra.Command{
		Use:   "send [<name>] [--priority=normal] [--file=path] [--raw] [--expects-response] [--responds-to=<id>] [message...]",
		Short: "Send a message to an agent",
		Long: `Send a message to a running agent. The message body can be provided as arguments or read from a file.
With --raw, the body is sent directly to the agent's PTY without the [h2 message from: ...] prefix.
With --expects-response, a reminder trigger is registered on the recipient that fires at idle.
With --responds-to <id>, the trigger is removed from your own daemon (and optionally a response is sent).`,
		Args: cobra.MinimumNArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			// --responds-to mode: target and body are both optional.
			if respondsTo != "" {
				return handleRespondsTo(respondsTo, args, file, priority, allowSelf)
			}

			// Normal send or --expects-response: target is required.
			if len(args) < 1 {
				return fmt.Errorf("target agent name is required")
			}
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

			// Generate trigger ID for expects-response (before sending).
			var triggerID string
			if expectsResponse {
				triggerID = genShortID()
			}

			// Send the message.
			sockPath, findErr := socketdir.Find(name)
			if findErr != nil {
				return agentConnError(name, findErr)
			}
			conn, err := net.Dial("unix", sockPath)
			if err != nil {
				return agentConnError(name, err)
			}

			req := &message.Request{
				Type:     "send",
				Priority: priority,
				From:     from,
				Body:     body,
				Raw:      raw,
			}
			if expectsResponse {
				req.ExpectsResponse = true
				req.ERTriggerID = triggerID
			}

			if err := message.SendRequest(conn, req); err != nil {
				conn.Close()
				return fmt.Errorf("send request: %w", err)
			}

			resp, err := message.ReadResponse(conn)
			conn.Close()
			if err != nil {
				return fmt.Errorf("read response: %w", err)
			}
			if !resp.OK {
				return fmt.Errorf("send failed: %s", resp.Error)
			}

			if !expectsResponse {
				fmt.Println(resp.MessageID)
				return nil
			}

			// Register idle reminder trigger on recipient.
			finalID, triggerErr := registerExpectsResponseTrigger(name, from, triggerID)
			if triggerErr != nil {
				// Message was delivered but tracking failed.
				fmt.Fprintf(os.Stderr, "warning: message delivered but response tracking not created: %v\n", triggerErr)
				fmt.Println(triggerID)
				os.Exit(2) // distinct from exit 1 for total failure
			}

			fmt.Println(finalID)
			return nil
		},
	}

	cmd.Flags().StringVar(&priority, "priority", "normal", "Message priority (interrupt|normal|idle-first|idle)")
	cmd.Flags().StringVar(&file, "file", "", "Read message body from file")
	cmd.Flags().BoolVar(&allowSelf, "allow-self", false, "Allow sending a message to yourself")
	cmd.Flags().BoolVar(&raw, "raw", false, "Send body directly to PTY without [h2 message from: ...] prefix (useful for permission prompts)")
	cmd.Flags().BoolVar(&expectsResponse, "expects-response", false, "Register an idle reminder trigger on the recipient")
	cmd.Flags().StringVar(&respondsTo, "responds-to", "", "Close an expects-response obligation by trigger ID")

	return cmd
}

// registerExpectsResponseTrigger registers an idle reminder trigger on the
// recipient's daemon. Retries once on ID collision. Returns the final trigger
// ID used (which may differ from the input on collision retry).
func registerExpectsResponseTrigger(agentName, sender, triggerID string) (string, error) {
	buildSpec := func(id string) *message.TriggerSpec {
		reminderMsg := fmt.Sprintf(
			"[h2 reminder about message from %s (id: %s)] Respond with: h2 send --responds-to %s %s \"your response\"",
			sender, id, id, sender,
		)
		return &message.TriggerSpec{
			ID:       id,
			Name:     "expects-response-" + id,
			Event:    "state_change",
			State:    "idle",
			Message:  reminderMsg,
			From:     "h2-reminder",
			Priority: "idle",
		}
	}

	spec := buildSpec(triggerID)
	resp, err := sendSocketRequest(agentName, &message.Request{
		Type:    "trigger_add",
		Trigger: spec,
	})
	if err != nil {
		return "", err
	}
	if !resp.OK {
		// Check if collision — retry once with new ID.
		if strings.Contains(resp.Error, "already exists") {
			newID := genShortID()
			spec = buildSpec(newID)
			resp2, err2 := sendSocketRequest(agentName, &message.Request{
				Type:    "trigger_add",
				Trigger: spec,
			})
			if err2 != nil {
				return "", err2
			}
			if !resp2.OK {
				return "", fmt.Errorf("trigger add failed after retry: %s", resp2.Error)
			}
			return newID, nil
		}
		return "", fmt.Errorf("trigger add failed: %s", resp.Error)
	}
	return triggerID, nil
}

// handleRespondsTo handles the --responds-to flow: optionally send a response,
// then remove the trigger from own daemon.
func handleRespondsTo(triggerID string, args []string, file, priority string, allowSelf bool) error {
	var name, body string

	if file != "" {
		if len(args) < 1 {
			return fmt.Errorf("target agent name is required when sending a response body with --file")
		}
		name = args[0]
		data, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("read file: %w", err)
		}
		body = string(data)
	} else if len(args) > 1 {
		name = args[0]
		body = cleanLLMEscapes(strings.Join(args[1:], " "))
	} else if len(args) == 1 {
		// Could be just a target name with no body — treat as close-only.
		// The target is ignored for close-only, but we accept it.
	}
	// len(args) == 0: close-only, no target, no body.

	// If body is present, target must be present.
	if body != "" && name == "" {
		return fmt.Errorf("target agent name is required when sending a response body")
	}

	// Send the response message first (if body present).
	if body != "" {
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
	}

	// Remove the trigger from own daemon (best-effort).
	self := resolveActor()
	selfSock, err := socketdir.Find(self)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not find own daemon socket to remove trigger: %v\n", err)
		return nil
	}
	conn, err := net.Dial("unix", selfSock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not connect to own daemon to remove trigger: %v\n", err)
		return nil
	}
	defer conn.Close()

	if err := message.SendRequest(conn, &message.Request{
		Type:      "trigger_remove",
		TriggerID: triggerID,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: trigger remove request failed: %v\n", err)
		return nil
	}
	resp, err := message.ReadResponse(conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: trigger remove response failed: %v\n", err)
		return nil
	}
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "warning: trigger not found (may have already fired): %s\n", resp.Error)
	}

	return nil
}

// genShortID generates an 8-character hex string for trigger IDs.
func genShortID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("%08x", b)
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
