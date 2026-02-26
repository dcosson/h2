package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"h2/internal/config"
	"h2/internal/session/message"
	"h2/internal/socketdir"
)

func newHandleHookCmd() *cobra.Command {
	var agentName string
	var forcedPermissionResult string

	cmd := &cobra.Command{
		Use:   "handle-hook",
		Short: "Handle a Claude Code hook event",
		Long: `Reads a Claude Code hook JSON payload from stdin, forwards the event
to the agent's h2 session, and optionally handles PermissionRequest events
with an AI reviewer.

Designed to be registered as the hook command for all Claude Code hook events
in settings.json. Exits 0 with JSON on stdout.`,
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

			// Extract hook_event_name from the JSON payload.
			var envelope struct {
				HookEventName string `json:"hook_event_name"`
			}
			if err := json.Unmarshal(data, &envelope); err != nil {
				return fmt.Errorf("parse hook JSON: %w", err)
			}
			if envelope.HookEventName == "" {
				return fmt.Errorf("hook_event_name not found in payload")
			}
			if forcedPermissionResult != "" && !isValidForcedPermissionResult(forcedPermissionResult) {
				return fmt.Errorf("--force-permission-request-result must be one of: deny, allow, ask_user")
			}

			// Step 1: Always forward the hook event to the agent.
			sendHookEvent(agentName, envelope.HookEventName, data)

			// Step 2: For PermissionRequest, optionally run the permission reviewer.
			if envelope.HookEventName == "PermissionRequest" {
				return handlePermissionRequest(cmd, agentName, data, forcedPermissionResult)
			}

			// All other events: return empty JSON.
			fmt.Fprintln(cmd.OutOrStdout(), "{}")
			return nil
		},
	}

	cmd.Flags().StringVar(&agentName, "agent", "", "Agent name (defaults to $H2_ACTOR)")
	cmd.Flags().StringVar(&forcedPermissionResult, "force-permission-request-result", "", "Force PermissionRequest result: deny, allow, or ask_user (only applies to PermissionRequest hooks)")

	return cmd
}

// sendHookEvent forwards a hook event to the agent's socket. Best-effort:
// errors are silently ignored so the hook command always returns a response
// to Claude Code.
func sendHookEvent(agentName, eventName string, payload []byte) {
	sockPath, err := socketdir.Find(agentName)
	if err != nil {
		return
	}
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return
	}
	defer conn.Close()

	message.SendRequest(conn, &message.Request{
		Type:      "hook_event",
		EventName: eventName,
		Payload:   json.RawMessage(payload),
	})
	message.ReadResponse(conn)
}

// sendPermissionDecision sends a permission_decision event to the agent.
func sendPermissionDecision(agentName, sessionID, toolName, decision, reason string) {
	payload, _ := json.Marshal(map[string]string{
		"hook_event_name": "permission_decision",
		"session_id":      sessionID,
		"tool_name":       toolName,
		"decision":        decision,
		"reason":          reason,
	})

	sockPath, err := socketdir.Find(agentName)
	if err != nil {
		return
	}
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return
	}
	defer conn.Close()

	message.SendRequest(conn, &message.Request{
		Type:      "hook_event",
		EventName: "permission_decision",
		Payload:   json.RawMessage(payload),
	})
	message.ReadResponse(conn)
}

// handlePermissionRequest processes a PermissionRequest hook event.
// The PermissionRequest event has already been forwarded to the agent
// (setting WaitingForPermission state). This function optionally runs
// the AI reviewer and returns a decision to Claude Code.
func handlePermissionRequest(cmd *cobra.Command, agentName string, data []byte, forcedResult string) error {
	var request permissionInput
	if err := json.Unmarshal(data, &request); err != nil {
		// Can't parse — fall through to Claude Code's built-in dialog.
		fmt.Fprintln(cmd.OutOrStdout(), "{}")
		return nil
	}

	if forcedResult != "" {
		return writeForcedPermissionResult(cmd, agentName, request, forcedResult)
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
		// No reviewer — report ask_user and fall through.
		sendPermissionDecision(agentName, request.SessionID, request.ToolName, "ask_user", "no reviewer instructions")
		fmt.Fprintln(cmd.OutOrStdout(), "{}")
		return nil
	}

	// Call claude --print --model haiku with reviewer instructions.
	decision, reason := callReviewer(string(reviewerInstructions), request)

	switch decision {
	case "ALLOW":
		sendPermissionDecision(agentName, request.SessionID, request.ToolName, "allow", reason)
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
		sendPermissionDecision(agentName, request.SessionID, request.ToolName, "deny", reason)
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
		// ASK_USER or unrecognized — fall through to Claude Code's dialog.
		sendPermissionDecision(agentName, request.SessionID, request.ToolName, "ask_user", reason)
		fmt.Fprintln(cmd.OutOrStdout(), "{}")
	}

	return nil
}

func isValidForcedPermissionResult(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "allow", "deny", "ask_user":
		return true
	default:
		return false
	}
}

func writeForcedPermissionResult(cmd *cobra.Command, agentName string, request permissionInput, forcedResult string) error {
	reason := "forced by --force-permission-request-result"

	switch strings.ToLower(strings.TrimSpace(forcedResult)) {
	case "allow":
		sendPermissionDecision(agentName, request.SessionID, request.ToolName, "allow", reason)
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
		return nil
	case "deny":
		sendPermissionDecision(agentName, request.SessionID, request.ToolName, "deny", reason)
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
		return nil
	case "ask_user":
		sendPermissionDecision(agentName, request.SessionID, request.ToolName, "ask_user", reason)
		fmt.Fprintln(cmd.OutOrStdout(), "{}")
		return nil
	default:
		// Guardrail: caller validates this already.
		return fmt.Errorf("--force-permission-request-result must be one of: deny, allow, ask_user")
	}
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
	HookEventName string          `json:"hookEventName"`
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
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stderr = nil

	// Remove OTEL env vars so the child process doesn't send telemetry
	// to the parent's OTEL server.
	cmd.Env = cleanOtelEnv(os.Environ())

	out, err := cmd.Output()
	if err != nil {
		return "ASK_USER", "reviewer error"
	}

	return parseReviewerResponse(string(out))
}

// cleanOtelEnv returns a copy of env with OTEL-related variables removed.
func cleanOtelEnv(env []string) []string {
	otelPrefixes := []string{
		"OTEL_",
		"CLAUDE_CODE_ENABLE_TELEMETRY=",
	}
	cleaned := make([]string, 0, len(env))
	for _, e := range env {
		skip := false
		for _, prefix := range otelPrefixes {
			if strings.HasPrefix(e, prefix) {
				skip = true
				break
			}
		}
		if !skip {
			cleaned = append(cleaned, e)
		}
	}
	return cleaned
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
