package cmd

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"h2/internal/config"
	"h2/internal/session/message"
	"h2/internal/socketdir"
)

func TestParseLsofPIDsForSocket(t *testing.T) {
	const sockPath = "/tmp/bridge.alice.sock"
	raw := "p101\nn/tmp/other.sock\np202\nn" + sockPath + "\np202\nn" + sockPath + "\np303\nn/tmp/x.sock\n"
	got := parseLsofPIDsForSocket(raw, sockPath)
	want := []int{202}
	if !slices.Equal(got, want) {
		t.Fatalf("parseLsofPIDsForSocket = %v, want %v", got, want)
	}
}

func TestStopExistingBridgeIfRunning_NoSocketNoPIDs(t *testing.T) {
	restore := mockBridgeCleanupDeps()
	defer restore()

	sendBridgeStopFunc = func(_ string) (bool, error) { return false, nil }
	listSocketPIDsFunc = func(_ string) ([]int, error) { return nil, nil }

	stopped, err := stopExistingBridgeIfRunning("alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stopped {
		t.Fatalf("expected stopped=false")
	}
}

func TestStopExistingBridgeIfRunning_GracefulStopSettlesWithoutKill(t *testing.T) {
	restore := mockBridgeCleanupDeps()
	defer restore()

	bridgeSettleTimeout = 20 * time.Millisecond
	bridgePollInterval = 5 * time.Millisecond
	bridgePersistenceChecks = 2

	sendBridgeStopFunc = func(_ string) (bool, error) { return true, nil }
	// First sample still sees PID, then process exits naturally.
	calls := 0
	listSocketPIDsFunc = func(_ string) ([]int, error) {
		calls++
		if calls == 1 {
			return []int{111}, nil
		}
		return nil, nil
	}
	processCommandFunc = func(pid int) (string, error) {
		return fmt.Sprintf("h2 _bridge-service --for alice --pid %d", pid), nil
	}
	killed := false
	signalProcessFunc = func(_ int, _ syscall.Signal) error {
		killed = true
		return nil
	}

	stopped, err := stopExistingBridgeIfRunning("alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stopped {
		t.Fatalf("expected stopped=true when graceful stop was sent")
	}
	if killed {
		t.Fatal("expected no signal sends when process settles naturally")
	}
}

func TestStopExistingBridgeIfRunning_KillsPersistentPIDs(t *testing.T) {
	restore := mockBridgeCleanupDeps()
	defer restore()

	bridgeSettleTimeout = 20 * time.Millisecond
	bridgePollInterval = 5 * time.Millisecond
	bridgePersistenceChecks = 2
	bridgeTermWaitTimeout = 20 * time.Millisecond

	sendBridgeStopFunc = func(_ string) (bool, error) { return false, nil }

	alive := true
	listSocketPIDsFunc = func(_ string) ([]int, error) {
		if alive {
			return []int{222}, nil
		}
		return nil, nil
	}
	processCommandFunc = func(pid int) (string, error) {
		return fmt.Sprintf("h2 _bridge-service --for alice --pid %d", pid), nil
	}
	var sigs []syscall.Signal
	signalProcessFunc = func(pid int, sig syscall.Signal) error {
		if pid != 222 {
			t.Fatalf("unexpected pid signaled: %d", pid)
		}
		sigs = append(sigs, sig)
		alive = false
		return nil
	}

	stopped, err := stopExistingBridgeIfRunning("alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stopped {
		t.Fatalf("expected stopped=true when orphan was terminated")
	}
	if len(sigs) == 0 || sigs[0] != syscall.SIGTERM {
		t.Fatalf("expected SIGTERM first, got %v", sigs)
	}
}

func TestStopExistingBridgeIfRunning_StopsRunningBridgeSocket(t *testing.T) {
	restore := mockBridgeCleanupDeps()
	defer restore()

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

	// Bypass lsof in this integration-style test: once socket is removed,
	// no lingering process cleanup should be needed.
	listSocketPIDsFunc = func(_ string) ([]int, error) { return nil, nil }

	stopped, err := stopExistingBridgeIfRunning("alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stopped {
		t.Fatalf("expected stopped=true for running bridge")
	}

	<-done
}

func mockBridgeCleanupDeps() func() {
	oldSend := sendBridgeStopFunc
	oldList := listSocketPIDsFunc
	oldProc := processCommandFunc
	oldSignal := signalProcessFunc
	oldSleep := sleepFunc
	oldNow := nowFunc
	oldDial := netDialTimeout

	oldDialTimeout := bridgeDialTimeout
	oldSettle := bridgeSettleTimeout
	oldPoll := bridgePollInterval
	oldChecks := bridgePersistenceChecks
	oldTermWait := bridgeTermWaitTimeout

	sleepFunc = time.Sleep
	nowFunc = time.Now

	return func() {
		sendBridgeStopFunc = oldSend
		listSocketPIDsFunc = oldList
		processCommandFunc = oldProc
		signalProcessFunc = oldSignal
		sleepFunc = oldSleep
		nowFunc = oldNow
		netDialTimeout = oldDial

		bridgeDialTimeout = oldDialTimeout
		bridgeSettleTimeout = oldSettle
		bridgePollInterval = oldPoll
		bridgePersistenceChecks = oldChecks
		bridgeTermWaitTimeout = oldTermWait
	}
}
