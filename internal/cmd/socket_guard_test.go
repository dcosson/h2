package cmd

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"h2/internal/config"
	"h2/internal/socketdir"
)

func TestEnsureAgentSocketAvailable_NoSocket(t *testing.T) {
	config.ResetResolveCache()
	socketdir.ResetDirCache()
	t.Cleanup(func() {
		config.ResetResolveCache()
		socketdir.ResetDirCache()
	})

	tmpDir := t.TempDir()
	h2Root := filepath.Join(tmpDir, ".h2")
	if err := os.MkdirAll(filepath.Join(h2Root, "sockets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteMarker(h2Root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", tmpDir)
	t.Setenv("H2_ROOT_DIR", h2Root)
	t.Setenv("H2_DIR", h2Root)

	if err := ensureAgentSocketAvailable("agent1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureAgentSocketAvailable_StaleSocketRemoved(t *testing.T) {
	config.ResetResolveCache()
	socketdir.ResetDirCache()
	t.Cleanup(func() {
		config.ResetResolveCache()
		socketdir.ResetDirCache()
	})

	tmpDir, err := os.MkdirTemp("/tmp", "h2t-guard")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	h2Root := filepath.Join(tmpDir, ".h2")
	sockDir := filepath.Join(h2Root, "sockets")
	if err := os.MkdirAll(sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteMarker(h2Root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", tmpDir)
	t.Setenv("H2_ROOT_DIR", h2Root)
	t.Setenv("H2_DIR", h2Root)

	sockPath := filepath.Join(sockDir, socketdir.Format(socketdir.TypeAgent, "agent1"))
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()

	if err := ensureAgentSocketAvailable("agent1"); err != nil {
		t.Fatalf("unexpected error probing stale socket: %v", err)
	}
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Fatalf("expected stale socket to be removed, stat err=%v", err)
	}
}

func TestEnsureAgentSocketAvailable_LiveSocketErrors(t *testing.T) {
	config.ResetResolveCache()
	socketdir.ResetDirCache()
	t.Cleanup(func() {
		config.ResetResolveCache()
		socketdir.ResetDirCache()
	})

	tmpDir, err := os.MkdirTemp("/tmp", "h2t-guard")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	h2Root := filepath.Join(tmpDir, ".h2")
	sockDir := filepath.Join(h2Root, "sockets")
	if err := os.MkdirAll(sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteMarker(h2Root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", tmpDir)
	t.Setenv("H2_ROOT_DIR", h2Root)
	t.Setenv("H2_DIR", h2Root)

	sockPath := filepath.Join(sockDir, socketdir.Format(socketdir.TypeAgent, "agent1"))
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	err = ensureAgentSocketAvailable("agent1")
	if err == nil {
		t.Fatal("expected error for live socket")
	}
	if got := err.Error(); got != `agent "agent1" is already running` {
		t.Fatalf("unexpected error: %q", got)
	}
}
