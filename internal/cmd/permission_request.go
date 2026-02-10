package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"h2/internal/config"
	"h2/internal/session/message"
	"h2/internal/socketdir"
)

func newPermissionRequestCmd() *cobra.Command {
	var agentName string

	cmd := &cobra.Command{
		Use:   "permission-request",
		Short: "Handle permission requests via AI reviewer (hook command)",
		Long: `Reads a Claude Code PermissionRequest hook JSON payload from stdin,
reviews it using role-specific AI reviewer instructions, and returns a decision.

Designed to be registered as a PermissionRequest hook in settings.json.
When the AI reviewer returns ASK_USER (or no reviewer is configured),
sends a blocked_permission event to the agent and returns empty output
to fall through to Claude Code's built-in permission dialog.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if agentName == "" {
				agentName = os.Getenv("H2_ACTOR")
			}
			if agentName == "" {
				return fmt.Errorf("--agent is required (or set H2_ACTOR)")
			}

			data, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return fmt.Errorf("read stdin: %w", err)
			}

			// Extract tool info from the request.
			var request permissionInput
			if err := json.Unmarshal(data, &request); err != nil {
				return fmt.Errorf("parse permission request: %w", err)
			}

			// Skip review for non-risky tools.
			switch request.ToolName {
			case "AskUserQuestion", "ExitPlanMode":
				fmt.Fprintln(cmd.OutOrStdout(), "{}")
				return nil
			}

			// Find the session directory and check for reviewer instructions.
			sessionDir := os.Getenv("H2_SESSION_DIR")
			if sessionDir == "" {
				sessionDir = config.SessionDir(agentName)
			}
			reviewerPath := filepath.Join(sessionDir, "permission-reviewer.md")
			reviewerInstructions, err := os.ReadFile(reviewerPath)
			if err != nil {
				// No reviewer instructions — report decision and fall through.
				reportDecision(agentName, request.SessionID, request.ToolName, "ask_user", "no reviewer instructions")
				fmt.Fprintln(cmd.OutOrStdout(), "{}")
				return nil
			}

			// Call claude --print --model haiku with reviewer instructions.
			decision, reason := callReviewer(string(reviewerInstructions), request)

			switch decision {
			case "ALLOW":
				reportDecision(agentName, request.SessionID, request.ToolName, "allow", reason)
				resp := hookResponse{
					HookSpecificOutput: hookDecision{
						HookEventName: "PermissionRequest",
						Decision: decisionPayload{
							Behavior: "allow",
						},
					},
				}
				out, _ := json.Marshal(resp)
				fmt.Fprintln(cmd.OutOrStdout(), string(out))

			case "DENY":
				if reason == "" {
					reason = "Denied by permission reviewer"
				}
				reportDecision(agentName, request.SessionID, request.ToolName, "deny", reason)
				resp := hookResponse{
					HookSpecificOutput: hookDecision{
						HookEventName: "PermissionRequest",
						Decision: decisionPayload{
							Behavior: "deny",
							Message:  reason,
						},
					},
				}
				out, _ := json.Marshal(resp)
				fmt.Fprintln(cmd.OutOrStdout(), string(out))

			default:
				// ASK_USER or unrecognized — report decision and fall through.
				reportDecision(agentName, request.SessionID, request.ToolName, "ask_user", reason)
				fmt.Fprintln(cmd.OutOrStdout(), "{}")
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&agentName, "agent", "", "Agent name (defaults to $H2_ACTOR)")

	return cmd
}

// permissionInput is the JSON payload from a PermissionRequest hook.
type permissionInput struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	SessionID string          `json:"session_id"`
	CWD       string          `json:"cwd"`
}

// hookResponse is the JSON output for a PermissionRequest hook.
type hookResponse struct {
	HookSpecificOutput hookDecision `json:"hookSpecificOutput"`
}

type hookDecision struct {
	HookEventName string         `json:"hookEventName"`
	Decision      decisionPayload `json:"decision"`
}

type decisionPayload struct {
	Behavior string `json:"behavior"`
	Message  string `json:"message,omitempty"`
}

// callReviewer invokes claude --print --model haiku with the reviewer
// instructions and permission request, returning the decision and reason.
func callReviewer(instructions string, req permissionInput) (decision string, reason string) {
	toolInput, _ := json.Marshal(req.ToolInput)
	prompt := fmt.Sprintf(`%s

Permission request:
- Tool: %s
- Input: %s

Respond with exactly two lines.
Line 1: the decision word (ALLOW, DENY, or ASK_USER).
Line 2: a brief reason.
No other text.`, instructions, req.ToolName, string(toolInput))

	cmd := exec.Command("claude", "--print", "--model", "haiku")
	cmd.Stdin = stringReader(prompt)
	cmd.Stderr = nil

	out, err := cmd.Output()
	if err != nil {
		// On error, fall through to user.
		return "ASK_USER", "reviewer error"
	}

	return parseReviewerResponse(string(out))
}

// parseReviewerResponse extracts the decision and reason from the reviewer's output.
func parseReviewerResponse(output string) (string, string) {
	lines := splitLines(output)
	if len(lines) == 0 {
		return "ASK_USER", "empty response"
	}

	decision := lines[0]
	reason := ""
	if len(lines) > 1 {
		reason = lines[1]
	}

	// Normalize decision.
	switch decision {
	case "ALLOW", "DENY", "ASK_USER":
		return decision, reason
	default:
		return "ASK_USER", "unrecognized decision: " + decision
	}
}

// splitLines splits a string into non-empty trimmed lines.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			line := s[start:i]
			// Trim \r for Windows-style line endings.
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			if line != "" {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	return lines
}

// stringReader returns a strings.Reader for the given string.
func stringReader(s string) io.Reader {
	return &stringReaderImpl{data: []byte(s)}
}

type stringReaderImpl struct {
	data []byte
	pos  int
}

func (r *stringReaderImpl) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// reportDecision sends a permission_decision hook event to the agent's socket.
// This reports ALLOW, DENY, and ASK_USER decisions so they appear in the activity log
// and the agent can track blocked state.
func reportDecision(agentName, sessionID, toolName, decision, reason string) {
	sockPath, err := socketdir.Find(agentName)
	if err != nil {
		return // best-effort
	}
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return
	}
	defer conn.Close()

	payload, _ := json.Marshal(map[string]string{
		"hook_event_name": "permission_decision",
		"session_id":      sessionID,
		"tool_name":       toolName,
		"decision":        decision,
		"reason":          reason,
	})

	message.SendRequest(conn, &message.Request{
		Type:      "hook_event",
		EventName: "permission_decision",
		Payload:   json.RawMessage(payload),
	})

	// Read response (don't care about result).
	message.ReadResponse(conn)
}
