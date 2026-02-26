package cmd

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"h2/internal/config"
	"h2/internal/session/message"
	"h2/internal/socketdir"
)

func TestStopExistingBridgeIfRunning_NoSocket(t *testing.T) {
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

	stopped, err := stopExistingBridgeIfRunning("alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stopped {
		t.Fatalf("expected stopped=false when no bridge is running")
	}
}

func TestStopExistingBridgeIfRunning_StopsRunningBridge(t *testing.T) {
	config.ResetResolveCache()
	socketdir.ResetDirCache()
	t.Cleanup(func() {
		config.ResetResolveCache()
		socketdir.ResetDirCache()
	})

	// Keep socket path short for macOS unix socket path limits.
	tmpDir, err := os.MkdirTemp("/tmp", "h2t-bridge")
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

	sockPath := filepath.Join(sockDir, socketdir.Format(socketdir.TypeBridge, "alice"))
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		req, err := message.ReadRequest(conn)
		if err != nil {
			return
		}
		if req.Type != "stop" {
			return
		}
		_ = message.SendResponse(conn, &message.Response{OK: true})
		_ = ln.Close()
		_ = os.Remove(sockPath)
	}()

	stopped, err := stopExistingBridgeIfRunning("alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stopped {
		t.Fatalf("expected stopped=true for running bridge")
	}

	<-done
}
