package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"h2/internal/config"
)

func newPeekCmd() *cobra.Command {
	var logPath string
	var numLines int
	var summarize bool

	cmd := &cobra.Command{
		Use:   "peek [name]",
		Short: "View recent agent activity from Claude Code session logs",
		Long: `Read the last N records from a Claude Code session transcript and
format them as a concise activity log. Use --summarize to get a
one-sentence summary via Claude haiku.

  h2 peek concierge              Show recent activity for an agent
  h2 peek --log-path <path>      Use an explicit JSONL file
  h2 peek concierge --summarize  Summarize with haiku
  h2 peek concierge -n 50        Show last 50 records`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve the log path.
			path := logPath
			if path == "" {
				if len(args) == 0 {
					return fmt.Errorf("provide an agent name or --log-path")
				}
				name := args[0]
				sessionDir := config.SessionDir(name)
				meta, err := config.ReadSessionMetadata(sessionDir)
				if err != nil {
					return fmt.Errorf("read session metadata for %q: %w (is the agent running?)", name, err)
				}
				path = meta.ClaudeCodeSessionLogPath
			} else if len(args) > 0 {
				return fmt.Errorf("--log-path and agent name are mutually exclusive")
			}

			// Read and format the log.
			lines, err := formatSessionLog(path, numLines)
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
		},
	}

	cmd.Flags().StringVar(&logPath, "log-path", "", "Explicit path to a Claude Code session JSONL file")
	cmd.Flags().IntVarP(&numLines, "num-lines", "n", 100, "Number of JSONL records to read from the end")
	cmd.Flags().BoolVar(&summarize, "summarize", false, "Summarize activity with Claude haiku")

	return cmd
}

// formatSessionLog reads the last N lines of a JSONL file and formats tool calls.
func formatSessionLog(path string, numLines int) ([]string, error) {
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
		formatted := formatRecord(line, now)
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
func formatRecord(line string, now time.Time) string {
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
				// Truncate long text to first line, max 80 chars.
				firstLine := strings.SplitN(text, "\n", 2)[0]
				if len(firstLine) > 80 {
					firstLine = firstLine[:77] + "..."
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

// formatRelativeTime formats a timestamp as a relative duration from now.
func formatRelativeTime(ts string, now time.Time) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05.000Z", ts)
		if err != nil {
			return ts
		}
	}

	d := now.Sub(t)
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
	prompt := fmt.Sprintf(`Summarize what this Claude Code agent is currently working on in 1-2 sentences based on its recent activity log. Be specific about what files and tools are being used.

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

// tailFile reads the last N lines from a file efficiently.
func tailFile(r io.ReadSeeker, n int) ([]string, error) {
	// For simplicity, read all and take last N.
	// Session logs are typically <100MB, so this is fine.
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	var lines []string
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}
