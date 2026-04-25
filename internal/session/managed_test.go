package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"h2/internal/config"
	"h2/internal/socketdir"
)

func TestManagedRuntimeStartsWithoutSocketAndStops(t *testing.T) {
	setupManagedRuntimeH2Dir(t)

	rc := testRC("managed-agent", "sh", []string{"-c", "printf ready; sleep 30"})
	rc.Pod = "managed-pod"
	sessionDir := filepath.Join(config.ConfigDir(), "sessions", rc.AgentName)
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRuntimeConfig(sessionDir, rc); err != nil {
		t.Fatal(err)
	}

	runtime := NewManagedRuntime(rc, ManagedOpts{SessionDir: sessionDir})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := runtime.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForManagedChild(t, runtime)

	status := runtime.Status()
	if status.Name != "managed-agent" {
		t.Errorf("status.Name = %q", status.Name)
	}
	if status.Pod != "managed-pod" {
		t.Errorf("status.Pod = %q", status.Pod)
	}
	if _, err := os.Stat(socketdir.Path(socketdir.TypeAgent, rc.AgentName)); !os.IsNotExist(err) {
		t.Fatalf("managed runtime created agent socket or unexpected stat error: %v", err)
	}

	runtime.Stop()
	select {
	case <-runtime.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("managed runtime did not stop")
	}
}

func TestManagedRuntimeResumeSetsResumeSessionID(t *testing.T) {
	setupManagedRuntimeH2Dir(t)

	rc := testRC("managed-resume", "sh", []string{"-c", "sleep 30"})
	rc.HarnessSessionID = "harness-session"
	runtime := NewManagedRuntime(rc, ManagedOpts{SessionDir: t.TempDir(), Resume: true})
	if runtime.Session.RC.ResumeSessionID != "harness-session" {
		t.Fatalf("ResumeSessionID = %q, want harness-session", runtime.Session.RC.ResumeSessionID)
	}
}

func TestManagedRuntimeStartOnlyOnce(t *testing.T) {
	setupManagedRuntimeH2Dir(t)

	rc := testRC("managed-once", "sh", []string{"-c", "sleep 30"})
	runtime := NewManagedRuntime(rc, ManagedOpts{SessionDir: t.TempDir()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := runtime.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForManagedChild(t, runtime)
	t.Cleanup(func() {
		runtime.Stop()
		<-runtime.Done()
	})

	if err := runtime.Start(ctx); err == nil {
		t.Fatal("expected second Start to fail")
	}
}

func setupManagedRuntimeH2Dir(t *testing.T) {
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

func waitForManagedChild(t *testing.T, runtime *ManagedRuntime) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.Session.VT != nil && runtime.Session.VT.Cmd != nil && runtime.Session.VT.Cmd.Process != nil {
			return
		}
		select {
		case err := <-runtime.Done():
			t.Fatalf("managed runtime exited before child start: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatal("managed child did not start")
}
