package gateway

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"h2/internal/config"
	"h2/internal/socketdir"
)

func TestManagerStartSessionPersistsRunningState(t *testing.T) {
	setupGatewayTestH2Dir(t)

	sessionDir := filepath.Join(config.ConfigDir(), "sessions", "managed-a")
	rc := gatewayTestRC("managed-a", "sh", []string{"-c", "sleep 30"})
	writeGatewayTestRC(t, sessionDir, rc)

	manager := NewManager(ManagerOpts{Generation: "test-generation"})
	status, err := manager.StartSession(StartSessionRequest{
		SessionDir:    sessionDir,
		RuntimeConfig: rc,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() {
		shutdownManager(t, manager)
	})
	if status.Agent.Name != "managed-a" {
		t.Fatalf("status agent = %q, want managed-a", status.Agent.Name)
	}

	got := waitForRuntimeState(t, sessionDir, GatewayRuntimeRunning)
	if got.GatewayDesiredState != GatewayDesiredRunning {
		t.Fatalf("desired state = %q, want running", got.GatewayDesiredState)
	}
	if got.GatewayGeneration != "test-generation" {
		t.Fatalf("generation = %q, want test-generation", got.GatewayGeneration)
	}
	if got.GatewayPID != os.Getpid() {
		t.Fatalf("gateway pid = %d, want %d", got.GatewayPID, os.Getpid())
	}
	if got.ChildPID == 0 {
		t.Fatal("expected child pid to be persisted")
	}
}

func TestManagerStopSessionMarksDesiredStopped(t *testing.T) {
	setupGatewayTestH2Dir(t)

	sessionDir := filepath.Join(config.ConfigDir(), "sessions", "managed-stop")
	rc := gatewayTestRC("managed-stop", "sh", []string{"-c", "sleep 30"})
	writeGatewayTestRC(t, sessionDir, rc)

	manager := NewManager(ManagerOpts{Generation: "test-generation"})
	if _, err := manager.StartSession(StartSessionRequest{SessionDir: sessionDir, RuntimeConfig: rc}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	waitForRuntimeState(t, sessionDir, GatewayRuntimeRunning)

	if err := manager.StopSession("managed-stop"); err != nil {
		t.Fatalf("StopSession: %v", err)
	}
	got := waitForRuntimeState(t, sessionDir, GatewayRuntimeStopped)
	if got.GatewayDesiredState != GatewayDesiredStopped {
		t.Fatalf("desired state = %q, want stopped", got.GatewayDesiredState)
	}
	if got.LastExitReason != "user_stop" {
		t.Fatalf("last exit = %q, want user_stop", got.LastExitReason)
	}
}

func TestManagerChildExitKeepsDesiredRunning(t *testing.T) {
	setupGatewayTestH2Dir(t)

	sessionDir := filepath.Join(config.ConfigDir(), "sessions", "managed-exit")
	rc := gatewayTestRC("managed-exit", "sh", []string{"-c", "exit 7"})
	writeGatewayTestRC(t, sessionDir, rc)

	manager := NewManager(ManagerOpts{Generation: "test-generation"})
	if _, err := manager.StartSession(StartSessionRequest{SessionDir: sessionDir, RuntimeConfig: rc}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() {
		_ = manager.StopSession("managed-exit")
		shutdownManager(t, manager)
	})

	got := waitForRuntimeState(t, sessionDir, GatewayRuntimeExited)
	if got.GatewayDesiredState != GatewayDesiredRunning {
		t.Fatalf("desired state = %q, want running", got.GatewayDesiredState)
	}
	if got.LastExitReason == "" {
		t.Fatal("expected child exit reason to be persisted")
	}
}

func TestManagerRejectsDuplicateManagedSession(t *testing.T) {
	setupGatewayTestH2Dir(t)

	sessionDir := filepath.Join(config.ConfigDir(), "sessions", "managed-dup")
	rc := gatewayTestRC("managed-dup", "sh", []string{"-c", "sleep 30"})
	writeGatewayTestRC(t, sessionDir, rc)

	manager := NewManager(ManagerOpts{Generation: "test-generation"})
	if _, err := manager.StartSession(StartSessionRequest{SessionDir: sessionDir, RuntimeConfig: rc}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() {
		shutdownManager(t, manager)
	})

	if _, err := manager.StartSession(StartSessionRequest{SessionDir: sessionDir, RuntimeConfig: rc}); err == nil {
		t.Fatal("expected duplicate StartSession to fail")
	}
}

func setupGatewayTestH2Dir(t *testing.T) {
	t.Helper()
	config.ResetResolveCache()
	socketdir.ResetDirCache()
	t.Cleanup(func() {
		config.ResetResolveCache()
		socketdir.ResetDirCache()
	})

	root := filepath.Join(t.TempDir(), ".h2")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteMarker(root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("H2_DIR", root)
}

func gatewayTestRC(name, command string, args []string) *config.RuntimeConfig {
	return &config.RuntimeConfig{
		AgentName:   name,
		SessionID:   name + "-session",
		HarnessType: "generic",
		Command:     command,
		Args:        args,
		CWD:         os.TempDir(),
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
	}
}

func writeGatewayTestRC(t *testing.T, sessionDir string, rc *config.RuntimeConfig) {
	t.Helper()
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRuntimeConfig(sessionDir, rc); err != nil {
		t.Fatal(err)
	}
}

func waitForRuntimeState(t *testing.T, sessionDir, want string) *config.RuntimeConfig {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last *config.RuntimeConfig
	var lastErr error
	for time.Now().Before(deadline) {
		rc, err := config.ReadRuntimeConfig(sessionDir)
		if err == nil {
			last = rc
			if rc.GatewayRuntimeState == want {
				return rc
			}
		} else {
			lastErr = err
		}
		time.Sleep(10 * time.Millisecond)
	}
	if last != nil {
		t.Fatalf("runtime state = %q, want %q (metadata: %+v)", last.GatewayRuntimeState, want, last)
	}
	t.Fatalf("runtime state %q not observed: %v", want, lastErr)
	return nil
}

func shutdownManager(t *testing.T, manager *Manager) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
