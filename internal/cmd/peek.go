package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"h2/internal/config"
	"h2/internal/session/agent/monitor"
	"h2/internal/session/agent/shared/eventstore"
)

func newPeekCmd() *cobra.Command {
	var numLines int
	var messageChars int
	var summarize bool

	cmd := &cobra.Command{
		Use:   "peek <name>",
		Short: "View recent agent activity",
		Long: `Read the last N events from an agent's event store and format them
as a concise activity log. Use --summarize to get a one-sentence summary
via Claude haiku.

  h2 peek concierge              Show recent activity for an agent
  h2 peek concierge --summarize  Summarize with haiku
  h2 peek concierge -n 500       Show last 500 events (default 150)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			sessionDir := config.SessionDir(name)

			lines, err := formatEventLog(sessionDir, numLines, messageChars)
			if err != nil {
				return fmt.Errorf("read events for %q: %w (is the agent running?)", name, err)
			}

			if len(lines) == 0 {
				fmt.Println("(no activity found)")
				return nil
			}

			output := strings.Join(lines, "\n")
			if summarize {
				return summarizeWithHaiku(output)
			}
			fmt.Println(output)
			return nil
		},
	}

	cmd.Flags().IntVarP(&numLines, "num-lines", "n", 150, "Number of events to read from the end")
	cmd.Flags().IntVar(&messageChars, "message-chars", 500, "Max characters for message text (0 for no limit)")
	cmd.Flags().BoolVar(&summarize, "summarize", false, "Summarize activity with Claude haiku")

	return cmd
}

// formatEventLog reads events from the event store and formats them.
func formatEventLog(sessionDir string, numLines int, messageChars int) ([]string, error) {
	events, err := eventstore.ReadEventsFile(sessionDir)
	if err != nil {
		return nil, err
	}

	// Take the last N events.
	start := 0
	if len(events) > numLines {
		start = len(events) - numLines
	}
	recent := events[start:]

	now := time.Now()
	var output []string
	for _, ev := range recent {
		formatted := formatAgentEvent(ev, now, messageChars)
		if formatted != "" {
			output = append(output, formatted)
		}
	}
	return output, nil
}

// formatAgentEvent formats a single AgentEvent into a human-readable line.
// Returns "" for event types that are not interesting to display.
func formatAgentEvent(ev monitor.AgentEvent, now time.Time, messageChars int) string {
	ts := formatRelativeDuration(now.Sub(ev.Timestamp))

	switch ev.Type {
	case monitor.EventAgentMessage:
		data, ok := ev.Data.(monitor.AgentMessageData)
		if !ok || data.Content == "" {
			return ""
		}
		text := strings.TrimSpace(data.Content)
		firstLine := strings.SplitN(text, "\n", 2)[0]
		if messageChars > 0 && len(firstLine) > messageChars {
			firstLine = firstLine[:messageChars-3] + "..."
		}
		return fmt.Sprintf("[%s] %s", ts, firstLine)

	case monitor.EventToolCompleted:
		data, ok := ev.Data.(monitor.ToolCompletedData)
		if !ok {
			return ""
		}
		dur := formatToolDuration(data.DurationMs)
		if data.Success {
			return fmt.Sprintf("[%s] %s (%s)", ts, data.ToolName, dur)
		}
		return fmt.Sprintf("[%s] %s (%s, FAIL)", ts, data.ToolName, dur)

	case monitor.EventTurnCompleted:
		data, ok := ev.Data.(monitor.TurnCompletedData)
		if !ok {
			return ""
		}
		if data.CostUSD > 0 {
			return fmt.Sprintf("[%s] turn: %din %dout ($%.2f)", ts, data.InputTokens, data.OutputTokens, data.CostUSD)
		}
		return fmt.Sprintf("[%s] turn: %din %dout", ts, data.InputTokens, data.OutputTokens)

	case monitor.EventApprovalRequested:
		data, ok := ev.Data.(monitor.ApprovalRequestedData)
		if !ok {
			return ""
		}
		return fmt.Sprintf("[%s] approval: %s", ts, data.ToolName)

	case monitor.EventSessionStarted:
		data, ok := ev.Data.(monitor.SessionStartedData)
		if !ok {
			return fmt.Sprintf("[%s] session started", ts)
		}
		if data.Model != "" {
			return fmt.Sprintf("[%s] session started (%s)", ts, data.Model)
		}
		return fmt.Sprintf("[%s] session started", ts)

	case monitor.EventSessionEnded:
		return fmt.Sprintf("[%s] session ended", ts)

	default:
		// Skip turn_started, tool_started, state_change (too noisy).
		return ""
	}
}

// formatToolDuration formats milliseconds into a human-readable duration.
func formatToolDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000.0)
}

// formatRelativeDuration formats a duration as a relative time string.
func formatRelativeDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Second:
		return "now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// summarizeWithHaiku pipes the activity log to claude haiku for summarization.
func summarizeWithHaiku(activity string) error {
	prompt := fmt.Sprintf(`Summarize what this agent is currently working on in 1-2 sentences based on its recent activity log. Be specific about what tools are being used.

Activity log:
%s`, activity)

	cmd := exec.Command("claude", "--model", "haiku", "--print", prompt)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = strings.NewReader("")

	if err := cmd.Run(); err != nil {
		// Fall back to just printing the activity.
		fmt.Fprintln(os.Stderr, "(haiku summarization failed, showing raw activity)")
		fmt.Println(activity)
		return nil
	}
	return nil
}
