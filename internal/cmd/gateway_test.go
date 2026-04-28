package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"h2/internal/config"
	"h2/internal/gateway"
	"h2/internal/socketdir"
)

func TestGatewayStatusCmd(t *testing.T) {
	h2Root := setupGatewayCmdH2Dir(t)
	socketPath := socketdir.GatewayPath()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := gateway.NewServer(gateway.ServerOpts{
		H2Dir:      h2Root,
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
		case <-errCh:
		case <-time.After(2 * time.Second):
			t.Fatal("gateway server did not stop")
		}
	})
	waitForGatewayCmdHealth(t, socketPath)

	var out bytes.Buffer
	cmd := newGatewayStatusCmd()
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("gateway status: %v", err)
	}

	var health gateway.Health
	if err := json.Unmarshal(out.Bytes(), &health); err != nil {
		t.Fatalf("parse status output %q: %v", out.String(), err)
	}
	if health.H2Dir != h2Root {
		t.Errorf("H2Dir = %q, want %q", health.H2Dir, h2Root)
	}
	if health.Version != "test-version" {
		t.Errorf("Version = %q", health.Version)
	}
}

func TestGatewayStartCmdUsesExistingGateway(t *testing.T) {
	h2Root := setupGatewayCmdH2Dir(t)
	socketPath := socketdir.GatewayPath()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := gateway.NewServer(gateway.ServerOpts{
		H2Dir:      h2Root,
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
		case <-errCh:
		case <-time.After(2 * time.Second):
			t.Fatal("gateway server did not stop")
		}
	})
	waitForGatewayCmdHealth(t, socketPath)

	var out bytes.Buffer
	cmd := newGatewayStartCmd()
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("gateway start: %v", err)
	}

	var health gateway.Health
	if err := json.Unmarshal(out.Bytes(), &health); err != nil {
		t.Fatalf("parse start output %q: %v", out.String(), err)
	}
	if health.H2Dir != h2Root {
		t.Errorf("H2Dir = %q, want %q", health.H2Dir, h2Root)
	}
	if health.Version != "test-version" {
		t.Errorf("Version = %q", health.Version)
	}
}

func TestGatewayStatusCmdNoGateway(t *testing.T) {
	setupGatewayCmdH2Dir(t)

	cmd := newGatewayStatusCmd()
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected gateway status error")
	}
	if !strings.Contains(err.Error(), "gateway status") {
		t.Fatalf("error = %q, want gateway status context", err.Error())
	}
}

func setupGatewayCmdH2Dir(t *testing.T) string {
	t.Helper()
	config.ResetResolveCache()
	socketdir.ResetDirCache()
	t.Cleanup(func() {
		config.ResetResolveCache()
		socketdir.ResetDirCache()
	})

	tmpDir, err := os.MkdirTemp(os.TempDir(), "h2t-gateway-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	h2Root := filepath.Join(tmpDir, ".h2")
	if err := os.MkdirAll(filepath.Join(h2Root, "sockets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteMarker(h2Root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("H2_DIR", h2Root)
	t.Setenv("HOME", tmpDir)
	t.Setenv("H2_ROOT_DIR", h2Root)
	return h2Root
}

func waitForGatewayCmdHealth(t *testing.T, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if _, err := gateway.HealthCheck(context.Background(), socketPath); err == nil {
			return
		} else {
			lastErr = err
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("gateway did not become healthy: %v", lastErr)
}
