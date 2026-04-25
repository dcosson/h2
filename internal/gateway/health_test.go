package gateway

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServerHealth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	socketPath := shortSocketPath(t)
	server := NewServer(ServerOpts{
		H2Dir:      "/tmp/h2-test",
		SocketPath: socketPath,
		Version:    "test-version",
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("server Run after cancel: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("server did not stop after cancel")
		}
	})

	health := waitForHealth(t, socketPath)
	if health.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", health.PID, os.Getpid())
	}
	if health.H2Dir != "/tmp/h2-test" {
		t.Errorf("H2Dir = %q", health.H2Dir)
	}
	if health.Version != "test-version" {
		t.Errorf("Version = %q", health.Version)
	}
	if health.ProtocolVersion != ProtocolVersion {
		t.Errorf("ProtocolVersion = %d, want %d", health.ProtocolVersion, ProtocolVersion)
	}
	if health.UptimeMillis < 0 {
		t.Errorf("UptimeMillis = %d, want non-negative", health.UptimeMillis)
	}
}

func TestServerRemovesStaleGatewaySocket(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	socketPath := shortSocketPath(t)
	if err := os.WriteFile(socketPath, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	server := NewServer(ServerOpts{H2Dir: "/tmp/h2-test", SocketPath: socketPath})
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-errCh
	})

	waitForHealth(t, socketPath)
}

func TestHealthRejectsIncompatibleProtocol(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	socketPath := shortSocketPath(t)
	server := NewServer(ServerOpts{H2Dir: "/tmp/h2-test", SocketPath: socketPath})
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-errCh
	})
	waitForHealth(t, socketPath)

	_, err := HealthWithVersion(context.Background(), socketPath, ProtocolVersion+1)
	if err == nil {
		t.Fatal("expected incompatible protocol error")
	}
	if !strings.Contains(err.Error(), "protocol version") {
		t.Fatalf("error = %q, want protocol version", err.Error())
	}
}

func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(os.TempDir(), "h2gt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "gateway.sock")
}

func waitForHealth(t *testing.T, socketPath string) *Health {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		health, err := HealthCheck(context.Background(), socketPath)
		if err == nil {
			return health
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("health not ready: %v", lastErr)
	return nil
}
