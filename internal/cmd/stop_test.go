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

func TestStopCmd_RequiresArg(t *testing.T) {
	cmd := newStopCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no args provided")
	}
}

func TestStopCmd_NoSocket(t *testing.T) {
	config.ResetResolveCache()
	socketdir.ResetDirCache()
	t.Cleanup(func() {
		config.ResetResolveCache()
		socketdir.ResetDirCache()
	})

	// Point socket dir at an empty temp dir so Find fails.
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("H2_DIR", "")
	h2Root := filepath.Join(tmpDir, ".h2")
	os.MkdirAll(filepath.Join(h2Root, "sockets"), 0o700)
	config.WriteMarker(h2Root)

	cmd := newStopCmd()
	cmd.SetArgs([]string{"nonexistent"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing socket")
	}
}

func TestStopCmd_SendsStopRequest(t *testing.T) {
	config.ResetResolveCache()
	socketdir.ResetDirCache()
	t.Cleanup(func() {
		config.ResetResolveCache()
		socketdir.ResetDirCache()
	})

	// Use a short path to stay under macOS's ~104 byte socket path limit.
	tmpDir, err := os.MkdirTemp("/tmp", "h2t-stop")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	t.Setenv("HOME", tmpDir)
	t.Setenv("H2_DIR", "")

	h2Root := filepath.Join(tmpDir, ".h2")
	sockDir := filepath.Join(h2Root, "sockets")
	os.MkdirAll(sockDir, 0o700)
	config.WriteMarker(h2Root)

	// Create a mock socket that handles the stop request.
	sockPath := filepath.Join(sockDir, socketdir.Format(socketdir.TypeAgent, "a"))
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var received *message.Request
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
		received = req
		message.SendResponse(conn, &message.Response{OK: true})
	}()

	cmd := newStopCmd()
	cmd.SetArgs([]string{"a"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	<-done
	if received == nil {
		t.Fatal("expected to receive a request")
	}
	if received.Type != "stop" {
		t.Errorf("expected type=stop, got %q", received.Type)
	}
}
