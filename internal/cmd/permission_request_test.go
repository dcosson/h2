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
