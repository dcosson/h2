package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2/internal/config"
)

// rolloutMeta writes a rollout file whose session_meta record carries the given
// fields, mirroring what Codex writes at conversation start.
func writeRollout(t *testing.T, configDir, convID string, payload map[string]any) string {
	t.Helper()
	logDir := filepath.Join(configDir, "sessions", "2026", "08", "09")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload["id"] = convID
	line, err := json.Marshal(map[string]any{
		"timestamp": "2026-08-09T04:29:27.280Z",
		"type":      "session_meta",
		"payload":   payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(logDir, "rollout-2026-08-09T04-29-27-"+convID+".jsonl")
	if err := os.WriteFile(path, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// noRolloutRetries makes the "rollout file missing" path return immediately so
// tests don't pay the real launch-race backoff.
func noRolloutRetries(t *testing.T) {
	t.Helper()
	prev := rolloutMetaRetries
	rolloutMetaRetries = 0
	t.Cleanup(func() { rolloutMetaRetries = prev })
}

func TestResolveResumeTarget_TopLevelUnchanged(t *testing.T) {
	configDir := t.TempDir()
	writeRollout(t, configDir, "root-1", map[string]any{"session_id": "root-1", "thread_source": "user"})

	if got := resolveResumeTarget(configDir, "root-1"); got != "root-1" {
		t.Errorf("resolveResumeTarget = %q, want %q", got, "root-1")
	}
}

// A session resumed by Codex forks a new conversation: same lineage, a new ID,
// forked_from_id set, and no sub-agent markers. It must resume as-is.
func TestResolveResumeTarget_ResumeForkUnchanged(t *testing.T) {
	configDir := t.TempDir()
	writeRollout(t, configDir, "fork-1", map[string]any{
		"session_id":     "fork-1",
		"forked_from_id": "root-1",
		"thread_source":  "user",
	})

	if got := resolveResumeTarget(configDir, "fork-1"); got != "fork-1" {
		t.Errorf("resolveResumeTarget = %q, want %q", got, "fork-1")
	}
}

// The bug this repairs: an older h2 build recorded a sub-agent's conversation as
// the agent's session identity, so relaunch dropped the user into the child.
func TestResolveResumeTarget_SubagentWalksToParent(t *testing.T) {
	configDir := t.TempDir()
	writeRollout(t, configDir, "root-1", map[string]any{"session_id": "root-1", "thread_source": "user"})
	writeRollout(t, configDir, "child-1", map[string]any{
		"session_id":       "root-1",
		"parent_thread_id": "root-1",
		"thread_source":    "subagent",
	})

	if got := resolveResumeTarget(configDir, "child-1"); got != "root-1" {
		t.Errorf("resolveResumeTarget = %q, want the top-level parent %q", got, "root-1")
	}
}

func TestResolveResumeTarget_NestedSubagentWalksToTopLevel(t *testing.T) {
	configDir := t.TempDir()
	writeRollout(t, configDir, "root-1", map[string]any{"session_id": "root-1", "thread_source": "user"})
	writeRollout(t, configDir, "child-1", map[string]any{
		"session_id":       "root-1",
		"parent_thread_id": "root-1",
		"thread_source":    "subagent",
	})
	writeRollout(t, configDir, "grandchild-1", map[string]any{
		"session_id":       "root-1",
		"parent_thread_id": "child-1",
		"thread_source":    "subagent",
	})

	if got := resolveResumeTarget(configDir, "grandchild-1"); got != "root-1" {
		t.Errorf("resolveResumeTarget = %q, want the top-level ancestor %q", got, "root-1")
	}
}

// A sub-agent whose parent rollout has been deleted cannot be resumed at all;
// starting fresh beats handing the user a dead child session.
func TestResolveResumeTarget_SubagentWithoutParentStartsFresh(t *testing.T) {
	noRolloutRetries(t)
	configDir := t.TempDir()
	writeRollout(t, configDir, "child-1", map[string]any{
		"session_id":    "root-1",
		"thread_source": "subagent",
	})

	if got := resolveResumeTarget(configDir, "child-1"); got != "" {
		t.Errorf("resolveResumeTarget = %q, want empty (start fresh)", got)
	}
}

func TestResolveResumeTarget_UnknownRolloutUnchanged(t *testing.T) {
	noRolloutRetries(t)
	configDir := t.TempDir()

	if got := resolveResumeTarget(configDir, "gone-1"); got != "gone-1" {
		t.Errorf("resolveResumeTarget = %q, want the ID unchanged", got)
	}
}

// Codex marks sub-agents three ways; any one of them is enough.
func TestCodexRolloutMeta_IsSubagent(t *testing.T) {
	tests := []struct {
		name string
		meta codexRolloutMeta
		want bool
	}{
		{"thread_source", codexRolloutMeta{ThreadSource: "subagent"}, true},
		{"parent_thread_id", codexRolloutMeta{ParentThreadID: "root-1"}, true},
		{"source object", codexRolloutMeta{Source: json.RawMessage(`{"subagent":{"thread_spawn":{"depth":1}}}`)}, true},
		{"top-level cli source", codexRolloutMeta{ThreadSource: "user", Source: json.RawMessage(`"cli"`)}, false},
		{"resume fork", codexRolloutMeta{ThreadSource: "user", ForkedFromID: "root-1"}, false},
		{"older codex without thread_source", codexRolloutMeta{}, false},
	}
	for _, tt := range tests {
		if got := tt.meta.isSubagent(); got != tt.want {
			t.Errorf("%s: isSubagent() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func newTestHarness(t *testing.T, prefix string, rc *config.RuntimeConfig) *CodexHarness {
	t.Helper()
	rc.HarnessType = "codex"
	rc.Command = "codex"
	rc.HarnessConfigPathPrefix = prefix
	rc.Profile = "default"
	if rc.AgentName == "" {
		rc.AgentName = "test"
	}
	return New(rc, nil)
}

func TestBuildCommandArgs_ResumeRepairsSubagentID(t *testing.T) {
	prefix := t.TempDir()
	configDir := filepath.Join(prefix, "default")
	writeRollout(t, configDir, "root-1", map[string]any{"session_id": "root-1", "thread_source": "user"})
	writeRollout(t, configDir, "child-1", map[string]any{
		"session_id":       "root-1",
		"parent_thread_id": "root-1",
		"thread_source":    "subagent",
	})

	h := newTestHarness(t, prefix, &config.RuntimeConfig{ResumeSessionID: "child-1"})
	args := h.BuildCommandArgs(nil, nil)

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "resume root-1") {
		t.Errorf("args = %v, want a resume of the top-level parent", args)
	}
	if strings.Contains(joined, "child-1") {
		t.Errorf("args = %v, must not resume the sub-agent conversation", args)
	}
}

// When the stored ID is unrepairable, launch a fresh session — including the
// role instructions a fresh session needs.
func TestBuildCommandArgs_UnrepairableResumeStartsFresh(t *testing.T) {
	noRolloutRetries(t)
	prefix := t.TempDir()
	configDir := filepath.Join(prefix, "default")
	writeRollout(t, configDir, "child-1", map[string]any{
		"session_id":    "root-1",
		"thread_source": "subagent",
	})

	h := newTestHarness(t, prefix, &config.RuntimeConfig{
		ResumeSessionID: "child-1",
		Instructions:    "You are a test agent.",
	})
	args := h.BuildCommandArgs(nil, nil)

	joined := strings.Join(args, " ")
	if strings.Contains(joined, "resume") {
		t.Errorf("args = %v, want no resume for an unrepairable session", args)
	}
	if !strings.Contains(joined, "instructions=") {
		t.Errorf("args = %v, want instructions passed to the fresh session", args)
	}
}
