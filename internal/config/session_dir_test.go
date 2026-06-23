package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindSessionDirByID(t *testing.T) {
	h2dir := t.TempDir()
	if err := WriteMarker(h2dir); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	t.Setenv("H2_DIR", h2dir)
	ResetResolveCache()
	defer ResetResolveCache()

	aDir := SessionDir("agent-a")
	bDir := SessionDir("agent-b")
	if err := os.MkdirAll(aDir, 0o755); err != nil {
		t.Fatalf("mkdir a: %v", err)
	}
	if err := os.MkdirAll(bDir, 0o755); err != nil {
		t.Fatalf("mkdir b: %v", err)
	}

	if err := WriteRuntimeConfig(aDir, &RuntimeConfig{
		AgentName: "agent-a", SessionID: "sid-a", HarnessType: "claude_code",
		Command: "claude", CWD: "/tmp", StartedAt: "2024-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("write config a: %v", err)
	}
	if err := WriteRuntimeConfig(bDir, &RuntimeConfig{
		AgentName: "agent-b", SessionID: "sid-b", HarnessType: "claude_code",
		Command: "claude", CWD: "/tmp", StartedAt: "2024-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("write config b: %v", err)
	}

	if got := FindSessionDirByID("sid-b"); got != bDir {
		t.Fatalf("FindSessionDirByID(sid-b) = %q, want %q", got, bDir)
	}
	if got := FindSessionDirByID("missing"); got != "" {
		t.Fatalf("FindSessionDirByID(missing) = %q, want empty", got)
	}
	if got := FindSessionDirByID(""); got != "" {
		t.Fatalf("FindSessionDirByID(\"\") = %q, want empty", got)
	}
}

func TestFindSessionDirByID_IgnoresBadMetadata(t *testing.T) {
	h2dir := t.TempDir()
	if err := WriteMarker(h2dir); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	t.Setenv("H2_DIR", h2dir)
	ResetResolveCache()
	defer ResetResolveCache()

	validDir := SessionDir("valid")
	badDir := SessionDir("bad")
	if err := os.MkdirAll(validDir, 0o755); err != nil {
		t.Fatalf("mkdir valid: %v", err)
	}
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("mkdir bad: %v", err)
	}

	if err := WriteRuntimeConfig(validDir, &RuntimeConfig{
		AgentName: "valid", SessionID: "sid-ok", HarnessType: "claude_code",
		Command: "claude", CWD: "/tmp", StartedAt: "2024-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("write config valid: %v", err)
	}
	badMetaPath := filepath.Join(badDir, "session.metadata.json")
	if err := os.WriteFile(badMetaPath, []byte("{"), 0o644); err != nil {
		t.Fatalf("write bad metadata: %v", err)
	}

	if got := FindSessionDirByID("sid-ok"); got != validDir {
		t.Fatalf("FindSessionDirByID(sid-ok) = %q, want %q", got, validDir)
	}
}

func TestFindSessionDirByHarnessSessionID(t *testing.T) {
	h2dir := t.TempDir()
	if err := WriteMarker(h2dir); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	t.Setenv("H2_DIR", h2dir)
	ResetResolveCache()
	defer ResetResolveCache()

	// Codex-style session: internal SessionID differs from HarnessSessionID.
	codexDir := SessionDir("codex-agent")
	// Claude-style session: SessionID == HarnessSessionID.
	claudeDir := SessionDir("claude-agent")
	for _, d := range []string{codexDir, claudeDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	if err := WriteRuntimeConfig(codexDir, &RuntimeConfig{
		AgentName: "codex-agent", SessionID: "internal-id", HarnessSessionID: "codex-conv-id",
		HarnessType: "codex", Command: "codex", CWD: "/tmp", StartedAt: "2024-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("write codex config: %v", err)
	}
	if err := WriteRuntimeConfig(claudeDir, &RuntimeConfig{
		AgentName: "claude-agent", SessionID: "claude-uuid", HarnessSessionID: "claude-uuid",
		HarnessType: "claude_code", Command: "claude", CWD: "/tmp", StartedAt: "2024-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("write claude config: %v", err)
	}

	// Matches on the harness session id, not the internal h2 session id.
	if got := FindSessionDirByHarnessSessionID("codex-conv-id"); got != codexDir {
		t.Fatalf("FindSessionDirByHarnessSessionID(codex-conv-id) = %q, want %q", got, codexDir)
	}
	if got := FindSessionDirByHarnessSessionID("claude-uuid"); got != claudeDir {
		t.Fatalf("FindSessionDirByHarnessSessionID(claude-uuid) = %q, want %q", got, claudeDir)
	}
	// The internal h2 session id must NOT match via the harness lookup.
	if got := FindSessionDirByHarnessSessionID("internal-id"); got != "" {
		t.Fatalf("FindSessionDirByHarnessSessionID(internal-id) = %q, want empty", got)
	}
	if got := FindSessionDirByHarnessSessionID("missing"); got != "" {
		t.Fatalf("FindSessionDirByHarnessSessionID(missing) = %q, want empty", got)
	}
	if got := FindSessionDirByHarnessSessionID(""); got != "" {
		t.Fatalf("FindSessionDirByHarnessSessionID(\"\") = %q, want empty", got)
	}
}
