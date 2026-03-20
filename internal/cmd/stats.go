package cmd

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"h2/internal/config"
)

type statsUsageRow struct {
	Bucket      string  `json:"bucket"`
	TokensIn    int64   `json:"tokens_in"`
	TokensOut   int64   `json:"tokens_out"`
	Turns       int64   `json:"turns"`
	ToolUses    int64   `json:"tool_uses"`
	H2Messages  int64   `json:"h2_messages"`
	TokensInM   float64 `json:"tokens_in_m"`
	TokensOutM  float64 `json:"tokens_out_m"`
	TokensInTxt string  `json:"tokens_in_display"`
	TokOutTxt   string  `json:"tokens_out_display"`
}

type statsUsageOptions struct {
	startRaw       string
	endRaw         string
	rollup         string
	format         string
	matchAgentName []string
	matchHarness   []string
	matchProfile   []string
	matchRole      []string
}

type statsAgentSession struct {
	AgentName string
	Role      string
	Harness   string
	Profile   string
	Events    string
}

func newStatsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show h2 statistics",
	}
	cmd.AddCommand(newStatsUsageCmd())
	return cmd
}

func newStatsUsageCmd() *cobra.Command {
	opts := statsUsageOptions{
		rollup: "day",
		format: "table",
	}
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Usage stats (tokens, turns, tools, h2 messages)",
		RunE: func(cmd *cobra.Command, args []string) error {
			start, end, err := parseStatsTimeRange(opts.startRaw, opts.endRaw)
			if err != nil {
				return err
			}
			if err := validateStatsUsageOptions(opts); err != nil {
				return err
			}
			rows, err := collectUsageRows(config.ConfigDir(), opts, start, end)
			if err != nil {
				return err
			}
			switch opts.format {
			case "json":
				return printUsageJSON(cmd.OutOrStdout(), rows)
			case "csv":
				return printUsageCSV(cmd.OutOrStdout(), rows)
			default:
				return printUsageTable(cmd.OutOrStdout(), rows)
			}
		},
	}

	cmd.Flags().StringVar(&opts.startRaw, "start", "", "Start date/time (YYYY-MM-DD or ISO datetime)")
	cmd.Flags().StringVar(&opts.endRaw, "end", "", "End date/time (YYYY-MM-DD or ISO datetime)")
	cmd.Flags().StringVar(&opts.rollup, "rollup", "day", "Rollup bucket: total, year, month, week, day, hour")
	cmd.Flags().StringVar(&opts.format, "format", "table", "Output format: table, json, csv")
	cmd.Flags().StringArrayVar(&opts.matchAgentName, "match-agent-name", nil, "Glob match agent name (repeatable)")
	cmd.Flags().StringArrayVar(&opts.matchHarness, "match-harness", nil, "Match harness (claude_code, codex, generic), repeatable")
	cmd.Flags().StringArrayVar(&opts.matchProfile, "match-profile", nil, "Match profile name, repeatable")
	cmd.Flags().StringArrayVar(&opts.matchRole, "match-role", nil, "Match role name, repeatable")
	return cmd
}

func validateStatsUsageOptions(opts statsUsageOptions) error {
	validRollups := map[string]bool{
		"total": true, "year": true, "month": true, "week": true, "day": true, "hour": true,
	}
	if !validRollups[opts.rollup] {
		return fmt.Errorf("invalid --rollup %q; valid: total, year, month, week, day, hour", opts.rollup)
	}
	validFormats := map[string]bool{"table": true, "json": true, "csv": true}
	if !validFormats[opts.format] {
		return fmt.Errorf("invalid --format %q; valid: table, json, csv", opts.format)
	}
	return nil
}

func collectUsageRows(h2Dir string, opts statsUsageOptions, start, end *time.Time) ([]statsUsageRow, error) {
	agents, err := loadStatsAgents(h2Dir)
	if err != nil {
		return nil, err
	}
	agg := map[string]*statsUsageRow{}
	for _, agent := range agents {
		if !matchesAgentFilters(agent, opts) {
			continue
		}
		if err := aggregateAgentEvents(agent, opts.rollup, start, end, agg); err != nil {
			return nil, err
		}
		if err := aggregateAgentMessages(h2Dir, agent.AgentName, opts.rollup, start, end, agg); err != nil {
			return nil, err
		}
	}

	rows := make([]statsUsageRow, 0, len(agg))
	for _, row := range agg {
		row.TokensInM = float64(row.TokensIn) / 1_000_000
		row.TokensOutM = float64(row.TokensOut) / 1_000_000
		row.TokensInTxt = fmt.Sprintf("%.1fM", row.TokensInM)
		row.TokOutTxt = fmt.Sprintf("%.1fM", row.TokensOutM)
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Bucket < rows[j].Bucket })
	return rows, nil
}

func loadStatsAgents(h2Dir string) ([]statsAgentSession, error) {
	sessionsDir := filepath.Join(h2Dir, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("read sessions dir: %w", err)
	}
	roleCache := map[string]statsAgentSession{}
	out := []statsAgentSession{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		agentName := e.Name()
		dir := filepath.Join(sessionsDir, agentName)
		events := filepath.Join(dir, "events.jsonl")
		if _, err := os.Stat(events); err != nil {
			continue
		}
		metaPath := filepath.Join(dir, "session.metadata.json")
		roleName := ""
		command := ""
		if data, err := os.ReadFile(metaPath); err == nil {
			var meta struct {
				Role    string `json:"role"`
				Command string `json:"command"`
			}
			_ = json.Unmarshal(data, &meta)
			roleName = strings.TrimSpace(meta.Role)
			command = strings.TrimSpace(meta.Command)
		}

		harness := deriveHarnessFromCommand(command)
		profile := "unknown"
		if roleName != "" {
			if cached, ok := roleCache[roleName]; ok {
				if cached.Harness != "" {
					harness = cached.Harness
				}
				if cached.Profile != "" {
					profile = cached.Profile
				}
			} else {
				loadedHarness := ""
				loadedProfile := "unknown"
				if role, _, err := config.LoadRoleForDisplay(roleName); err == nil && role != nil {
					if role.GetHarnessType() != "" {
						loadedHarness = role.GetHarnessType()
					}
					if p := strings.TrimSpace(role.GetProfile()); p != "" {
						loadedProfile = p
					}
				}
				roleCache[roleName] = statsAgentSession{Harness: loadedHarness, Profile: loadedProfile}
				if loadedHarness != "" {
					harness = loadedHarness
				}
				if loadedProfile != "" {
					profile = loadedProfile
				}
			}
		}
		if profile == "" {
			profile = "unknown"
		}
		out = append(out, statsAgentSession{
			AgentName: agentName,
			Role:      roleName,
			Harness:   harness,
			Profile:   profile,
			Events:    events,
		})
	}
	return out, nil
}

func deriveHarnessFromCommand(command string) string {
	switch filepath.Base(strings.TrimSpace(command)) {
	case "claude":
		return "claude_code"
	case "codex":
		return "codex"
	default:
		return "generic"
	}
}

func matchesAgentFilters(a statsAgentSession, opts statsUsageOptions) bool {
	if len(opts.matchAgentName) > 0 {
		ok := false
		for _, pattern := range opts.matchAgentName {
			if m, _ := filepath.Match(pattern, a.AgentName); m {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(opts.matchRole) > 0 && !matchesExactAny(a.Role, opts.matchRole) {
		return false
	}
	if len(opts.matchHarness) > 0 && !matchesExactAny(a.Harness, opts.matchHarness) {
		return false
	}
	if len(opts.matchProfile) > 0 && !matchesExactAny(a.Profile, opts.matchProfile) {
		return false
	}
	return true
}

func matchesExactAny(value string, vals []string) bool {
	for _, v := range vals {
		if value == v {
			return true
		}
	}
	return false
}

func aggregateAgentEvents(agent statsAgentSession, rollup string, start, end *time.Time, agg map[string]*statsUsageRow) error {
	data, err := os.ReadFile(agent.Events)
	if err != nil {
		return fmt.Errorf("read %s: %w", agent.Events, err)
	}
	for _, ln := range bytes.Split(data, []byte{'\n'}) {
		ln = bytes.TrimSpace(ln)
		if len(ln) == 0 {
			continue
		}
		var ev struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Data      struct {
				InputTokens  int64 `json:"InputTokens"`
				OutputTokens int64 `json:"OutputTokens"`
			} `json:"data"`
		}
		if err := json.Unmarshal(ln, &ev); err != nil {
			continue
		}
		ts, err := parseEventTime(ev.Timestamp)
		if err != nil {
			continue
		}
		if !inTimeRange(ts, start, end) {
			continue
		}
		b := bucketKey(ts, rollup)
		row := ensureUsageRow(agg, b)
		switch ev.Type {
		case "turn_completed":
			row.TokensIn += ev.Data.InputTokens
			row.TokensOut += ev.Data.OutputTokens
			row.Turns++
		case "tool_started":
			row.ToolUses++
		}
	}
	return nil
}

func aggregateAgentMessages(h2Dir, agentName, rollup string, start, end *time.Time, agg map[string]*statsUsageRow) error {
	msgDir := filepath.Join(h2Dir, "messages", agentName)
	entries, err := os.ReadDir(msgDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read messages dir %s: %w", msgDir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ts, ok := parseMessageFileTime(e.Name())
		if !ok {
			continue
		}
		if !inTimeRange(ts, start, end) {
			continue
		}
		b := bucketKey(ts, rollup)
		row := ensureUsageRow(agg, b)
		row.H2Messages++
	}
	return nil
}

func parseMessageFileTime(name string) (time.Time, bool) {
	parts := strings.SplitN(name, "-", 3)
	if len(parts) < 2 || len(parts[0]) != 8 || len(parts[1]) != 6 {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("20060102150405", parts[0]+parts[1], time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func ensureUsageRow(agg map[string]*statsUsageRow, b string) *statsUsageRow {
	row := agg[b]
	if row == nil {
		row = &statsUsageRow{Bucket: b}
		agg[b] = row
	}
	return row
}

func parseEventTime(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err == nil {
		return t, nil
	}
	return time.Time{}, err
}

func inTimeRange(t time.Time, start, end *time.Time) bool {
	if start != nil && t.Before(*start) {
		return false
	}
	if end != nil && !t.Before(*end) {
		return false
	}
	return true
}

func bucketKey(t time.Time, rollup string) string {
	lt := t.In(time.Local)
	switch rollup {
	case "total":
		return "total"
	case "year":
		return fmt.Sprintf("%04d", lt.Year())
	case "month":
		return fmt.Sprintf("%04d-%02d", lt.Year(), int(lt.Month()))
	case "week":
		weekday := lt.Weekday()
		if weekday == time.Sunday {
			weekday = 7
		}
		monday := lt.AddDate(0, 0, -int(weekday-time.Monday))
		return monday.Format("2006-01-02")
	case "hour":
		return lt.Format("2006-01-02 15:00")
	default:
		return lt.Format("2006-01-02")
	}
}

func parseStatsTimeRange(startRaw, endRaw string) (*time.Time, *time.Time, error) {
	start, err := parseStatsTime(startRaw, false)
	if err != nil {
		return nil, nil, fmt.Errorf("parse --start: %w", err)
	}
	end, err := parseStatsTime(endRaw, true)
	if err != nil {
		return nil, nil, fmt.Errorf("parse --end: %w", err)
	}
	if start != nil && end != nil && !start.Before(*end) {
		return nil, nil, fmt.Errorf("--start must be before --end")
	}
	return start, end, nil
}

func parseStatsTime(raw string, isEnd bool) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if len(raw) == len("2006-01-02") && strings.Count(raw, "-") == 2 {
		d, err := time.ParseInLocation("2006-01-02", raw, time.Local)
		if err != nil {
			return nil, err
		}
		if isEnd {
			e := d.Add(24 * time.Hour)
			return &e, nil
		}
		return &d, nil
	}

	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return &t, nil
		}
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("unsupported time format %q", raw)
}

func printUsageJSON(out io.Writer, rows []statsUsageRow) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}

func printUsageCSV(out io.Writer, rows []statsUsageRow) error {
	w := csv.NewWriter(out)
	if err := w.Write([]string{
		"bucket", "tokens_in", "tokens_out", "tokens_in_display", "tokens_out_display", "turns", "tool_uses", "h2_messages",
	}); err != nil {
		return err
	}
	for _, row := range rows {
		if err := w.Write([]string{
			row.Bucket,
			strconv.FormatInt(row.TokensIn, 10),
			strconv.FormatInt(row.TokensOut, 10),
			row.TokensInTxt,
			row.TokOutTxt,
			strconv.FormatInt(row.Turns, 10),
			strconv.FormatInt(row.ToolUses, 10),
			strconv.FormatInt(row.H2Messages, 10),
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func printUsageTable(out io.Writer, rows []statsUsageRow) error {
	headers := []string{"bucket", "tokens_in", "tokens_out", "turns", "tool_uses", "h2_messages"}
	widths := []int{len(headers[0]), len(headers[1]), len(headers[2]), len(headers[3]), len(headers[4]), len(headers[5])}

	formatted := make([][]string, 0, len(rows))
	for _, row := range rows {
		rec := []string{
			row.Bucket,
			row.TokensInTxt,
			row.TokOutTxt,
			formatInt(row.Turns),
			formatInt(row.ToolUses),
			formatInt(row.H2Messages),
		}
		for i, s := range rec {
			if len(s) > widths[i] {
				widths[i] = len(s)
			}
		}
		formatted = append(formatted, rec)
	}

	fmt.Fprintf(out, "%-*s  %-*s  %-*s  %*s  %*s  %*s\n",
		widths[0], headers[0],
		widths[1], headers[1],
		widths[2], headers[2],
		widths[3], headers[3],
		widths[4], headers[4],
		widths[5], headers[5],
	)
	for _, rec := range formatted {
		fmt.Fprintf(out, "%-*s  %-*s  %-*s  %*s  %*s  %*s\n",
			widths[0], rec[0],
			widths[1], rec[1],
			widths[2], rec[2],
			widths[3], rec[3],
			widths[4], rec[4],
			widths[5], rec[5],
		)
	}
	return nil
}

func formatInt(v int64) string {
	s := strconv.FormatInt(v, 10)
	if len(s) <= 3 {
		return s
	}
	n := len(s)
	out := make([]byte, 0, n+n/3)
	pre := n % 3
	if pre == 0 {
		pre = 3
	}
	out = append(out, s[:pre]...)
	for i := pre; i < n; i += 3 {
		out = append(out, ',')
		out = append(out, s[i:i+3]...)
	}
	return string(out)
}
