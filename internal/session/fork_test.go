package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2/internal/config"
	"h2/internal/session/agent/harness/claude"
)

// setupForkTestH2Dir creates an isolated fake h2 directory so fork tests
// never touch the real config dir. Returns the h2 dir path.
func setupForkTestH2Dir(t *testing.T) string {
	t.Helper()
	config.ResetResolveCache()
	t.Cleanup(config.ResetResolveCache)

	h2Dir := filepath.Join(t.TempDir(), "h2")
	for _, sub := range []string{"sessions", "sockets"} {
		if err := os.MkdirAll(filepath.Join(h2Dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.WriteMarker(h2Dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("H2_DIR", h2Dir)
	return h2Dir
}

func TestGenerateForkName(t *testing.T) {
	tests := []struct {
		name   string
		parent string
		taken  map[string]bool
		want   string
	}{
		{"first fork", "fond-birch", nil, "fond-birch-fork1"},
		{"second fork", "fond-birch", map[string]bool{"fond-birch-fork1": true}, "fond-birch-fork2"},
		{"fork of a fork strips suffix", "fond-birch-fork1", map[string]bool{"fond-birch-fork1": true}, "fond-birch-fork2"},
		{"fork of a fork with gap", "fond-birch-fork3", map[string]bool{"fond-birch-fork3": true, "fond-birch-fork1": true, "fond-birch-fork2": true}, "fond-birch-fork4"},
		{"fork-like name that isn't a suffix", "forklift", nil, "forklift-fork1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateForkName(tt.parent, func(name string) bool { return tt.taken[name] })
			if got != tt.want {
				t.Errorf("GenerateForkName(%q) = %q, want %q", tt.parent, got, tt.want)
			}
		})
	}
}

// writeForkParentSession creates a parent session dir + metadata + a native
// claude session log, returning the parent RuntimeConfig.
func writeForkParentSession(t *testing.T, h2Dir, name string) *config.RuntimeConfig {
	t.Helper()
	cwd := "/Users/testuser/projects/myapp"
	configPrefix := filepath.Join(h2Dir, "claude-config")
	parentID := "11111111-1111-1111-1111-111111111111"

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
		RoleName:                "coder",
		Pod:                     "my-pod",
		PodIndex:                2,
		StartedAt:               "2026-01-01T00:00:00Z",
	}

	sessionDir := config.SessionDir(name)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRuntimeConfig(sessionDir, rc); err != nil {
		t.Fatal(err)
	}

	logPath := rc.NativeSessionLogPath()
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	logContent := `{"sessionId":"` + parentID + `","type":"user","message":"hello"}` + "\n" +
		`{"sessionId":"` + parentID + `","type":"assistant","message":"hi"}` + "\n"
	if err := os.WriteFile(logPath, []byte(logContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return rc
}

func TestForkSessionFiles_Success(t *testing.T) {
	h2Dir := setupForkTestH2Dir(t)
	parent := writeForkParentSession(t, h2Dir, "fond-birch")

	forked, forkedDir, err := ForkSessionFiles(parent, "")
	if err != nil {
		t.Fatalf("ForkSessionFiles: %v", err)
	}

	// Name and identity.
	if forked.AgentName != "fond-birch-fork1" {
		t.Errorf("AgentName = %q, want fond-birch-fork1", forked.AgentName)
	}
	if forked.SessionID == parent.SessionID || forked.SessionID == "" {
		t.Errorf("SessionID = %q, want new non-empty id", forked.SessionID)
	}
	if forked.HarnessSessionID != forked.SessionID {
		t.Errorf("HarnessSessionID = %q, want == SessionID %q", forked.HarnessSessionID, forked.SessionID)
	}

	// Pod membership is not inherited.
	if forked.Pod != "" || forked.PodIndex != 0 {
		t.Errorf("Pod/PodIndex = %q/%d, want empty/0", forked.Pod, forked.PodIndex)
	}

	// Everything else carries over.
	if forked.RoleName != parent.RoleName || forked.CWD != parent.CWD || forked.Profile != parent.Profile {
		t.Errorf("role/cwd/profile not carried over: %+v", forked)
	}

	// Metadata written to the new session dir.
	if forkedDir != config.SessionDir("fond-birch-fork1") {
		t.Errorf("forkedDir = %q, want %q", forkedDir, config.SessionDir("fond-birch-fork1"))
	}
	onDisk, err := config.ReadRuntimeConfig(forkedDir)
	if err != nil {
		t.Fatalf("read forked runtime config: %v", err)
	}
	if onDisk.AgentName != forked.AgentName || onDisk.SessionID != forked.SessionID {
		t.Errorf("on-disk config mismatch: %+v", onDisk)
	}

	// The native log was copied to the new session id with ids rewritten.
	newLog, err := os.ReadFile(forked.NativeSessionLogPath())
	if err != nil {
		t.Fatalf("read forked session log: %v", err)
	}
	if strings.Contains(string(newLog), parent.HarnessSessionID) {
		t.Error("forked log still contains parent session id")
	}
	if got := strings.Count(string(newLog), forked.HarnessSessionID); got != 2 {
		t.Errorf("forked log contains new session id %d times, want 2", got)
	}

	// The parent log is untouched.
	oldLog, err := os.ReadFile(parent.NativeSessionLogPath())
	if err != nil {
		t.Fatalf("parent session log missing after fork: %v", err)
	}
	if !strings.Contains(string(oldLog), parent.HarnessSessionID) {
		t.Error("parent log was modified by fork")
	}
}

func TestForkSessionFiles_SecondForkIncrementsName(t *testing.T) {
	h2Dir := setupForkTestH2Dir(t)
	parent := writeForkParentSession(t, h2Dir, "fond-birch")

	first, _, err := ForkSessionFiles(parent, "")
	if err != nil {
		t.Fatalf("first fork: %v", err)
	}
	second, _, err := ForkSessionFiles(parent, "")
	if err != nil {
		t.Fatalf("second fork: %v", err)
	}
	if first.AgentName != "fond-birch-fork1" || second.AgentName != "fond-birch-fork2" {
		t.Errorf("fork names = %q, %q; want fond-birch-fork1, fond-birch-fork2", first.AgentName, second.AgentName)
	}
}

func TestForkSessionFiles_ForkOfForkSharesBaseName(t *testing.T) {
	h2Dir := setupForkTestH2Dir(t)
	parent := writeForkParentSession(t, h2Dir, "fond-birch")

	first, _, err := ForkSessionFiles(parent, "")
	if err != nil {
		t.Fatalf("first fork: %v", err)
	}
	grandchild, _, err := ForkSessionFiles(first, "")
	if err != nil {
		t.Fatalf("fork of fork: %v", err)
	}
	if grandchild.AgentName != "fond-birch-fork2" {
		t.Errorf("fork of fork name = %q, want fond-birch-fork2", grandchild.AgentName)
	}
}

func TestForkSessionFiles_ExplicitName(t *testing.T) {
	h2Dir := setupForkTestH2Dir(t)
	parent := writeForkParentSession(t, h2Dir, "fond-birch")

	forked, forkedDir, err := ForkSessionFiles(parent, "my-custom-fork")
	if err != nil {
		t.Fatalf("ForkSessionFiles: %v", err)
	}
	if forked.AgentName != "my-custom-fork" {
		t.Errorf("AgentName = %q, want my-custom-fork", forked.AgentName)
	}
	if forkedDir != config.SessionDir("my-custom-fork") {
		t.Errorf("forkedDir = %q, want %q", forkedDir, config.SessionDir("my-custom-fork"))
	}
}

func TestForkSessionFiles_ExplicitNameTaken(t *testing.T) {
	h2Dir := setupForkTestH2Dir(t)
	parent := writeForkParentSession(t, h2Dir, "fond-birch")
	writeForkParentSession(t, h2Dir, "already-here")

	_, _, err := ForkSessionFiles(parent, "already-here")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("err = %v, want 'already exists'", err)
	}
}

func TestForkSessionFiles_MissingNativeLogComputesSuffix(t *testing.T) {
	// Older sessions may not have NativeLogPathSuffix persisted; fork should
	// recompute it from CWD + HarnessSessionID (same as rotate does).
	h2Dir := setupForkTestH2Dir(t)
	parent := writeForkParentSession(t, h2Dir, "fond-birch")
	parent.NativeLogPathSuffix = ""

	forked, _, err := ForkSessionFiles(parent, "")
	if err != nil {
		t.Fatalf("ForkSessionFiles: %v", err)
	}
	if _, err := os.Stat(forked.NativeSessionLogPath()); err != nil {
		t.Errorf("forked session log not created: %v", err)
	}
}

func TestForkSessionFiles_Errors(t *testing.T) {
	h2Dir := setupForkTestH2Dir(t)

	t.Run("unsupported harness", func(t *testing.T) {
		parent := writeForkParentSession(t, h2Dir, "codex-agent")
		parent.HarnessType = "codex"
		if _, _, err := ForkSessionFiles(parent, ""); err == nil || !strings.Contains(err.Error(), "not supported") {
			t.Errorf("err = %v, want 'not supported'", err)
		}
	})

	t.Run("no harness session id", func(t *testing.T) {
		parent := writeForkParentSession(t, h2Dir, "no-hsid-agent")
		parent.HarnessSessionID = ""
		if _, _, err := ForkSessionFiles(parent, ""); err == nil || !strings.Contains(err.Error(), "harness_session_id") {
			t.Errorf("err = %v, want 'harness_session_id'", err)
		}
	})

	t.Run("no session log", func(t *testing.T) {
		parent := writeForkParentSession(t, h2Dir, "no-log-agent")
		if err := os.Remove(parent.NativeSessionLogPath()); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ForkSessionFiles(parent, ""); err == nil || !strings.Contains(err.Error(), "session log") {
			t.Errorf("err = %v, want 'session log'", err)
		}
	})
}
