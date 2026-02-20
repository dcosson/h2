package cmd

import (
	"bufio"
	"encoding/json"
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
	var logPath string
	var numLines int
	var messageChars int
	var summarize bool

	cmd := &cobra.Command{
		Use:   "peek [name]",
		Short: "View recent agent activity",
		Long: `Read the last N events from an agent's event store and format them
as a concise activity log. Use --summarize to get a one-sentence summary
via Claude haiku. Use --log-path for raw Claude Code session JSONL files.

  h2 peek concierge              Show recent activity for an agent
  h2 peek --log-path <path>      Use a raw Claude Code session JSONL file
  h2 peek concierge --summarize  Summarize with haiku
  h2 peek concierge -n 500       Show last 500 events (default 150)`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// --log-path: legacy path (raw Claude Code JSONL).
			if logPath != "" {
				if len(args) > 0 {
					return fmt.Errorf("--log-path and agent name are mutually exclusive")
				}
				return peekLegacy(logPath, numLines, messageChars, summarize)
			}

			if len(args) == 0 {
				return fmt.Errorf("provide an agent name or --log-path")
			}

			name := args[0]
			sessionDir := config.SessionDir(name)

			// Try event store first.
			lines, err := formatEventLog(sessionDir, numLines, messageChars)
			if err != nil {
				// Fall back to legacy Claude Code JSONL via session metadata.
				meta, metaErr := config.ReadSessionMetadata(sessionDir)
				if metaErr != nil {
					return fmt.Errorf("read session for %q: %w (is the agent running?)", name, metaErr)
				}
				lines, err = formatSessionLog(meta.ClaudeCodeSessionLogPath, numLines, messageChars)
				if err != nil {
					return err
				}
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

	cmd.Flags().StringVar(&logPath, "log-path", "", "Explicit path to a Claude Code session JSONL file (legacy)")
	cmd.Flags().IntVarP(&numLines, "num-lines", "n", 150, "Number of events to read from the end")
	cmd.Flags().IntVar(&messageChars, "message-chars", 500, "Max characters for message text (0 for no limit)")
	cmd.Flags().BoolVar(&summarize, "summarize", false, "Summarize activity with Claude haiku")

	return cmd
}

// peekLegacy runs the legacy peek path using a raw Claude Code session JSONL file.
func peekLegacy(path string, numLines, messageChars int, summarize bool) error {
	lines, err := formatSessionLog(path, numLines, messageChars)
	if err != nil {
		return err
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
}

// --- Event store path ---

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

// --- Legacy Claude Code JSONL path ---

// formatSessionLog reads the last N lines of a JSONL file and formats tool calls.
func formatSessionLog(path string, numLines int, messageChars int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open session log: %w", err)
	}
	defer f.Close()

	// Read all lines (session logs are typically manageable size).
	var allLines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB line buffer
	for scanner.Scan() {
		allLines = append(allLines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read session log: %w", err)
	}

	// Take the last N lines.
	start := 0
	if len(allLines) > numLines {
		start = len(allLines) - numLines
	}
	recent := allLines[start:]

	now := time.Now()
	var output []string
	for _, line := range recent {
		formatted := formatRecord(line, now, messageChars)
		if formatted != "" {
			output = append(output, formatted)
		}
	}
	return output, nil
}

// sessionRecord is the minimal structure for parsing JSONL records.
type sessionRecord struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
}

// assistantMessage represents the message field of an assistant record.
type assistantMessage struct {
	Content []contentBlock `json:"content"`
}

// contentBlock represents a content block in an assistant message.
type contentBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name,omitempty"`  // for tool_use
	Text  string          `json:"text,omitempty"`  // for text
	Input json.RawMessage `json:"input,omitempty"` // for tool_use
}

// toolInput extracts a summary of tool input parameters.
type toolInput struct {
	Command     string `json:"command,omitempty"`     // Bash
	FilePath    string `json:"file_path,omitempty"`   // Read, Write, Edit
	Pattern     string `json:"pattern,omitempty"`     // Grep, Glob
	Query       string `json:"query,omitempty"`       // WebSearch
	URL         string `json:"url,omitempty"`         // WebFetch
	Description string `json:"description,omitempty"` // Task
	Prompt      string `json:"prompt,omitempty"`      // Task
	Skill       string `json:"skill,omitempty"`       // Skill
}

// formatRecord formats a single JSONL record into a human-readable line.
// Returns "" if the record isn't interesting (not a tool call or text).
func formatRecord(line string, now time.Time, messageChars int) string {
	var rec sessionRecord
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		return ""
	}

	if rec.Type != "assistant" {
		return ""
	}

	ts := formatRelativeTime(rec.Timestamp, now)

	var msg assistantMessage
	if err := json.Unmarshal(rec.Message, &msg); err != nil {
		return ""
	}

	var parts []string
	for _, block := range msg.Content {
		switch block.Type {
		case "tool_use":
			detail := toolDetail(block)
			if detail != "" {
				parts = append(parts, fmt.Sprintf("%s: %s", block.Name, detail))
			} else {
				parts = append(parts, block.Name)
			}
		case "text":
			text := strings.TrimSpace(block.Text)
			if text != "" {
				firstLine := strings.SplitN(text, "\n", 2)[0]
				if messageChars > 0 && len(firstLine) > messageChars {
					firstLine = firstLine[:messageChars-3] + "..."
				}
				parts = append(parts, firstLine)
			}
		}
	}

	if len(parts) == 0 {
		return ""
	}

	return fmt.Sprintf("[%s] %s", ts, strings.Join(parts, " | "))
}

// toolDetail extracts a short description of what the tool is doing.
func toolDetail(block contentBlock) string {
	var input toolInput
	if err := json.Unmarshal(block.Input, &input); err != nil {
		return ""
	}

	switch block.Name {
	case "Bash":
		cmd := input.Command
		if cmd == "" {
			return ""
		}
		if len(cmd) > 60 {
			cmd = cmd[:57] + "..."
		}
		return cmd
	case "Read":
		return shortPath(input.FilePath)
	case "Write":
		return shortPath(input.FilePath)
	case "Edit":
		return shortPath(input.FilePath)
	case "Grep":
		return input.Pattern
	case "Glob":
		return input.Pattern
	case "WebSearch":
		return input.Query
	case "Task":
		if input.Description != "" {
			return input.Description
		}
		return ""
	default:
		return ""
	}
}

// shortPath returns the last 2 components of a file path.
func shortPath(p string) string {
	if p == "" {
		return ""
	}
	parts := strings.Split(p, "/")
	if len(parts) <= 2 {
		return p
	}
	return strings.Join(parts[len(parts)-2:], "/")
}

// formatRelativeTime formats a timestamp string as a relative duration from now.
func formatRelativeTime(ts string, now time.Time) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05.000Z", ts)
		if err != nil {
			return ts
		}
	}
	return formatRelativeDuration(now.Sub(t))
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
