package cmd

import (
	"testing"
	"time"
)

func TestFormatRecord_ToolUse(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2025-01-15T12:00:30Z")
	line := `{"type":"assistant","timestamp":"2025-01-15T12:00:00Z","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/home/user/project/src/main.go"}}]}}`

	got := formatRecord(line, now)
	want := "[30s ago] Read: src/main.go"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatRecord_TextOnly(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2025-01-15T12:05:00Z")
	line := `{"type":"assistant","timestamp":"2025-01-15T12:00:00Z","message":{"content":[{"type":"text","text":"Hello, I will help you with that."}]}}`

	got := formatRecord(line, now)
	want := "[5m ago] Hello, I will help you with that."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatRecord_BashTool(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2025-01-15T12:00:10Z")
	line := `{"type":"assistant","timestamp":"2025-01-15T12:00:00Z","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"make test"}}]}}`

	got := formatRecord(line, now)
	want := "[10s ago] Bash: make test"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatRecord_UserIgnored(t *testing.T) {
	now := time.Now()
	line := `{"type":"user","timestamp":"2025-01-15T12:00:00Z","message":{"content":[{"type":"text","text":"hello"}]}}`

	got := formatRecord(line, now)
	if got != "" {
		t.Errorf("expected empty for user record, got %q", got)
	}
}

func TestFormatRecord_InvalidJSON(t *testing.T) {
	got := formatRecord("not json", time.Now())
	if got != "" {
		t.Errorf("expected empty for invalid JSON, got %q", got)
	}
}

func TestFormatRecord_MultipleBlocks(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2025-01-15T12:00:05Z")
	line := `{"type":"assistant","timestamp":"2025-01-15T12:00:00Z","message":{"content":[{"type":"text","text":"Reading file"},{"type":"tool_use","name":"Read","input":{"file_path":"/a/b/c.go"}}]}}`

	got := formatRecord(line, now)
	want := "[5s ago] Reading file | Read: b/c.go"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatRelativeTime(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2025-01-15T12:00:00Z")

	tests := []struct {
		ts   string
		want string
	}{
		{"2025-01-15T12:00:00Z", "now"},
		{"2025-01-15T11:59:30Z", "30s ago"},
		{"2025-01-15T11:55:00Z", "5m ago"},
		{"2025-01-15T10:00:00Z", "2h ago"},
		{"2025-01-13T12:00:00Z", "2d ago"},
	}

	for _, tt := range tests {
		got := formatRelativeTime(tt.ts, now)
		if got != tt.want {
			t.Errorf("formatRelativeTime(%q) = %q, want %q", tt.ts, got, tt.want)
		}
	}
}

func TestShortPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"file.go", "file.go"},
		{"dir/file.go", "dir/file.go"},
		{"/home/user/project/src/main.go", "src/main.go"},
	}
	for _, tt := range tests {
		got := shortPath(tt.input)
		if got != tt.want {
			t.Errorf("shortPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestToolDetail_Grep(t *testing.T) {
	block := contentBlock{
		Type:  "tool_use",
		Name:  "Grep",
		Input: []byte(`{"pattern":"TODO"}`),
	}
	got := toolDetail(block)
	if got != "TODO" {
		t.Errorf("got %q, want %q", got, "TODO")
	}
}

func TestToolDetail_Task(t *testing.T) {
	block := contentBlock{
		Type:  "tool_use",
		Name:  "Task",
		Input: []byte(`{"description":"Run unit tests"}`),
	}
	got := toolDetail(block)
	if got != "Run unit tests" {
		t.Errorf("got %q, want %q", got, "Run unit tests")
	}
}

func TestToolDetail_BashTruncation(t *testing.T) {
	longCmd := "this is a very long command that exceeds the sixty character limit for display"
	block := contentBlock{
		Type:  "tool_use",
		Name:  "Bash",
		Input: []byte(`{"command":"` + longCmd + `"}`),
	}
	got := toolDetail(block)
	if len(got) > 60 {
		t.Errorf("expected truncated to 60 chars, got %d: %q", len(got), got)
	}
}

func TestFormatRecord_TextTruncation(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2025-01-15T12:00:01Z")
	longText := "This is a very long text message that exceeds eighty characters and should be truncated to avoid cluttering the output display"
	line := `{"type":"assistant","timestamp":"2025-01-15T12:00:00Z","message":{"content":[{"type":"text","text":"` + longText + `"}]}}`

	got := formatRecord(line, now)
	// The text part should be truncated to 80 chars (77 + "...")
	if len(got) > 100 { // [1s ago] prefix + 80 char text
		// Check it ends with ...
		if got[len(got)-3:] != "..." {
			t.Errorf("expected truncated text ending with ..., got %q", got)
		}
	}
}
