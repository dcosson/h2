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
