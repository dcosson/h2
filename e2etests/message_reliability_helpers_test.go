package e2etests

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// sandboxOpts configures the reliability sandbox.
type sandboxOpts struct {
	agentType        string // agent type for the role (default "claude")
	model            string // model to use (default "haiku")
	permissionScript string // path to permission script (empty = no custom script)
}

// createReliabilitySandbox creates a fully isolated h2 environment for a
// reliability test. It initializes h2, creates a role, writes CLAUDE.md
// instructions, and creates the project working directory.
func createReliabilitySandbox(t *testing.T, agentName string, opts sandboxOpts) reliabilitySandbox {
	t.Helper()

	agentType := opts.agentType
	if agentType == "" {
		agentType = "claude"
	}
	model := opts.model
	if model == "" {
		model = "haiku"
	}

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
agent_type: %s
model: %s
working_dir: %s
instructions: |
  You are a test agent for message receipt reliability testing.
  When you receive messages containing RECEIPT-, acknowledge them
  silently and continue your current work.
  When asked to list all RECEIPT- messages, list every one you
  remember seeing, one per line.
`, agentName, agentType, model, projectDir)

	// If a permission script is provided, configure the PermissionRequest hook.
	if opts.permissionScript != "" {
		roleYAML += fmt.Sprintf(`hooks:
  PermissionRequest:
    - matcher: ""
      hooks:
        - type: command
          command: "%s"
          timeout: 60
`, opts.permissionScript)
	}

	createRole(t, h2Dir, agentName, roleYAML)

	return reliabilitySandbox{
		H2Dir:      h2Dir,
		ProjectDir: projectDir,
		AgentName:  agentName,
	}
}

// --- Token Sending ---

// sendTokenResult holds the outcome of a single token send attempt.
type sendTokenResult struct {
	Token string
	OK    bool
	Error string
}

// trySendToken sends a single RECEIPT token via h2 send without calling t.Fatal.
// Safe to call from any goroutine.
func trySendToken(h2Dir, agentName, token, priority string) sendTokenResult {
	cmd := exec.Command(h2Binary, "send", "--priority="+priority, agentName, token)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Clear any inherited H2_DIR before setting the test value (matches runH2Opts pattern).
	cmd.Env = append(os.Environ(), "H2_DIR=")
	cmd.Env = append(cmd.Env, "H2_DIR="+h2Dir, "H2_ACTOR=test-harness")

	err := cmd.Run()
	if err != nil {
		return sendTokenResult{Token: token, OK: false, Error: stderr.String()}
	}
	return sendTokenResult{Token: token, OK: true}
}

// sendTokens sends RECEIPT tokens at the given interval until stop is closed.
// Returns the list of tokens that were successfully sent. Safe to call from
// any goroutine (does not call t.Fatal).
// Token format: RECEIPT-<testName>-<seqNum>-<timestamp>
func sendTokens(t *testing.T, h2Dir, agentName, testName string,
	interval time.Duration, priority string, stop <-chan struct{}) []string {
	var tokens []string
	seq := 0

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return tokens
		case <-ticker.C:
			token := fmt.Sprintf("%s%s-%d-%d", tokenPrefix, testName, seq, time.Now().UnixMilli())
			result := trySendToken(h2Dir, agentName, token, priority)
			if !result.OK {
				t.Logf("sendTokens: failed to send token %s: %s", token, result.Error)
			} else {
				tokens = append(tokens, token)
				t.Logf("sendTokens: sent token %s", token)
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

// collectReceivedTokens counts how many RECEIPT tokens were delivered to the
// agent by counting UserPromptSubmit hook events in session-activity.jsonl.
//
// Each delivered message triggers a UserPromptSubmit hook when the text is
// typed into the PTY. We count these events to determine delivery. Since the
// hook payload doesn't contain the message body, we also scan the raw JSONL
// lines for any RECEIPT token strings that may appear in surrounding context.
//
// As a complementary strategy, we also scan the raw lines of the activity log
// for RECEIPT tokens — they sometimes appear in hook payloads or other events.
func collectReceivedTokens(t *testing.T, h2Dir, agentName string) []string {
	t.Helper()

	logPath := filepath.Join(h2Dir, "sessions", agentName, "session-activity.jsonl")
	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Logf("collectReceivedTokens: no activity log at %s", logPath)
			return nil
		}
		t.Fatalf("collectReceivedTokens: open %s: %v", logPath, err)
	}
	defer f.Close()

	var tokens []string

	// Scan raw JSONL lines for RECEIPT tokens. This catches tokens that
	// appear anywhere in the log (hook payloads, tool output, etc.).
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		for _, token := range extractTokensFromText(line) {
			tokens = append(tokens, token)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("collectReceivedTokens: scan error: %v", err)
	}

	return uniqueStrings(tokens)
}

// countUserPromptSubmits counts UserPromptSubmit hook events in the activity
// log. This gives the total number of messages that were submitted to the
// agent's context, regardless of content.
func countUserPromptSubmits(t *testing.T, h2Dir, agentName string) int {
	t.Helper()
	entries := readActivityLog(t, h2Dir, agentName)
	count := 0
	for _, e := range entries {
		if e.Event == "hook" && e.HookEvent == "UserPromptSubmit" {
			count++
		}
	}
	return count
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
// Works with both plain text and JSON-encoded strings (where tokens may
// appear as values like "body":"RECEIPT-test-0-111").
func extractTokensFromText(text string) []string {
	var tokens []string
	// Search for the RECEIPT- prefix at any position in the text, then
	// extract the full token (contiguous non-delimiter characters).
	for i := 0; i < len(text); i++ {
		if !strings.HasPrefix(text[i:], tokenPrefix) {
			continue
		}
		// Found a RECEIPT- prefix. Scan forward to find end of token.
		j := i + len(tokenPrefix)
		for j < len(text) {
			c := text[j]
			// Token characters: alphanumeric, dash, underscore, dot
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
				j++
			} else {
				break
			}
		}
		token := text[i:j]
		if len(token) > len(tokenPrefix) { // Must have content after prefix
			tokens = append(tokens, token)
		}
		i = j - 1 // Skip past this token
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
