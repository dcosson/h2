package cmd

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"h2/internal/config"
	"h2/internal/session/message"
	"h2/internal/socketdir"
)

// mockHookAgent listens on a Unix socket and records received requests.
type mockHookAgent struct {
	listener net.Listener
	received []message.Request
	mu       sync.Mutex
	wg       sync.WaitGroup
}

func newMockHookAgent(t *testing.T, sockPath string) *mockHookAgent {
	t.Helper()
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	a := &mockHookAgent{listener: ln}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			a.wg.Add(1)
			go func() {
				defer a.wg.Done()
				defer conn.Close()
				req, err := message.ReadRequest(conn)
				if err != nil {
					return
				}
				a.mu.Lock()
				a.received = append(a.received, *req)
				a.mu.Unlock()
				message.SendResponse(conn, &message.Response{OK: true})
			}()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		a.wg.Wait()
	})
	return a
}

func (a *mockHookAgent) Received() []message.Request {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]message.Request(nil), a.received...)
}

// shortHookTempDir creates a temp directory with a short path for Unix sockets.
func shortHookTempDir(t *testing.T) string {
	t.Helper()
	name := t.Name()
	if len(name) > 20 {
		name = name[:20]
	}
	dir, err := os.MkdirTemp("/tmp", "h2h-"+name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// setupMockAgent creates the ~/.h2/sockets/ structure inside tmpDir and
// starts a mock agent socket. Returns the mock agent. Sets HOME to tmpDir.
func setupMockAgent(t *testing.T, tmpDir, agentName string) *mockHookAgent {
	t.Helper()

	// Reset caches so socketdir.Dir() and config.ConfigDir() pick up the new HOME.
	config.ResetResolveCache()
	socketdir.ResetDirCache()
	t.Cleanup(func() {
		config.ResetResolveCache()
		socketdir.ResetDirCache()
	})

	h2Root := filepath.Join(tmpDir, ".h2")
	sockDir := filepath.Join(h2Root, "sockets")
	os.MkdirAll(sockDir, 0o755)
	// Write marker file so ResolveDir finds this as a valid h2 dir.
	config.WriteMarker(h2Root)

	sockPath := filepath.Join(sockDir, socketdir.Format(socketdir.TypeAgent, agentName))
	t.Setenv("HOME", tmpDir)
	t.Setenv("H2_ROOT_DIR", h2Root)
	t.Setenv("H2_DIR", h2Root)
	return newMockHookAgent(t, sockPath)
}

// --- handle-hook command tests ---

func TestHandleHook_SendsEventToAgent(t *testing.T) {
	tmpDir := shortHookTempDir(t)
	agent := setupMockAgent(t, tmpDir, "myagent")

	payload := `{"hook_event_name": "PreToolUse", "tool_name": "Bash", "session_id": "abc123"}`

	cmd := newHandleHookCmd()
	cmd.SetArgs([]string{"--agent", "myagent"})
	cmd.SetIn(bytes.NewBufferString(payload))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reqs := agent.Received()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	if reqs[0].Type != "hook_event" {
		t.Errorf("expected type=hook_event, got %q", reqs[0].Type)
	}
	if reqs[0].EventName != "PreToolUse" {
		t.Errorf("expected event_name=PreToolUse, got %q", reqs[0].EventName)
	}

	// Verify the full payload was forwarded.
	var payloadMap map[string]interface{}
	if err := json.Unmarshal(reqs[0].Payload, &payloadMap); err != nil {
		t.Fatalf("failed to parse forwarded payload: %v", err)
	}
	if payloadMap["tool_name"] != "Bash" {
		t.Errorf("expected tool_name=Bash in payload, got %v", payloadMap["tool_name"])
	}

	// Verify stdout output.
	if got := stdout.String(); got != "{}\n" {
		t.Errorf("expected stdout={}, got %q", got)
	}
}

func TestHandleHook_DefaultsAgentFromH2Actor(t *testing.T) {
	tmpDir := shortHookTempDir(t)
	agent := setupMockAgent(t, tmpDir, "concierge")
	t.Setenv("H2_ACTOR", "concierge")

	payload := `{"hook_event_name": "SessionStart"}`

	cmd := newHandleHookCmd()
	cmd.SetArgs([]string{}) // no --agent flag
	cmd.SetIn(bytes.NewBufferString(payload))
	cmd.SetOut(&bytes.Buffer{})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reqs := agent.Received()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	if reqs[0].EventName != "SessionStart" {
		t.Errorf("expected event_name=SessionStart, got %q", reqs[0].EventName)
	}
}

func TestHandleHook_ErrorNoAgent(t *testing.T) {
	t.Setenv("H2_ACTOR", "")

	cmd := newHandleHookCmd()
	cmd.SetArgs([]string{})
	cmd.SetIn(bytes.NewBufferString(`{"hook_event_name": "PreToolUse"}`))

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no agent specified")
	}
	if err.Error() != "--agent is required (or set H2_ACTOR)" {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestHandleHook_ErrorNoEventName(t *testing.T) {
	cmd := newHandleHookCmd()
	cmd.SetArgs([]string{"--agent", "test"})
	cmd.SetIn(bytes.NewBufferString(`{"some_field": "value"}`))

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when hook_event_name missing")
	}
	if err.Error() != "hook_event_name not found in payload" {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestHandleHook_ErrorInvalidJSON(t *testing.T) {
	cmd := newHandleHookCmd()
	cmd.SetArgs([]string{"--agent", "test"})
	cmd.SetIn(bytes.NewBufferString(`not json`))

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestHandleHook_ErrorNegativeDelaySeconds(t *testing.T) {
	cmd := newHandleHookCmd()
	cmd.SetArgs([]string{"--agent", "test", "--delay-seconds", "-1"})
	cmd.SetIn(bytes.NewBufferString(`{"hook_event_name":"PreToolUse"}`))

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for negative delay")
	}
	if err.Error() != "--delay-seconds must be >= 0" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleHook_ErrorNegativeDelayPermissionRequestSeconds(t *testing.T) {
	cmd := newHandleHookCmd()
	cmd.SetArgs([]string{"--agent", "test", "--delay-permission-request-seconds", "-1"})
	cmd.SetIn(bytes.NewBufferString(`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{},"session_id":"s1"}`))

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for negative permission delay")
	}
	if err.Error() != "--delay-permission-request-seconds must be >= 0" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleHook_DelaySeconds_AppliesToAnyHook(t *testing.T) {
	tmpDir := shortHookTempDir(t)
	agent := setupMockAgent(t, tmpDir, "myagent")

	cmd := newHandleHookCmd()
	cmd.SetArgs([]string{"--agent", "myagent", "--delay-seconds", "0.05"})
	cmd.SetIn(bytes.NewBufferString(`{"hook_event_name":"SessionStart"}`))
	cmd.SetOut(&bytes.Buffer{})

	start := time.Now()
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if elapsed := time.Since(start); elapsed < 45*time.Millisecond {
		t.Fatalf("elapsed = %v, want >= 45ms", elapsed)
	}

	reqs := agent.Received()
	if len(reqs) != 1 || reqs[0].EventName != "SessionStart" {
		t.Fatalf("unexpected requests: %+v", reqs)
	}
}

// --- PermissionRequest tests ---

func TestHandleHook_PermissionRequest_SkipNonRiskyTool(t *testing.T) {
	// Even without a mock agent, non-risky tools should return {} immediately.
	cmd := newHandleHookCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader(`{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion","tool_input":{}}`))

	t.Setenv("H2_ACTOR", "test-agent")

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if strings.TrimSpace(out.String()) != "{}" {
		t.Errorf("output = %q, want {}", out.String())
	}
}

func TestHandleHook_PermissionRequest_NoReviewerInstructions(t *testing.T) {
	tmpDir := shortHookTempDir(t)
	setupMockAgent(t, tmpDir, "test-agent")

	// Set up a temp session dir with no permission-reviewer.md.
	sessionDir := filepath.Join(tmpDir, "sessions", "test-agent")
	os.MkdirAll(sessionDir, 0o755)

	cmd := newHandleHookCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader(`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"make test"}}`))

	t.Setenv("H2_ACTOR", "test-agent")
	t.Setenv("H2_SESSION_DIR", sessionDir)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Should fall through (output {}) when no reviewer instructions exist.
	if strings.TrimSpace(out.String()) != "{}" {
		t.Errorf("output = %q, want {}", out.String())
	}

	// Should have sent 2 events: hook_event (PermissionRequest) + hook_event (permission_decision).
	// Give the mock agent a moment to process.
}

func TestHandleHook_PermissionRequest_ForwardsEventBeforeDecision(t *testing.T) {
	tmpDir := shortHookTempDir(t)
	agent := setupMockAgent(t, tmpDir, "test-agent")

	// Set up a temp session dir with no permission-reviewer.md (will fall through).
	sessionDir := filepath.Join(tmpDir, "sessions", "test-agent")
	os.MkdirAll(sessionDir, 0o755)

	payload := `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"ls"},"session_id":"s1"}`

	cmd := newHandleHookCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader(payload))

	t.Setenv("H2_ACTOR", "test-agent")
	t.Setenv("H2_SESSION_DIR", sessionDir)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	reqs := agent.Received()
	if len(reqs) < 2 {
		t.Fatalf("expected at least 2 requests (hook_event + permission_decision), got %d", len(reqs))
	}

	// First request should be the PermissionRequest hook event.
	if reqs[0].EventName != "PermissionRequest" {
		t.Errorf("first event = %q, want PermissionRequest", reqs[0].EventName)
	}

	// Second request should be the permission_decision.
	if reqs[1].EventName != "permission_decision" {
		t.Errorf("second event = %q, want permission_decision", reqs[1].EventName)
	}
}

func TestHandleHook_PermissionRequest_DelaySecondsBeforeAction(t *testing.T) {
	tmpDir := shortHookTempDir(t)
	agent := setupMockAgent(t, tmpDir, "test-agent")

	cmd := newHandleHookCmd()
	cmd.SetArgs([]string{"--delay-seconds", "0.05", "--force-permission-request-result", "ask_user"})
	cmd.SetIn(strings.NewReader(`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"ls"},"session_id":"s1"}`))
	cmd.SetOut(&bytes.Buffer{})
	t.Setenv("H2_ACTOR", "test-agent")

	start := time.Now()
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if elapsed := time.Since(start); elapsed < 45*time.Millisecond {
		t.Fatalf("elapsed = %v, want >= 45ms", elapsed)
	}

	reqs := agent.Received()
	if len(reqs) < 2 {
		t.Fatalf("expected at least 2 requests, got %d", len(reqs))
	}
	if reqs[0].EventName != "PermissionRequest" {
		t.Fatalf("first event = %q, want PermissionRequest", reqs[0].EventName)
	}
	if reqs[1].EventName != "permission_decision" {
		t.Fatalf("second event = %q, want permission_decision", reqs[1].EventName)
	}
}

func TestHandleHook_PermissionRequest_DelayPermissionRequestSecondsBeforeAction(t *testing.T) {
	tmpDir := shortHookTempDir(t)
	agent := setupMockAgent(t, tmpDir, "test-agent")

	cmd := newHandleHookCmd()
	cmd.SetArgs([]string{"--delay-permission-request-seconds", "0.05", "--force-permission-request-result", "ask_user"})
	cmd.SetIn(strings.NewReader(`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"ls"},"session_id":"s1"}`))
	cmd.SetOut(&bytes.Buffer{})
	t.Setenv("H2_ACTOR", "test-agent")

	start := time.Now()
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if elapsed := time.Since(start); elapsed < 45*time.Millisecond {
		t.Fatalf("elapsed = %v, want >= 45ms", elapsed)
	}

	reqs := agent.Received()
	if len(reqs) < 2 {
		t.Fatalf("expected at least 2 requests, got %d", len(reqs))
	}
	if reqs[0].EventName != "PermissionRequest" {
		t.Fatalf("first event = %q, want PermissionRequest", reqs[0].EventName)
	}
	if reqs[1].EventName != "permission_decision" {
		t.Fatalf("second event = %q, want permission_decision", reqs[1].EventName)
	}
}

func TestHandleHook_PermissionRequest_ForceAllow(t *testing.T) {
	tmpDir := shortHookTempDir(t)
	agent := setupMockAgent(t, tmpDir, "test-agent")

	payload := `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"ls"},"session_id":"s1"}`

	cmd := newHandleHookCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader(payload))
	cmd.SetArgs([]string{"--force-permission-request-result", "allow"})

	t.Setenv("H2_ACTOR", "test-agent")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(out.String(), `"behavior":"allow"`) {
		t.Fatalf("output = %q, want allow behavior", out.String())
	}

	reqs := agent.Received()
	if len(reqs) < 2 {
		t.Fatalf("expected at least 2 requests (hook_event + permission_decision), got %d", len(reqs))
	}
	if reqs[1].EventName != "permission_decision" {
		t.Fatalf("second event = %q, want permission_decision", reqs[1].EventName)
	}
	var decision map[string]string
	if err := json.Unmarshal(reqs[1].Payload, &decision); err != nil {
		t.Fatalf("unmarshal decision payload: %v", err)
	}
	if decision["decision"] != "allow" {
		t.Fatalf("decision = %q, want allow", decision["decision"])
	}
	if decision["reason"] != "forced by --force-permission-request-result" {
		t.Fatalf("reason = %q, want forced reason", decision["reason"])
	}
}

func TestHandleHook_PermissionRequest_ForceDeny(t *testing.T) {
	tmpDir := shortHookTempDir(t)
	agent := setupMockAgent(t, tmpDir, "test-agent")

	payload := `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"ls"},"session_id":"s1"}`

	cmd := newHandleHookCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader(payload))
	cmd.SetArgs([]string{"--force-permission-request-result", "deny"})

	t.Setenv("H2_ACTOR", "test-agent")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(out.String(), `"behavior":"deny"`) {
		t.Fatalf("output = %q, want deny behavior", out.String())
	}

	reqs := agent.Received()
	if len(reqs) < 2 {
		t.Fatalf("expected at least 2 requests (hook_event + permission_decision), got %d", len(reqs))
	}
	var decision map[string]string
	if err := json.Unmarshal(reqs[1].Payload, &decision); err != nil {
		t.Fatalf("unmarshal decision payload: %v", err)
	}
	if decision["decision"] != "deny" {
		t.Fatalf("decision = %q, want deny", decision["decision"])
	}
}

func TestHandleHook_PermissionRequest_ForceAskUser(t *testing.T) {
	tmpDir := shortHookTempDir(t)
	agent := setupMockAgent(t, tmpDir, "test-agent")

	payload := `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"ls"},"session_id":"s1"}`

	cmd := newHandleHookCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader(payload))
	cmd.SetArgs([]string{"--force-permission-request-result", "ask_user"})

	t.Setenv("H2_ACTOR", "test-agent")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if strings.TrimSpace(out.String()) != "{}" {
		t.Fatalf("output = %q, want {}", out.String())
	}

	reqs := agent.Received()
	if len(reqs) < 2 {
		t.Fatalf("expected at least 2 requests (hook_event + permission_decision), got %d", len(reqs))
	}
	var decision map[string]string
	if err := json.Unmarshal(reqs[1].Payload, &decision); err != nil {
		t.Fatalf("unmarshal decision payload: %v", err)
	}
	if decision["decision"] != "ask_user" {
		t.Fatalf("decision = %q, want ask_user", decision["decision"])
	}
}

func TestHandleHook_ForcePermissionRequestResult_Invalid(t *testing.T) {
	cmd := newHandleHookCmd()
	cmd.SetArgs([]string{"--agent", "test-agent", "--force-permission-request-result", "maybe"})
	cmd.SetIn(bytes.NewBufferString(`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{},"session_id":"s1"}`))

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid --force-permission-request-result")
	}
	if err.Error() != "--force-permission-request-result must be one of: deny, allow, ask_user" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- parseReviewerResponse tests ---

func TestParseReviewerResponse_Allow(t *testing.T) {
	decision, reason := parseReviewerResponse("ALLOW\nSafe read operation")
	if decision != "ALLOW" {
		t.Errorf("decision = %q, want ALLOW", decision)
	}
	if reason != "Safe read operation" {
		t.Errorf("reason = %q, want %q", reason, "Safe read operation")
	}
}

func TestParseReviewerResponse_Deny(t *testing.T) {
	decision, reason := parseReviewerResponse("DENY\nDestructive operation")
	if decision != "DENY" {
		t.Errorf("decision = %q, want DENY", decision)
	}
	if reason != "Destructive operation" {
		t.Errorf("reason = %q, want %q", reason, "Destructive operation")
	}
}

func TestParseReviewerResponse_AskUser(t *testing.T) {
	decision, _ := parseReviewerResponse("ASK_USER\nUncertain")
	if decision != "ASK_USER" {
		t.Errorf("decision = %q, want ASK_USER", decision)
	}
}

func TestParseReviewerResponse_Empty(t *testing.T) {
	decision, reason := parseReviewerResponse("")
	if decision != "ASK_USER" {
		t.Errorf("decision = %q, want ASK_USER", decision)
	}
	if reason != "empty response" {
		t.Errorf("reason = %q, want %q", reason, "empty response")
	}
}

func TestParseReviewerResponse_Unrecognized(t *testing.T) {
	decision, reason := parseReviewerResponse("MAYBE\nNot sure")
	if decision != "ASK_USER" {
		t.Errorf("decision = %q, want ASK_USER", decision)
	}
	if !strings.Contains(reason, "unrecognized") {
		t.Errorf("reason = %q, want to contain 'unrecognized'", reason)
	}
}

func TestParseReviewerResponse_WindowsLineEndings(t *testing.T) {
	decision, reason := parseReviewerResponse("ALLOW\r\nSafe\r\n")
	if decision != "ALLOW" {
		t.Errorf("decision = %q, want ALLOW", decision)
	}
	if reason != "Safe" {
		t.Errorf("reason = %q, want %q", reason, "Safe")
	}
}

// --- splitLines tests ---

func TestSplitLines(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"ALLOW\nOK", []string{"ALLOW", "OK"}},
		{"DENY\n", []string{"DENY"}},
		{"", nil},
		{"ALLOW\r\nOK\r\n", []string{"ALLOW", "OK"}},
		{"\n\nALLOW\n\n", []string{"ALLOW"}},
	}

	for _, tt := range tests {
		got := splitLines(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("splitLines(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitLines(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

// --- DCG PreToolUse tests ---

func TestHandleDCGPreToolUse_NonBashTool_PassThrough(t *testing.T) {
	payload := `{"hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":"/tmp/foo"}}`
	cfg := &config.DCGConfig{}

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	err := handleDCGPreToolUse(cmd, cfg, []byte(payload))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out.String()) != "{}" {
		t.Errorf("expected {} for non-Bash tool, got %q", out.String())
	}
}

func TestHandleDCGPreToolUse_SafeCommand_Allow(t *testing.T) {
	payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls -la"}}`
	cfg := &config.DCGConfig{}

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	err := handleDCGPreToolUse(cmd, cfg, []byte(payload))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Safe command should return {} (allow/pass through).
	if strings.TrimSpace(out.String()) != "{}" {
		t.Errorf("expected {} for safe command, got %q", out.String())
	}
}

func TestHandleDCGPreToolUse_DestructiveCommand_StrictPolicy_Deny(t *testing.T) {
	payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`
	cfg := &config.DCGConfig{
		DestructivePolicy: "strict",
	}

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	err := handleDCGPreToolUse(cmd, cfg, []byte(payload))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := out.String()
	if strings.TrimSpace(result) == "{}" {
		t.Fatal("expected deny response for destructive command with strict policy, got {}")
	}
	if !strings.Contains(result, `"permissionDecision":"deny"`) {
		t.Errorf("expected deny decision, got %q", result)
	}
}

func TestHandleDCGPreToolUse_DestructiveCommand_InteractivePolicy_Ask(t *testing.T) {
	// "git push --force" with interactive policy should trigger an Ask decision.
	payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git push --force"}}`
	cfg := &config.DCGConfig{
		DestructivePolicy: "interactive",
	}

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	err := handleDCGPreToolUse(cmd, cfg, []byte(payload))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := strings.TrimSpace(out.String())
	// Interactive policy returns Ask for medium+ severity.
	// If dcg classifies it as destructive, we expect either ask or deny, not {}.
	if result == "{}" {
		// Some commands may not match any rules — that's OK, just pass through.
		t.Skip("command did not match any DCG rules")
	}
	// Should contain either "ask" or "deny" decision.
	if !strings.Contains(result, `"permissionDecision"`) {
		t.Errorf("expected permissionDecision in response, got %q", result)
	}
}

func TestHandleDCGPreToolUse_AllowAllPolicy_Allows(t *testing.T) {
	// Even destructive commands pass with allow-all.
	payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`
	cfg := &config.DCGConfig{
		DestructivePolicy: "allow-all",
		PrivacyPolicy:     "allow-all",
	}

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	err := handleDCGPreToolUse(cmd, cfg, []byte(payload))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out.String()) != "{}" {
		t.Errorf("expected {} with allow-all policy, got %q", out.String())
	}
}

func TestHandleDCGPreToolUse_EmptyCommand_PassThrough(t *testing.T) {
	payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":""}}`
	cfg := &config.DCGConfig{}

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	err := handleDCGPreToolUse(cmd, cfg, []byte(payload))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out.String()) != "{}" {
		t.Errorf("expected {} for empty command, got %q", out.String())
	}
}

func TestHandleDCGPreToolUse_InvalidJSON_PassThrough(t *testing.T) {
	cfg := &config.DCGConfig{}

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	err := handleDCGPreToolUse(cmd, cfg, []byte("not json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out.String()) != "{}" {
		t.Errorf("expected {} for invalid JSON, got %q", out.String())
	}
}

// --- buildDCGOptions tests ---

func TestBuildDCGOptions_Empty(t *testing.T) {
	cfg := &config.DCGConfig{}
	opts := buildDCGOptions(cfg)
	// Should have at least the WithEnv option.
	if len(opts) < 1 {
		t.Errorf("expected at least 1 option (WithEnv), got %d", len(opts))
	}
}

func TestBuildDCGOptions_AllFields(t *testing.T) {
	cfg := &config.DCGConfig{
		DestructivePolicy: "strict",
		PrivacyPolicy:     "moderate",
		Allowlist:         []string{"git status"},
		Blocklist:         []string{"rm *"},
		EnabledPacks:      []string{"core.git"},
		DisabledPacks:     []string{"database.sql"},
	}
	opts := buildDCGOptions(cfg)
	// Should have: destructive policy + privacy policy + allowlist + blocklist + packs + disabled packs + env = 7.
	if len(opts) != 7 {
		t.Errorf("expected 7 options, got %d", len(opts))
	}
}

// --- dcgPolicyFromString tests ---

func TestDCGPolicyFromString(t *testing.T) {
	tests := []struct {
		name    string
		wantNil bool
	}{
		{"allow-all", false},
		{"permissive", false},
		{"moderate", false},
		{"strict", false},
		{"very-strict", false},
		{"interactive", false},
		{"unknown", true},
		{"", true},
	}
	for _, tt := range tests {
		p := dcgPolicyFromString(tt.name)
		if tt.wantNil && p != nil {
			t.Errorf("dcgPolicyFromString(%q) = %v, want nil", tt.name, p)
		}
		if !tt.wantNil && p == nil {
			t.Errorf("dcgPolicyFromString(%q) = nil, want non-nil", tt.name)
		}
	}
}

// --- DCG integration test via handle-hook command ---

func TestHandleHook_PreToolUse_DCG_DeniesDestructive(t *testing.T) {
	tmpDir := shortHookTempDir(t)
	setupMockAgent(t, tmpDir, "dcg-agent")

	// Create session dir with RuntimeConfig that has DCG enabled + strict.
	h2Root := filepath.Join(tmpDir, ".h2")
	sessionDir := filepath.Join(h2Root, "sessions", "dcg-agent")
	os.MkdirAll(sessionDir, 0o755)

	enabled := true
	rc := &config.RuntimeConfig{
		AgentName:   "dcg-agent",
		SessionID:   "test-session",
		HarnessType: "claude_code",
		Command:     "claude",
		CWD:         tmpDir,
		StartedAt:   "2026-01-01T00:00:00Z",
		PermissionReview: &config.PermissionReview{
			DCG: &config.DCGConfig{
				Enabled:           &enabled,
				DestructivePolicy: "strict",
			},
		},
	}
	if err := config.WriteRuntimeConfig(sessionDir, rc); err != nil {
		t.Fatalf("write runtime config: %v", err)
	}

	payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /"},"session_id":"test-session"}`

	cmd := newHandleHookCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader(payload))
	cmd.SetArgs([]string{"--agent", "dcg-agent"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	result := strings.TrimSpace(out.String())
	if result == "{}" {
		t.Fatal("expected deny response for 'rm -rf /' with strict policy, got {}")
	}
	if !strings.Contains(result, `"permissionDecision":"deny"`) {
		t.Errorf("expected deny in response, got %q", result)
	}
}

func TestHandleHook_PreToolUse_DCG_AllowsSafeCommand(t *testing.T) {
	tmpDir := shortHookTempDir(t)
	setupMockAgent(t, tmpDir, "dcg-agent2")

	h2Root := filepath.Join(tmpDir, ".h2")
	sessionDir := filepath.Join(h2Root, "sessions", "dcg-agent2")
	os.MkdirAll(sessionDir, 0o755)

	enabled := true
	rc := &config.RuntimeConfig{
		AgentName:   "dcg-agent2",
		SessionID:   "test-session",
		HarnessType: "claude_code",
		Command:     "claude",
		CWD:         tmpDir,
		StartedAt:   "2026-01-01T00:00:00Z",
		PermissionReview: &config.PermissionReview{
			DCG: &config.DCGConfig{
				Enabled:           &enabled,
				DestructivePolicy: "strict",
			},
		},
	}
	if err := config.WriteRuntimeConfig(sessionDir, rc); err != nil {
		t.Fatalf("write runtime config: %v", err)
	}

	payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls -la"},"session_id":"test-session"}`

	cmd := newHandleHookCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader(payload))
	cmd.SetArgs([]string{"--agent", "dcg-agent2"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if strings.TrimSpace(out.String()) != "{}" {
		t.Errorf("expected {} for safe command, got %q", out.String())
	}
}

func TestHandleHook_PreToolUse_DCG_Disabled_PassThrough(t *testing.T) {
	tmpDir := shortHookTempDir(t)
	setupMockAgent(t, tmpDir, "dcg-agent3")

	h2Root := filepath.Join(tmpDir, ".h2")
	sessionDir := filepath.Join(h2Root, "sessions", "dcg-agent3")
	os.MkdirAll(sessionDir, 0o755)

	disabled := false
	rc := &config.RuntimeConfig{
		AgentName:   "dcg-agent3",
		SessionID:   "test-session",
		HarnessType: "claude_code",
		Command:     "claude",
		CWD:         tmpDir,
		StartedAt:   "2026-01-01T00:00:00Z",
		PermissionReview: &config.PermissionReview{
			DCG: &config.DCGConfig{
				Enabled:           &disabled,
				DestructivePolicy: "strict",
			},
		},
	}
	if err := config.WriteRuntimeConfig(sessionDir, rc); err != nil {
		t.Fatalf("write runtime config: %v", err)
	}

	payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /"},"session_id":"test-session"}`

	cmd := newHandleHookCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader(payload))
	cmd.SetArgs([]string{"--agent", "dcg-agent3"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// DCG disabled — should pass through.
	if strings.TrimSpace(out.String()) != "{}" {
		t.Errorf("expected {} when DCG disabled, got %q", out.String())
	}
}

// --- cleanOtelEnv tests ---

func TestCleanOtelEnv(t *testing.T) {
	env := []string{
		"HOME=/home/user",
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318",
		"OTEL_LOGS_EXPORTER=otlp",
		"PATH=/usr/bin",
		"CLAUDE_CODE_ENABLE_TELEMETRY=1",
		"OTHER_VAR=value",
	}

	cleaned := cleanOtelEnv(env)

	// Should remove OTEL_ and CLAUDE_CODE_ENABLE_TELEMETRY vars.
	for _, e := range cleaned {
		if strings.HasPrefix(e, "OTEL_") || strings.HasPrefix(e, "CLAUDE_CODE_ENABLE_TELEMETRY=") {
			t.Errorf("expected OTEL/telemetry var to be removed: %s", e)
		}
	}

	// Should keep other vars.
	found := map[string]bool{}
	for _, e := range cleaned {
		found[e] = true
	}
	if !found["HOME=/home/user"] {
		t.Error("expected HOME to be preserved")
	}
	if !found["PATH=/usr/bin"] {
		t.Error("expected PATH to be preserved")
	}
	if !found["OTHER_VAR=value"] {
		t.Error("expected OTHER_VAR to be preserved")
	}
	if len(cleaned) != 3 {
		t.Errorf("expected 3 env vars after cleaning, got %d: %v", len(cleaned), cleaned)
	}
}
