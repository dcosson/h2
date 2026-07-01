package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2/internal/config"
	"h2/internal/session"
	"h2/internal/session/agent/harness/claude"
)

// writeForkTestSession creates a session dir + metadata + native claude log
// for a fork test parent agent. Returns the parent RuntimeConfig.
func writeForkTestSession(t *testing.T, h2Dir, name string) *config.RuntimeConfig {
	t.Helper()
	cwd := "/Users/testuser/projects/myapp"
	configPrefix := filepath.Join(h2Dir, "claude-config")
	parentID := "22222222-2222-2222-2222-222222222222"

	rc := &config.RuntimeConfig{
		AgentName:               name,
		SessionID:               parentID,
		HarnessSessionID:        parentID,
		HarnessType:             "claude_code",
		HarnessConfigPathPrefix: configPrefix,
		Profile:                 "default",
		NativeLogPathSuffix:     claude.NativeLogPathSuffix(cwd, parentID),
		Command:                 "claude",
		CWD:                     cwd,
		Pod:                     "my-pod",
		StartedAt:               "2026-01-01T00:00:00Z",
	}
	writeTestRuntimeConfig(t, name, rc)

	logPath := rc.NativeSessionLogPath()
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	logContent := `{"sessionId":"` + parentID + `","type":"user"}` + "\n"
	if err := os.WriteFile(logPath, []byte(logContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return rc
}

func TestFork_NoSession(t *testing.T) {
	setupRotateTestH2Dir(t)

	cmd := newForkCmd()
	cmd.SetArgs([]string{"nonexistent-fork-agent"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "no session found") {
		t.Errorf("err = %v, want 'no session found'", err)
	}
}

func TestFork_Success(t *testing.T) {
	h2Dir := setupRotateTestH2Dir(t)
	parent := writeForkTestSession(t, h2Dir, "fork-cli-parent")

	var forkedSessionDir string
	var forkedResume bool
	origFork := forkDaemonFunc
	forkDaemonFunc = func(sd string, hints session.TerminalHints, resume bool) error {
		forkedSessionDir = sd
		forkedResume = resume
		return nil
	}
	defer func() { forkDaemonFunc = origFork }()

	cmd := newForkCmd()
	cmd.SetArgs([]string{"fork-cli-parent", "--detach"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("fork: %v", err)
	}

	if forkedSessionDir != config.SessionDir("fork-cli-parent-fork1") {
		t.Errorf("daemon launched with session dir %q, want fork-cli-parent-fork1's", forkedSessionDir)
	}
	if !forkedResume {
		t.Error("forked daemon should be launched with resume=true")
	}

	forked, err := config.ReadRuntimeConfig(forkedSessionDir)
	if err != nil {
		t.Fatalf("read forked config: %v", err)
	}
	if forked.Pod != "" {
		t.Errorf("forked Pod = %q, want empty", forked.Pod)
	}
	if forked.HarnessSessionID == parent.HarnessSessionID {
		t.Error("forked session should have a new harness session id")
	}
	if _, err := os.Stat(forked.NativeSessionLogPath()); err != nil {
		t.Errorf("forked session log missing: %v", err)
	}
}

func TestFork_UnsupportedHarness(t *testing.T) {
	h2Dir := setupRotateTestH2Dir(t)
	rc := writeForkTestSession(t, h2Dir, "fork-cli-codex")
	rc.HarnessType = "codex"
	writeTestRuntimeConfig(t, "fork-cli-codex", rc)

	cmd := newForkCmd()
	cmd.SetArgs([]string{"fork-cli-codex", "--detach"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("err = %v, want 'not supported'", err)
	}
}

func TestParseSwitchControl(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{"switch frame", `{"type":"switch","name":"fond-birch-fork1"}`, "fond-birch-fork1"},
		{"resize frame", `{"type":"resize","cols":80,"rows":24}`, ""},
		{"empty name", `{"type":"switch"}`, ""},
		{"garbage", `not-json`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseSwitchControl([]byte(tt.payload)); got != tt.want {
				t.Errorf("parseSwitchControl(%q) = %q, want %q", tt.payload, got, tt.want)
			}
		})
	}
}
