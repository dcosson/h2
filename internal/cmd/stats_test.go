package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatsUsage_TableAndFilters(t *testing.T) {
	h2Dir := setupProfileTestH2Dir(t)

	rolePath := filepath.Join(h2Dir, "roles", "default.yaml")
	role := `role_name: default
agent_harness: codex
agent_account_profile: alt1
`
	if err := os.WriteFile(rolePath, []byte(role), 0o644); err != nil {
		t.Fatal(err)
	}

	sessionDir := filepath.Join(h2Dir, "sessions", "foo-sand")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"agent_name":"foo-sand","session_id":"sid","command":"codex","role":"default","started_at":"2026-03-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(sessionDir, "session.metadata.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	events := strings.Join([]string{
		`{"type":"turn_completed","timestamp":"2026-03-01T12:00:00Z","data":{"InputTokens":1500000,"OutputTokens":2500000}}`,
		`{"type":"tool_started","timestamp":"2026-03-01T12:01:00Z","data":{"ToolName":"exec_command"}}`,
		`{"type":"turn_completed","timestamp":"2026-03-02T12:00:00Z","data":{"InputTokens":10,"OutputTokens":20}}`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(sessionDir, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}

	msgDir := filepath.Join(h2Dir, "messages", "foo-sand")
	if err := os.MkdirAll(msgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(msgDir, "20260301-120500-aaa.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newStatsUsageCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{
		"--start", "2026-03-01",
		"--end", "2026-03-01",
		"--rollup", "day",
		"--match-agent-name", "*-sand",
		"--match-harness", "codex",
		"--match-role", "default",
		"--match-profile", "alt1",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stats usage failed: %v\n%s", err, out.String())
	}

	s := out.String()
	expect := []string{
		"bucket",
		"2026-03-01",
		"1.5M",
		"2.5M",
		"1",
		"1",
		"1",
	}
	for _, w := range expect {
		if !strings.Contains(s, w) {
			t.Fatalf("output missing %q:\n%s", w, s)
		}
	}
	if strings.Contains(s, "2026-03-02") {
		t.Fatalf("unexpected bucket outside range:\n%s", s)
	}
}

func TestParseStatsTimeRange_DateEndIsExclusiveNextDay(t *testing.T) {
	start, end, err := parseStatsTimeRange("2026-03-01", "2026-03-01")
	if err != nil {
		t.Fatalf("parseStatsTimeRange: %v", err)
	}
	if start == nil || end == nil {
		t.Fatal("expected both start and end")
	}
	if !start.Before(*end) {
		t.Fatalf("expected start before end, got start=%v end=%v", *start, *end)
	}
	if end.Sub(*start) != 24*time.Hour {
		t.Fatalf("expected 24h window for date end, got %s", end.Sub(*start))
	}
}
