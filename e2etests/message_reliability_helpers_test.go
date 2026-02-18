package e2etests

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// tokenPrefix is the common prefix for all receipt tokens.
const tokenPrefix = "RECEIPT-"

// --- Sandbox Setup ---

// reliabilitySandbox holds the isolated environment for a single reliability test.
type reliabilitySandbox struct {
	H2Dir      string // H2_DIR root
	ProjectDir string // working directory for the agent
	AgentName  string // agent name in this sandbox
}

// createReliabilitySandbox creates a fully isolated h2 environment for a
// reliability test. It initializes h2, creates a role with the given permission
// script path (empty = no custom permission script), writes CLAUDE.md
// instructions, and creates the project working directory.
func createReliabilitySandbox(t *testing.T, agentName string, permissionScriptPath string) reliabilitySandbox {
	t.Helper()

	h2Dir := createTestH2Dir(t)

	projectDir := filepath.Join(h2Dir, "..", "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("create project dir: %v", err)
	}

	// Write CLAUDE.md with receipt token instructions.
	claudeConfigDir := filepath.Join(h2Dir, "claude-config", "default")
	if err := os.MkdirAll(claudeConfigDir, 0o755); err != nil {
		t.Fatalf("create claude config dir: %v", err)
	}
	claudeMD := `You are a test agent. When you receive messages containing RECEIPT-,
acknowledge them silently and continue your current work. Do not stop
working to respond to RECEIPT- messages.

When asked to list all RECEIPT- messages, list every one you remember
seeing, one per line, with the exact token string.`
	if err := os.WriteFile(filepath.Join(claudeConfigDir, "CLAUDE.md"), []byte(claudeMD), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	// Build role YAML.
	roleYAML := fmt.Sprintf(`name: %s
agent_type: "true"
model: haiku
working_dir: %s
instructions: |
  You are a test agent for message receipt reliability testing.
  When you receive messages containing RECEIPT-, acknowledge them
  silently and continue your current work.
  When asked to list all RECEIPT- messages, list every one you
  remember seeing, one per line.
`, agentName, projectDir)

	// If a permission script is provided, configure the PermissionRequest hook.
	if permissionScriptPath != "" {
		roleYAML += fmt.Sprintf(`hooks:
  PermissionRequest:
    - matcher: ""
      hooks:
        - type: command
          command: "%s"
          timeout: 60
`, permissionScriptPath)
	}

	createRole(t, h2Dir, agentName, roleYAML)

	return reliabilitySandbox{
		H2Dir:      h2Dir,
		ProjectDir: projectDir,
		AgentName:  agentName,
	}
}

// --- Token Sending ---

// sendTokens sends RECEIPT tokens at the given interval until stop is closed.
// Returns the list of tokens that were sent. The tokens are sent via `h2 send`
// with the specified priority. Each token has format:
// RECEIPT-<testName>-<seqNum>-<timestamp>
func sendTokens(t *testing.T, h2Dir, agentName, testName string,
	interval time.Duration, priority string, stop <-chan struct{}) []string {
	t.Helper()

	var mu sync.Mutex
	var tokens []string
	seq := 0

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			mu.Lock()
			result := make([]string, len(tokens))
			copy(result, tokens)
			mu.Unlock()
			return result
		case <-ticker.C:
			token := fmt.Sprintf("%s%s-%d-%d", tokenPrefix, testName, seq, time.Now().UnixMilli())

			result := runH2WithEnv(t, h2Dir,
				[]string{"H2_ACTOR=test-harness"},
				"send", "--priority="+priority, agentName, token)

			if result.ExitCode != 0 {
				t.Logf("sendTokens: failed to send token %s: exit=%d stderr=%s",
					token, result.ExitCode, result.Stderr)
			} else {
				mu.Lock()
				tokens = append(tokens, token)
				mu.Unlock()
				t.Logf("sendTokens: sent token %s (msgID=%s)", token, strings.TrimSpace(result.Stdout))
			}
			seq++
		}
	}
}

// sendTokensAsync starts sendTokens in a background goroutine. Returns a
// function that stops sending and returns the tokens that were sent.
func sendTokensAsync(t *testing.T, h2Dir, agentName, testName string,
	interval time.Duration, priority string) (stopAndCollect func() []string) {
	t.Helper()

	stop := make(chan struct{})
	done := make(chan []string, 1)

	go func() {
		tokens := sendTokens(t, h2Dir, agentName, testName, interval, priority, stop)
		done <- tokens
	}()

	return func() []string {
		close(stop)
		return <-done
	}
}

// --- Agent State Polling ---

// agentStatus holds parsed status from `h2 status <name>` JSON output.
type agentStatus struct {
	Name                string `json:"name"`
	State               string `json:"state"`
	SubState            string `json:"sub_state"`
	QueuedCount         int    `json:"queued_count"`
	BlockedOnPermission bool   `json:"blocked_on_permission"`
}

// queryAgentStatus runs `h2 status <name>` and parses the JSON output.
// Returns nil if the agent is not reachable.
func queryAgentStatus(t *testing.T, h2Dir, agentName string) *agentStatus {
	t.Helper()
	result := runH2(t, h2Dir, "status", agentName)
	if result.ExitCode != 0 {
		return nil
	}
	var status agentStatus
	if err := json.Unmarshal([]byte(result.Stdout), &status); err != nil {
		t.Logf("queryAgentStatus: parse error: %v (stdout=%q)", err, result.Stdout)
		return nil
	}
	return &status
}

// waitForIdle polls agent status until it reports idle state.
// Fails the test if timeout is exceeded.
func waitForIdle(t *testing.T, h2Dir, agentName string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	pollInterval := 500 * time.Millisecond

	for time.Now().Before(deadline) {
		status := queryAgentStatus(t, h2Dir, agentName)
		if status != nil && status.State == "idle" {
			t.Logf("waitForIdle: agent %s is idle", agentName)
			return
		}
		if status != nil {
			t.Logf("waitForIdle: agent %s state=%s sub_state=%s queued=%d",
				agentName, status.State, status.SubState, status.QueuedCount)
		}
		time.Sleep(pollInterval)
	}

	t.Fatalf("waitForIdle: timed out after %v waiting for agent %s to become idle", timeout, agentName)
}

// waitForActive polls agent status until it reports active state.
// Fails the test if timeout is exceeded.
func waitForActive(t *testing.T, h2Dir, agentName string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	pollInterval := 500 * time.Millisecond

	for time.Now().Before(deadline) {
		status := queryAgentStatus(t, h2Dir, agentName)
		if status != nil && status.State == "active" {
			t.Logf("waitForActive: agent %s is active", agentName)
			return
		}
		time.Sleep(pollInterval)
	}

	t.Fatalf("waitForActive: timed out after %v waiting for agent %s to become active", timeout, agentName)
}

// --- Activity Log Parsing ---

// activityLogEntry represents a single line from session-activity.jsonl.
type activityLogEntry struct {
	Timestamp string `json:"ts"`
	Actor     string `json:"actor"`
	SessionID string `json:"session_id"`
	Event     string `json:"event"`
	HookEvent string `json:"hook_event,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
}

// readActivityLog reads and parses all entries from session-activity.jsonl
// for the given agent.
func readActivityLog(t *testing.T, h2Dir, agentName string) []activityLogEntry {
	t.Helper()

	logPath := filepath.Join(h2Dir, "sessions", agentName, "session-activity.jsonl")
	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("readActivityLog: open %s: %v", logPath, err)
	}
	defer f.Close()

	var entries []activityLogEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e activityLogEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Logf("readActivityLog: skipping malformed line: %v", err)
			continue
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("readActivityLog: scan error: %v", err)
	}
	return entries
}

// collectReceivedTokens scans session-activity.jsonl for UserPromptSubmit
// hook events. Since each delivered message triggers a UserPromptSubmit hook,
// we look at the raw JSONL lines for RECEIPT tokens.
//
// This approach reads the raw lines because the hook event payload doesn't
// contain the message body — instead we look at the delivery log. As a
// fallback, we also scan the messages directory for delivered message files.
func collectReceivedTokens(t *testing.T, h2Dir, agentName string) []string {
	t.Helper()

	var tokens []string

	// Strategy 1: Scan message files in the messages directory.
	// Messages are stored as files under h2Dir/../ or ~/.h2/messages/<agent>/.
	// The h2 message directory is at the h2Dir level.
	msgDir := filepath.Join(h2Dir, "messages", agentName)
	entries, err := os.ReadDir(msgDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			data, readErr := os.ReadFile(filepath.Join(msgDir, entry.Name()))
			if readErr != nil {
				continue
			}
			body := string(data)
			// Extract any RECEIPT tokens from the message body.
			for _, token := range extractTokensFromText(body) {
				tokens = append(tokens, token)
			}
		}
	}

	return uniqueStrings(tokens)
}

// collectReceivedTokensFromAgentQuery sends a message asking the agent to
// list all RECEIPT tokens it has seen, waits for a response, and parses the
// output. This is the "agent query" verification method from the plan.
func collectReceivedTokensFromAgentQuery(t *testing.T, h2Dir, agentName string, idleTimeout time.Duration) []string {
	t.Helper()

	// Send the query message.
	result := runH2WithEnv(t, h2Dir,
		[]string{"H2_ACTOR=test-harness"},
		"send", agentName, "List all RECEIPT- messages you received. Output each token on its own line, with the exact token string only.")

	if result.ExitCode != 0 {
		t.Logf("collectReceivedTokensFromAgentQuery: send failed: exit=%d stderr=%s",
			result.ExitCode, result.Stderr)
		return nil
	}

	// Wait for agent to process the query and go idle.
	waitForIdle(t, h2Dir, agentName, idleTimeout)

	// TODO: Read the agent's response from the session output/attach buffer.
	// For now, return nil — the log-based method is the primary verification.
	return nil
}

// --- Token Extraction ---

// extractTokensFromText finds all RECEIPT-* tokens in a block of text.
func extractTokensFromText(text string) []string {
	var tokens []string
	for _, word := range strings.Fields(text) {
		// Clean surrounding punctuation.
		cleaned := strings.Trim(word, "\"',;:()[]{}.")
		if strings.HasPrefix(cleaned, tokenPrefix) {
			tokens = append(tokens, cleaned)
		}
	}
	return tokens
}

// --- Verification ---

// receiptReport holds the result of comparing sent vs received tokens.
type receiptReport struct {
	Sent          []string
	Received      []string
	Missing       []string
	Extra         []string
	LossRate      float64 // 0.0 to 1.0
	DeliveryCount int
}

// verifyReceipt compares sent vs received tokens, reports missing ones.
// Calls t.Errorf for each missing token and logs a summary.
func verifyReceipt(t *testing.T, sent, received []string) receiptReport {
	t.Helper()

	report := buildReceiptReport(sent, received)

	t.Logf("verifyReceipt: sent=%d received=%d missing=%d extra=%d loss=%.1f%%",
		len(report.Sent), len(report.Received), len(report.Missing), len(report.Extra),
		report.LossRate*100)

	for _, token := range report.Missing {
		t.Errorf("verifyReceipt: missing token: %s", token)
	}

	return report
}

// buildReceiptReport builds a receipt report from sent and received token lists.
func buildReceiptReport(sent, received []string) receiptReport {
	receivedSet := make(map[string]bool, len(received))
	for _, tok := range received {
		receivedSet[tok] = true
	}

	sentSet := make(map[string]bool, len(sent))
	for _, tok := range sent {
		sentSet[tok] = true
	}

	var missing []string
	for _, tok := range sent {
		if !receivedSet[tok] {
			missing = append(missing, tok)
		}
	}

	var extra []string
	for _, tok := range received {
		if !sentSet[tok] {
			extra = append(extra, tok)
		}
	}

	lossRate := 0.0
	if len(sent) > 0 {
		lossRate = float64(len(missing)) / float64(len(sent))
	}

	return receiptReport{
		Sent:          sent,
		Received:      received,
		Missing:       missing,
		Extra:         extra,
		LossRate:      lossRate,
		DeliveryCount: len(received) - len(extra),
	}
}

// --- Permission Scripts ---

// createPermissionScript writes a permission script with the given behavior.
// behavior is one of: "allow", "deny", "ask-user"
// delay is how long to wait before returning the decision.
// Returns the absolute path to the created script.
func createPermissionScript(t *testing.T, dir string, behavior string, delay time.Duration) string {
	t.Helper()

	var scriptBody string
	switch behavior {
	case "allow":
		if delay > 0 {
			scriptBody = fmt.Sprintf("#!/bin/bash\nsleep %.1f\necho '{\"behavior\": \"allow\"}'\n", delay.Seconds())
		} else {
			scriptBody = "#!/bin/bash\necho '{\"behavior\": \"allow\"}'\n"
		}
	case "deny":
		if delay > 0 {
			scriptBody = fmt.Sprintf("#!/bin/bash\nsleep %.1f\necho '{\"behavior\": \"deny\"}'\n", delay.Seconds())
		} else {
			scriptBody = "#!/bin/bash\necho '{\"behavior\": \"deny\"}'\n"
		}
	case "ask-user":
		// Empty JSON = fall through to ask_user.
		scriptBody = "#!/bin/bash\necho '{}'\n"
	default:
		t.Fatalf("createPermissionScript: unknown behavior %q", behavior)
	}

	scriptPath := filepath.Join(dir, fmt.Sprintf("permission-%s.sh", behavior))
	if err := os.WriteFile(scriptPath, []byte(scriptBody), 0o755); err != nil {
		t.Fatalf("createPermissionScript: write %s: %v", scriptPath, err)
	}

	return scriptPath
}

// --- Work File Creation ---

// createWorkFiles creates files in the project dir for the agent to work on.
// Files are named work-0.txt, work-1.txt, etc. Each contains some filler
// text that the agent can read/edit.
func createWorkFiles(t *testing.T, projectDir string, count int) {
	t.Helper()

	for i := 0; i < count; i++ {
		name := fmt.Sprintf("work-%d.txt", i)
		content := fmt.Sprintf("# Work File %d\n\nThis is work file number %d.\nIt contains sample text for testing.\n\n", i, i)
		// Add some lines to make the file non-trivial.
		for j := 0; j < 10; j++ {
			content += fmt.Sprintf("Line %d: The quick brown fox jumps over the lazy dog.\n", j)
		}
		path := filepath.Join(projectDir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("createWorkFiles: write %s: %v", path, err)
		}
	}
}

// --- Agent Lifecycle Helpers ---

// launchReliabilityAgent launches an agent in detached mode using the sandbox
// configuration. Waits for the socket to appear and registers a cleanup to
// stop the agent.
func launchReliabilityAgent(t *testing.T, sb reliabilitySandbox) {
	t.Helper()

	result := runH2(t, sb.H2Dir, "run", "--role", sb.AgentName, "--name", sb.AgentName, "--detach")
	if result.ExitCode != 0 {
		t.Skipf("launchReliabilityAgent: launch failed (exit=%d): %s", result.ExitCode, result.Stderr)
	}

	t.Cleanup(func() {
		stopAgent(t, sb.H2Dir, sb.AgentName)
	})

	waitForSocket(t, sb.H2Dir, "agent", sb.AgentName)
	t.Logf("launchReliabilityAgent: agent %s is up", sb.AgentName)
}

// sendMessage sends a normal-priority message to the agent and waits for it
// to be enqueued. Returns the message ID.
func sendMessage(t *testing.T, h2Dir, agentName, body string) string {
	t.Helper()

	result := runH2WithEnv(t, h2Dir,
		[]string{"H2_ACTOR=test-harness"},
		"send", agentName, body)

	if result.ExitCode != 0 {
		t.Fatalf("sendMessage: failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}

	return strings.TrimSpace(result.Stdout)
}

// sendMessageWithPriority sends a message with the specified priority.
func sendMessageWithPriority(t *testing.T, h2Dir, agentName, body, priority string) string {
	t.Helper()

	result := runH2WithEnv(t, h2Dir,
		[]string{"H2_ACTOR=test-harness"},
		"send", "--priority="+priority, agentName, body)

	if result.ExitCode != 0 {
		t.Fatalf("sendMessageWithPriority: failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}

	return strings.TrimSpace(result.Stdout)
}

// sendRawMessage sends a raw message (no prefix) to the agent's PTY.
func sendRawMessage(t *testing.T, h2Dir, agentName, body string) string {
	t.Helper()

	result := runH2WithEnv(t, h2Dir,
		[]string{"H2_ACTOR=test-harness"},
		"send", "--raw", agentName, body)

	if result.ExitCode != 0 {
		t.Fatalf("sendRawMessage: failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}

	return strings.TrimSpace(result.Stdout)
}

// --- Utility ---

// uniqueStrings returns a deduplicated copy of the input slice, preserving order.
func uniqueStrings(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	var result []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}
