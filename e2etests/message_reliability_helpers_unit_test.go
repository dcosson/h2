package e2etests

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- extractTokensFromText ---

func TestExtractTokensFromText_BasicTokens(t *testing.T) {
	text := "Some text RECEIPT-test-0-1234567890 more text RECEIPT-test-1-1234567891"
	tokens := extractTokensFromText(text)
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d: %v", len(tokens), tokens)
	}
	if tokens[0] != "RECEIPT-test-0-1234567890" {
		t.Errorf("token[0] = %q, want RECEIPT-test-0-1234567890", tokens[0])
	}
	if tokens[1] != "RECEIPT-test-1-1234567891" {
		t.Errorf("token[1] = %q, want RECEIPT-test-1-1234567891", tokens[1])
	}
}

func TestExtractTokensFromText_QuotedTokens(t *testing.T) {
	text := `I received "RECEIPT-test-0-123" and "RECEIPT-test-1-456"`
	tokens := extractTokensFromText(text)
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d: %v", len(tokens), tokens)
	}
	if tokens[0] != "RECEIPT-test-0-123" {
		t.Errorf("token[0] = %q, want RECEIPT-test-0-123", tokens[0])
	}
}

func TestExtractTokensFromText_NoTokens(t *testing.T) {
	text := "No receipt tokens here"
	tokens := extractTokensFromText(text)
	if len(tokens) != 0 {
		t.Errorf("expected 0 tokens, got %d: %v", len(tokens), tokens)
	}
}

func TestExtractTokensFromText_EmptyString(t *testing.T) {
	tokens := extractTokensFromText("")
	if len(tokens) != 0 {
		t.Errorf("expected 0 tokens, got %d: %v", len(tokens), tokens)
	}
}

func TestExtractTokensFromText_TokenWithPunctuation(t *testing.T) {
	text := "(RECEIPT-test-0-123), [RECEIPT-test-1-456]."
	tokens := extractTokensFromText(text)
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d: %v", len(tokens), tokens)
	}
	if tokens[0] != "RECEIPT-test-0-123" {
		t.Errorf("token[0] = %q, want RECEIPT-test-0-123", tokens[0])
	}
	if tokens[1] != "RECEIPT-test-1-456" {
		t.Errorf("token[1] = %q, want RECEIPT-test-1-456", tokens[1])
	}
}

// --- buildReceiptReport ---

func TestBuildReceiptReport_AllReceived(t *testing.T) {
	sent := []string{"RECEIPT-a-0-1", "RECEIPT-a-1-2", "RECEIPT-a-2-3"}
	received := []string{"RECEIPT-a-0-1", "RECEIPT-a-1-2", "RECEIPT-a-2-3"}
	report := buildReceiptReport(sent, received)
	if len(report.Missing) != 0 {
		t.Errorf("expected 0 missing, got %d: %v", len(report.Missing), report.Missing)
	}
	if report.LossRate != 0.0 {
		t.Errorf("expected 0%% loss, got %.1f%%", report.LossRate*100)
	}
	if report.DeliveryCount != 3 {
		t.Errorf("expected delivery count 3, got %d", report.DeliveryCount)
	}
}

func TestBuildReceiptReport_SomeMissing(t *testing.T) {
	sent := []string{"RECEIPT-a-0-1", "RECEIPT-a-1-2", "RECEIPT-a-2-3", "RECEIPT-a-3-4"}
	received := []string{"RECEIPT-a-0-1", "RECEIPT-a-2-3"}
	report := buildReceiptReport(sent, received)
	if len(report.Missing) != 2 {
		t.Errorf("expected 2 missing, got %d: %v", len(report.Missing), report.Missing)
	}
	if report.Missing[0] != "RECEIPT-a-1-2" || report.Missing[1] != "RECEIPT-a-3-4" {
		t.Errorf("wrong missing tokens: %v", report.Missing)
	}
	if report.LossRate != 0.5 {
		t.Errorf("expected 50%% loss, got %.1f%%", report.LossRate*100)
	}
}

func TestBuildReceiptReport_AllMissing(t *testing.T) {
	sent := []string{"RECEIPT-a-0-1", "RECEIPT-a-1-2"}
	received := []string{}
	report := buildReceiptReport(sent, received)
	if len(report.Missing) != 2 {
		t.Errorf("expected 2 missing, got %d", len(report.Missing))
	}
	if report.LossRate != 1.0 {
		t.Errorf("expected 100%% loss, got %.1f%%", report.LossRate*100)
	}
}

func TestBuildReceiptReport_ExtraReceived(t *testing.T) {
	sent := []string{"RECEIPT-a-0-1"}
	received := []string{"RECEIPT-a-0-1", "RECEIPT-extra-0-999"}
	report := buildReceiptReport(sent, received)
	if len(report.Missing) != 0 {
		t.Errorf("expected 0 missing, got %d", len(report.Missing))
	}
	if len(report.Extra) != 1 {
		t.Errorf("expected 1 extra, got %d: %v", len(report.Extra), report.Extra)
	}
	if report.Extra[0] != "RECEIPT-extra-0-999" {
		t.Errorf("wrong extra token: %s", report.Extra[0])
	}
}

func TestBuildReceiptReport_EmptySent(t *testing.T) {
	report := buildReceiptReport(nil, nil)
	if report.LossRate != 0.0 {
		t.Errorf("expected 0%% loss for empty sent, got %.1f%%", report.LossRate*100)
	}
}

// --- uniqueStrings ---

func TestUniqueStrings_NoDuplicates(t *testing.T) {
	result := uniqueStrings([]string{"a", "b", "c"})
	if len(result) != 3 {
		t.Errorf("expected 3, got %d", len(result))
	}
}

func TestUniqueStrings_WithDuplicates(t *testing.T) {
	result := uniqueStrings([]string{"a", "b", "a", "c", "b"})
	if len(result) != 3 {
		t.Errorf("expected 3, got %d: %v", len(result), result)
	}
	// Check order preserved.
	if result[0] != "a" || result[1] != "b" || result[2] != "c" {
		t.Errorf("wrong order: %v", result)
	}
}

func TestUniqueStrings_Empty(t *testing.T) {
	result := uniqueStrings(nil)
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

// --- createPermissionScript ---

func TestCreatePermissionScript_Allow(t *testing.T) {
	dir := t.TempDir()
	path := createPermissionScript(t, dir, "allow", 0)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	content := string(data)
	if content != "#!/bin/bash\necho '{\"behavior\": \"allow\"}'\n" {
		t.Errorf("unexpected content: %q", content)
	}
	// Check executable.
	info, _ := os.Stat(path)
	if info.Mode()&0o111 == 0 {
		t.Error("script is not executable")
	}
}

func TestCreatePermissionScript_AllowWithDelay(t *testing.T) {
	dir := t.TempDir()
	path := createPermissionScript(t, dir, "allow", 1500*time.Millisecond)
	data, _ := os.ReadFile(path)
	content := string(data)
	if content != "#!/bin/bash\nsleep 1.5\necho '{\"behavior\": \"allow\"}'\n" {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestCreatePermissionScript_Deny(t *testing.T) {
	dir := t.TempDir()
	path := createPermissionScript(t, dir, "deny", 0)
	data, _ := os.ReadFile(path)
	content := string(data)
	if content != "#!/bin/bash\necho '{\"behavior\": \"deny\"}'\n" {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestCreatePermissionScript_AskUser(t *testing.T) {
	dir := t.TempDir()
	path := createPermissionScript(t, dir, "ask-user", 0)
	data, _ := os.ReadFile(path)
	content := string(data)
	if content != "#!/bin/bash\necho '{}'\n" {
		t.Errorf("unexpected content: %q", content)
	}
}

// --- createWorkFiles ---

func TestCreateWorkFiles(t *testing.T) {
	dir := t.TempDir()
	createWorkFiles(t, dir, 5)
	for i := 0; i < 5; i++ {
		name := filepath.Join(dir, fmt.Sprintf("work-%d.txt", i))
		if _, err := os.Stat(name); err != nil {
			t.Errorf("work file %d not found: %v", i, err)
		}
	}
	// Check content is non-trivial.
	data, _ := os.ReadFile(filepath.Join(dir, "work-0.txt"))
	if len(data) < 100 {
		t.Errorf("work file too small: %d bytes", len(data))
	}
}

// --- readActivityLog ---

func TestReadActivityLog_ParsesEntries(t *testing.T) {
	dir := t.TempDir()
	agentName := "test-agent"
	sessionDir := filepath.Join(dir, "sessions", agentName)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write sample JSONL.
	entries := []map[string]string{
		{"ts": "2025-01-01T00:00:00Z", "actor": agentName, "session_id": "abc", "event": "hook", "hook_event": "UserPromptSubmit"},
		{"ts": "2025-01-01T00:00:01Z", "actor": agentName, "session_id": "abc", "event": "hook", "hook_event": "PreToolUse", "tool_name": "Bash"},
		{"ts": "2025-01-01T00:00:02Z", "actor": agentName, "session_id": "abc", "event": "state_change", "from": "idle", "to": "active"},
	}
	f, err := os.Create(filepath.Join(sessionDir, "session-activity.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, _ := json.Marshal(e)
		f.Write(data)
		f.Write([]byte("\n"))
	}
	f.Close()

	result := readActivityLog(t, dir, agentName)
	if len(result) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(result))
	}
	if result[0].Event != "hook" || result[0].HookEvent != "UserPromptSubmit" {
		t.Errorf("entry[0] unexpected: %+v", result[0])
	}
	if result[1].ToolName != "Bash" {
		t.Errorf("entry[1] tool_name = %q, want Bash", result[1].ToolName)
	}
	if result[2].Event != "state_change" || result[2].From != "idle" || result[2].To != "active" {
		t.Errorf("entry[2] unexpected: %+v", result[2])
	}
}

func TestReadActivityLog_MissingFile(t *testing.T) {
	dir := t.TempDir()
	result := readActivityLog(t, dir, "nonexistent")
	if result != nil {
		t.Errorf("expected nil for missing file, got %d entries", len(result))
	}
}

// --- collectReceivedTokens ---

func TestCollectReceivedTokens_FromActivityLog(t *testing.T) {
	dir := t.TempDir()
	agentName := "token-agent"
	sessionDir := filepath.Join(dir, "sessions", agentName)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write activity log with RECEIPT tokens embedded in various events.
	// Tokens appear as JSON values like "tool_name":"RECEIPT-..." which
	// extractTokensFromText handles by splitting on JSON delimiters too.
	lines := []string{
		`{"ts":"2025-01-01T00:00:00Z","actor":"token-agent","session_id":"abc","event":"hook","hook_event":"UserPromptSubmit"}`,
		`{"ts":"2025-01-01T00:00:01Z","actor":"token-agent","session_id":"abc","event":"hook","hook_event":"UserPromptSubmit","body":"RECEIPT-test-0-111"}`,
		`{"ts":"2025-01-01T00:00:02Z","actor":"token-agent","session_id":"abc","event":"hook","hook_event":"PreToolUse","tool_name":"RECEIPT-test-1-222"}`,
		`{"ts":"2025-01-01T00:00:03Z","actor":"token-agent","session_id":"abc","event":"state_change","from":"idle","to":"active"}`,
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(sessionDir, "session-activity.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tokens := collectReceivedTokens(t, dir, agentName)
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d: %v", len(tokens), tokens)
	}
}

func TestCollectReceivedTokens_MissingLog(t *testing.T) {
	dir := t.TempDir()
	tokens := collectReceivedTokens(t, dir, "nonexistent")
	if tokens != nil {
		t.Errorf("expected nil for missing log, got %d tokens", len(tokens))
	}
}

func TestCollectReceivedTokens_DeduplicatesTokens(t *testing.T) {
	dir := t.TempDir()
	agentName := "dedup-agent"
	sessionDir := filepath.Join(dir, "sessions", agentName)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Same token appears in two lines.
	lines := []string{
		`{"ts":"2025-01-01T00:00:00Z","event":"hook","body":"RECEIPT-test-0-111"}`,
		`{"ts":"2025-01-01T00:00:01Z","event":"hook","body":"RECEIPT-test-0-111"}`,
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(sessionDir, "session-activity.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tokens := collectReceivedTokens(t, dir, agentName)
	if len(tokens) != 1 {
		t.Errorf("expected 1 deduplicated token, got %d: %v", len(tokens), tokens)
	}
}

// --- countUserPromptSubmits ---

func TestCountUserPromptSubmits(t *testing.T) {
	dir := t.TempDir()
	agentName := "submit-agent"
	sessionDir := filepath.Join(dir, "sessions", agentName)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	entries := []map[string]string{
		{"ts": "2025-01-01T00:00:00Z", "actor": agentName, "session_id": "abc", "event": "hook", "hook_event": "UserPromptSubmit"},
		{"ts": "2025-01-01T00:00:01Z", "actor": agentName, "session_id": "abc", "event": "hook", "hook_event": "PreToolUse"},
		{"ts": "2025-01-01T00:00:02Z", "actor": agentName, "session_id": "abc", "event": "hook", "hook_event": "UserPromptSubmit"},
		{"ts": "2025-01-01T00:00:03Z", "actor": agentName, "session_id": "abc", "event": "state_change", "from": "idle", "to": "active"},
		{"ts": "2025-01-01T00:00:04Z", "actor": agentName, "session_id": "abc", "event": "hook", "hook_event": "UserPromptSubmit"},
	}
	f, err := os.Create(filepath.Join(sessionDir, "session-activity.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, _ := json.Marshal(e)
		f.Write(data)
		f.Write([]byte("\n"))
	}
	f.Close()

	count := countUserPromptSubmits(t, dir, agentName)
	if count != 3 {
		t.Errorf("expected 3 UserPromptSubmit events, got %d", count)
	}
}

// --- createReliabilitySandbox ---

func TestCreateReliabilitySandbox_CreatesStructure(t *testing.T) {
	sb := createReliabilitySandbox(t, "test-agent", sandboxOpts{})

	// h2Dir should exist.
	if _, err := os.Stat(sb.H2Dir); err != nil {
		t.Fatalf("h2Dir not found: %v", err)
	}

	// Project dir should exist.
	if _, err := os.Stat(sb.ProjectDir); err != nil {
		t.Fatalf("projectDir not found: %v", err)
	}

	// Role should exist.
	rolePath := filepath.Join(sb.H2Dir, "roles", "test-agent.yaml")
	if _, err := os.Stat(rolePath); err != nil {
		t.Fatalf("role not found: %v", err)
	}

	// CLAUDE.md should exist.
	claudePath := filepath.Join(sb.H2Dir, "claude-config", "default", "CLAUDE.md")
	if _, err := os.Stat(claudePath); err != nil {
		t.Fatalf("CLAUDE.md not found: %v", err)
	}

	// Agent name should be set.
	if sb.AgentName != "test-agent" {
		t.Errorf("agent name = %q, want test-agent", sb.AgentName)
	}
}

func TestCreateReliabilitySandbox_DefaultsToClaudeAgent(t *testing.T) {
	sb := createReliabilitySandbox(t, "default-agent", sandboxOpts{})

	roleData, err := os.ReadFile(filepath.Join(sb.H2Dir, "roles", "default-agent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(roleData)
	if !strings.Contains(content, "agent_type: claude") {
		t.Errorf("role should default to agent_type: claude, got:\n%s", content)
	}
	if !strings.Contains(content, "model: haiku") {
		t.Errorf("role should default to model: haiku, got:\n%s", content)
	}
}

func TestCreateReliabilitySandbox_CustomAgentType(t *testing.T) {
	sb := createReliabilitySandbox(t, "custom-agent", sandboxOpts{
		agentType: "true",
		model:     "sonnet",
	})

	roleData, err := os.ReadFile(filepath.Join(sb.H2Dir, "roles", "custom-agent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(roleData)
	if !strings.Contains(content, "agent_type: true") {
		t.Errorf("role should use custom agent_type, got:\n%s", content)
	}
	if !strings.Contains(content, "model: sonnet") {
		t.Errorf("role should use custom model, got:\n%s", content)
	}
}

func TestCreateReliabilitySandbox_WithPermissionScript(t *testing.T) {
	dir := t.TempDir()
	scriptPath := createPermissionScript(t, dir, "allow", 0)
	sb := createReliabilitySandbox(t, "perm-test", sandboxOpts{permissionScript: scriptPath})

	// Role should reference the permission script in hooks.
	roleData, err := os.ReadFile(filepath.Join(sb.H2Dir, "roles", "perm-test.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(roleData), "PermissionRequest") {
		t.Error("role YAML missing PermissionRequest hook config")
	}
	if !strings.Contains(string(roleData), scriptPath) {
		t.Error("role YAML missing permission script path")
	}
}
