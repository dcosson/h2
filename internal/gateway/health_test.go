package gateway

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"h2/internal/config"
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

func TestServerSessionRPCs(t *testing.T) {
	setupGatewayTestH2Dir(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	socketPath := shortSocketPath(t)
	manager := NewManager(ManagerOpts{Generation: "rpc-generation"})
	server := NewServer(ServerOpts{
		H2Dir:      config.ConfigDir(),
		SocketPath: socketPath,
		Manager:    manager,
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
		case <-time.After(3 * time.Second):
			t.Fatal("server did not stop after cancel")
		}
	})
	waitForHealth(t, socketPath)

	sessionDir := filepath.Join(config.ConfigDir(), "sessions", "rpc-agent")
	rc := gatewayTestRC("rpc-agent", "sh", []string{"-c", "sleep 30"})
	writeGatewayTestRC(t, sessionDir, rc)

	status, err := StartSession(context.Background(), socketPath, StartSessionRequest{
		SessionDir:    sessionDir,
		RuntimeConfig: rc,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if status.Agent.Name != "rpc-agent" {
		t.Fatalf("status agent = %q, want rpc-agent", status.Agent.Name)
	}
	waitForRuntimeState(t, sessionDir, GatewayRuntimeRunning)

	list, err := ListRuntime(context.Background(), socketPath)
	if err != nil {
		t.Fatalf("ListRuntime: %v", err)
	}
	if len(list) != 1 || list[0].Agent.Name != "rpc-agent" {
		t.Fatalf("list = %+v, want rpc-agent", list)
	}

	status, err = SessionStatusFor(context.Background(), socketPath, "rpc-agent")
	if err != nil {
		t.Fatalf("SessionStatusFor: %v", err)
	}
	if status.Agent.Name != "rpc-agent" {
		t.Fatalf("status agent = %q, want rpc-agent", status.Agent.Name)
	}

	if err := StopSession(context.Background(), socketPath, "rpc-agent"); err != nil {
		t.Fatalf("StopSession: %v", err)
	}
	got := waitForRuntimeState(t, sessionDir, GatewayRuntimeStopped)
	if got.GatewayDesiredState != GatewayDesiredStopped {
		t.Fatalf("desired state = %q, want stopped", got.GatewayDesiredState)
	}
}

func TestServerStopAllSessionsRPC(t *testing.T) {
	setupGatewayTestH2Dir(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	socketPath := shortSocketPath(t)
	manager := NewManager(ManagerOpts{Generation: "rpc-generation"})
	server := NewServer(ServerOpts{
		H2Dir:      config.ConfigDir(),
		SocketPath: socketPath,
		Manager:    manager,
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
		case <-time.After(3 * time.Second):
			t.Fatal("server did not stop after cancel")
		}
	})
	waitForHealth(t, socketPath)

	sessionDirA := filepath.Join(config.ConfigDir(), "sessions", "rpc-stop-all-a")
	rcA := gatewayTestRC("rpc-stop-all-a", "sh", []string{"-c", "sleep 30"})
	writeGatewayTestRC(t, sessionDirA, rcA)
	sessionDirB := filepath.Join(config.ConfigDir(), "sessions", "rpc-stop-all-b")
	rcB := gatewayTestRC("rpc-stop-all-b", "sh", []string{"-c", "sleep 30"})
	writeGatewayTestRC(t, sessionDirB, rcB)

	if _, err := StartSession(context.Background(), socketPath, StartSessionRequest{SessionDir: sessionDirA, RuntimeConfig: rcA}); err != nil {
		t.Fatalf("StartSession A: %v", err)
	}
	if _, err := StartSession(context.Background(), socketPath, StartSessionRequest{SessionDir: sessionDirB, RuntimeConfig: rcB}); err != nil {
		t.Fatalf("StartSession B: %v", err)
	}
	waitForRuntimeState(t, sessionDirA, GatewayRuntimeRunning)
	waitForRuntimeState(t, sessionDirB, GatewayRuntimeRunning)

	if err := StopAllSessions(context.Background(), socketPath); err != nil {
		t.Fatalf("StopAllSessions: %v", err)
	}
	gotA := waitForRuntimeState(t, sessionDirA, GatewayRuntimeStopped)
	gotB := waitForRuntimeState(t, sessionDirB, GatewayRuntimeStopped)
	if gotA.GatewayDesiredState != GatewayDesiredStopped || gotB.GatewayDesiredState != GatewayDesiredStopped {
		t.Fatalf("desired states = %q/%q, want stopped/stopped", gotA.GatewayDesiredState, gotB.GatewayDesiredState)
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
