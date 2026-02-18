package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestPermissionRequest_SkipNonRiskyTool(t *testing.T) {
	cmd := newPermissionRequestCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader(`{"tool_name":"AskUserQuestion","tool_input":{}}`))

	os.Setenv("H2_ACTOR", "test-agent")
	defer os.Unsetenv("H2_ACTOR")

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if strings.TrimSpace(out.String()) != "{}" {
		t.Errorf("output = %q, want {}", out.String())
	}
}

func TestPermissionRequest_NoReviewerInstructions(t *testing.T) {
	// Set up a temp session dir with no permission-reviewer.md.
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "sessions", "test-agent")
	os.MkdirAll(sessionDir, 0o755)

	cmd := newPermissionRequestCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"make test"}}`))

	os.Setenv("H2_ACTOR", "test-agent")
	os.Setenv("H2_SESSION_DIR", sessionDir)
	defer os.Unsetenv("H2_ACTOR")
	defer os.Unsetenv("H2_SESSION_DIR")

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Should fall through (output {}) when no reviewer instructions exist.
	if strings.TrimSpace(out.String()) != "{}" {
		t.Errorf("output = %q, want {}", out.String())
	}
}

func TestPermissionRequest_RequiresAgent(t *testing.T) {
	cmd := newPermissionRequestCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(`{"tool_name":"Bash","tool_input":{}}`))

	os.Unsetenv("H2_ACTOR")

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no agent specified")
	}
	if !strings.Contains(err.Error(), "--agent") {
		t.Errorf("error = %q, want to contain --agent", err.Error())
	}
}

// --- Force flag tests ---

func TestPermissionRequest_ForceAllow(t *testing.T) {
	cmd := newPermissionRequestCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`))
	cmd.SetArgs([]string{"--force-allow"})

	os.Setenv("H2_ACTOR", "test-agent")
	defer os.Unsetenv("H2_ACTOR")

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, `"behavior":"allow"`) {
		t.Errorf("expected allow behavior, got: %s", output)
	}
}

func TestPermissionRequest_ForceDeny(t *testing.T) {
	cmd := newPermissionRequestCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"make test"}}`))
	cmd.SetArgs([]string{"--force-deny"})

	os.Setenv("H2_ACTOR", "test-agent")
	defer os.Unsetenv("H2_ACTOR")

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, `"behavior":"deny"`) {
		t.Errorf("expected deny behavior, got: %s", output)
	}
	if !strings.Contains(output, "force-deny") {
		t.Errorf("expected reason to mention force-deny, got: %s", output)
	}
}

func TestPermissionRequest_ForceAskUser(t *testing.T) {
	cmd := newPermissionRequestCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`))
	cmd.SetArgs([]string{"--force-ask-user"})

	os.Setenv("H2_ACTOR", "test-agent")
	defer os.Unsetenv("H2_ACTOR")

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// ASK_USER returns empty JSON to fall through to user prompt.
	if strings.TrimSpace(out.String()) != "{}" {
		t.Errorf("expected {}, got: %s", out.String())
	}
}

func TestPermissionRequest_ForceAllow_SkipsNonRiskyCheck(t *testing.T) {
	// With --force-allow, even non-risky tools should get an explicit allow
	// response (not just {}), because the force flag takes precedence.
	cmd := newPermissionRequestCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader(`{"tool_name":"AskUserQuestion","tool_input":{}}`))
	cmd.SetArgs([]string{"--force-allow"})

	os.Setenv("H2_ACTOR", "test-agent")
	defer os.Unsetenv("H2_ACTOR")

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, `"behavior":"allow"`) {
		t.Errorf("force-allow should produce allow even for non-risky tools, got: %s", output)
	}
}

func TestPermissionRequest_MutuallyExclusiveFlags(t *testing.T) {
	tests := []struct {
		name  string
		flags []string
	}{
		{"allow+deny", []string{"--force-allow", "--force-deny"}},
		{"allow+ask", []string{"--force-allow", "--force-ask-user"}},
		{"deny+ask", []string{"--force-deny", "--force-ask-user"}},
		{"all three", []string{"--force-allow", "--force-deny", "--force-ask-user"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newPermissionRequestCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetIn(strings.NewReader(`{"tool_name":"Bash","tool_input":{}}`))
			cmd.SetArgs(tt.flags)

			os.Setenv("H2_ACTOR", "test-agent")
			defer os.Unsetenv("H2_ACTOR")

			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected error for mutually exclusive flags")
			}
			if !strings.Contains(err.Error(), "mutually exclusive") {
				t.Errorf("error = %q, want to contain 'mutually exclusive'", err.Error())
			}
		})
	}
}

func TestBoolCount(t *testing.T) {
	if boolCount(false, false, false) != 0 {
		t.Error("expected 0")
	}
	if boolCount(true, false, false) != 1 {
		t.Error("expected 1")
	}
	if boolCount(true, true, false) != 2 {
		t.Error("expected 2")
	}
	if boolCount(true, true, true) != 3 {
		t.Error("expected 3")
	}
}
