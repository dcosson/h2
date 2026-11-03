package cmd

import (
	"bytes"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"h2/internal/config"
	"h2/internal/session/message"
	"h2/internal/socketdir"
)

// setupBridgeTestHome points h2 at a fresh, short temp dir (macOS unix socket
// paths are length-limited) and returns its sockets directory.
func setupBridgeTestHome(t *testing.T) string {
	t.Helper()
	config.ResetResolveCache()
	socketdir.ResetDirCache()
	t.Cleanup(func() {
		config.ResetResolveCache()
		socketdir.ResetDirCache()
	})

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
	return sockDir
}

// fakeBridge serves bridge socket requests. On "stop" it acks and, if
// exitOnStop, closes and removes its socket like a real bridge.
type fakeBridge struct {
	ln         net.Listener
	sockPath   string
	pid        int
	exitOnStop bool
	stops      chan struct{}
}

func startFakeBridge(t *testing.T, sockDir, name string, pid int, exitOnStop bool) *fakeBridge {
	t.Helper()
	sockPath := filepath.Join(sockDir, socketdir.Format(socketdir.TypeBridge, name))
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	fb := &fakeBridge{ln: ln, sockPath: sockPath, pid: pid, exitOnStop: exitOnStop, stops: make(chan struct{}, 10)}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			req, err := message.ReadRequest(conn)
			if err != nil {
				conn.Close()
				continue
			}
			switch req.Type {
			case "status":
				_ = message.SendResponse(conn, &message.Response{OK: true, Bridge: &message.BridgeInfo{Name: name, PID: fb.pid}})
				conn.Close()
			case "stop":
				_ = message.SendResponse(conn, &message.Response{OK: true})
				conn.Close()
				fb.stops <- struct{}{}
				if fb.exitOnStop {
					ln.Close()
					os.Remove(sockPath)
					return
				}
			default:
				conn.Close()
			}
		}
	}()
	return fb
}

func shortenBridgeStopTimeouts(t *testing.T) {
	t.Helper()
	oldStop, oldKill := bridgeStopTimeout, bridgeKillTimeout
	bridgeStopTimeout, bridgeKillTimeout = 300*time.Millisecond, 2*time.Second
	t.Cleanup(func() { bridgeStopTimeout, bridgeKillTimeout = oldStop, oldKill })
}

func TestStopBridge_NotRunning(t *testing.T) {
	setupBridgeTestHome(t)

	stopped, err := stopBridge("alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stopped {
		t.Fatal("expected stopped=false when no bridge is running")
	}
}

func TestStopBridge_RemovesStaleSocket(t *testing.T) {
	sockDir := setupBridgeTestHome(t)
	sockPath := filepath.Join(sockDir, socketdir.Format(socketdir.TypeBridge, "alice"))
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crashed bridge: listener gone, socket file left behind.
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	if !fileExists(sockPath) {
		t.Fatal("test setup: expected stale socket file")
	}

	stopped, err := stopBridge("alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stopped {
		t.Fatal("expected stopped=false for stale socket")
	}
	if fileExists(sockPath) {
		t.Fatal("expected stale socket to be removed")
	}
}

func TestStopBridge_StopsRunningBridge(t *testing.T) {
	sockDir := setupBridgeTestHome(t)
	fb := startFakeBridge(t, sockDir, "alice", 0, true)

	stopped, err := stopBridge("alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stopped {
		t.Fatal("expected stopped=true for running bridge")
	}
	if fileExists(fb.sockPath) {
		t.Fatal("expected socket gone after stop")
	}

	// A second stop reports nothing to stop instead of claiming success.
	stopped, err = stopBridge("alice")
	if err != nil || stopped {
		t.Fatalf("second stop: stopped=%v err=%v, want false/nil", stopped, err)
	}
}

// A bridge that acks stop but never exits (e.g. wedged on a hung external
// API) must be killed, not reported as stopped while still running.
func TestStopBridge_KillsBridgeThatDoesNotExit(t *testing.T) {
	sockDir := setupBridgeTestHome(t)
	shortenBridgeStopTimeouts(t)

	proc := exec.Command("sleep", "60")
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	reaped := make(chan struct{})
	go func() { proc.Wait(); close(reaped) }()
	t.Cleanup(func() { proc.Process.Kill() })

	fb := startFakeBridge(t, sockDir, "alice", proc.Process.Pid, false)

	stopped, err := stopBridge("alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stopped {
		t.Fatal("expected stopped=true after force kill")
	}
	select {
	case <-reaped:
	case <-time.After(2 * time.Second):
		t.Fatal("bridge process was not killed")
	}
	if fileExists(fb.sockPath) {
		t.Fatal("expected socket removed after force kill")
	}
}

func TestStopBridge_WedgedBridgeWithoutPIDErrors(t *testing.T) {
	sockDir := setupBridgeTestHome(t)
	shortenBridgeStopTimeouts(t)
	startFakeBridge(t, sockDir, "alice", 0, false)

	stopped, err := stopBridge("alice")
	if err == nil || stopped {
		t.Fatalf("stopped=%v err=%v, want false and an error", stopped, err)
	}
	if !strings.Contains(err.Error(), "still running") {
		t.Errorf("error %q should say the bridge is still running", err)
	}
}

func runBridgeStopCmd(t *testing.T, args ...string) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	cmd := newBridgeStopCmd()
	cmd.SetArgs(args)
	runErr := cmd.Execute()
	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	io.Copy(&buf, r)
	if runErr != nil {
		t.Fatalf("bridge stop %v: %v", args, runErr)
	}
	return buf.String()
}

func TestBridgeStopCmd_Messages(t *testing.T) {
	sockDir := setupBridgeTestHome(t)

	if out := runBridgeStopCmd(t); !strings.Contains(out, "No bridges are running; nothing to stop.") {
		t.Errorf("unnamed, none running: got %q", out)
	}
	if out := runBridgeStopCmd(t, "alice"); !strings.Contains(out, "Bridge alice is not running; nothing to stop.") {
		t.Errorf("named, not running: got %q", out)
	}

	startFakeBridge(t, sockDir, "alice", 0, true)
	if out := runBridgeStopCmd(t, "alice"); !strings.Contains(out, "Stopped bridge alice.") {
		t.Errorf("named, running: got %q", out)
	}
	if out := runBridgeStopCmd(t, "alice"); !strings.Contains(out, "not running; nothing to stop") {
		t.Errorf("named, after stop: got %q", out)
	}

	startFakeBridge(t, sockDir, "bob", 0, true)
	if out := runBridgeStopCmd(t); !strings.Contains(out, "Stopped bridge bob.") {
		t.Errorf("unnamed, one running: got %q", out)
	}
}
