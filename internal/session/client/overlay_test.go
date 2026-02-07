package client

import (
	"os/exec"
	"strings"
	"testing"

	"h2/internal/session/virtualterminal"
)

// --- HandleExitedBytes ---

func TestHandleExitedBytes_EnterRelaunches(t *testing.T) {
	var called bool
	o := &Client{
		OnRelaunch: func() { called = true },
	}

	buf := []byte{'\r'}
	n := o.HandleExitedBytes(buf, 0, len(buf))
	if n != len(buf) {
		t.Fatalf("expected %d, got %d", len(buf), n)
	}

	if !called {
		t.Fatal("expected OnRelaunch to be called")
	}
}

func TestHandleExitedBytes_NewlineRelaunches(t *testing.T) {
	var called bool
	o := &Client{
		OnRelaunch: func() { called = true },
	}

	buf := []byte{'\n'}
	o.HandleExitedBytes(buf, 0, len(buf))

	if !called {
		t.Fatal("expected OnRelaunch to be called")
	}
}

func TestHandleExitedBytes_QuitLowercase(t *testing.T) {
	var called bool
	o := &Client{
		OnQuit: func() { called = true },
	}

	buf := []byte{'q'}
	o.HandleExitedBytes(buf, 0, len(buf))

	if !called {
		t.Fatal("expected OnQuit to be called")
	}
	if !o.Quit {
		t.Fatal("expected Quit to be true")
	}
}

func TestHandleExitedBytes_QuitUppercase(t *testing.T) {
	var called bool
	o := &Client{
		OnQuit: func() { called = true },
	}

	buf := []byte{'Q'}
	o.HandleExitedBytes(buf, 0, len(buf))

	if !called {
		t.Fatal("expected OnQuit to be called")
	}
}

func TestHandleExitedBytes_IgnoresOtherKeys(t *testing.T) {
	var relaunchCalled, quitCalled bool
	o := &Client{
		OnRelaunch: func() { relaunchCalled = true },
		OnQuit:     func() { quitCalled = true },
	}

	buf := []byte{'a', 'b', 'c', 0x1B, ' '}
	o.HandleExitedBytes(buf, 0, len(buf))

	if relaunchCalled {
		t.Fatal("unexpected OnRelaunch call")
	}
	if quitCalled {
		t.Fatal("unexpected OnQuit call")
	}
}

func TestHandleExitedBytes_StartOffset(t *testing.T) {
	var called bool
	o := &Client{
		OnRelaunch: func() { called = true },
	}

	// Only bytes from index 2 onward should be processed.
	buf := []byte{'a', 'b', '\r'}
	n := o.HandleExitedBytes(buf, 2, len(buf))
	if n != len(buf) {
		t.Fatalf("expected %d, got %d", len(buf), n)
	}

	if !called {
		t.Fatal("expected OnRelaunch to be called")
	}
}

func TestHandleExitedBytes_NilCallbackDoesNotPanic(t *testing.T) {
	o := &Client{}

	// Should not panic even with nil callbacks.
	buf := []byte{'\r'}
	o.HandleExitedBytes(buf, 0, len(buf))

	buf = []byte{'q'}
	o.HandleExitedBytes(buf, 0, len(buf))
}

// --- exitMessage ---

func TestExitMessage_CleanExit(t *testing.T) {
	o := &Client{VT: &virtualterminal.VT{}}
	msg := o.exitMessage()
	if msg != "process exited" {
		t.Fatalf("expected %q, got %q", "process exited", msg)
	}
}

func TestExitMessage_Hung(t *testing.T) {
	vt := &virtualterminal.VT{ChildHung: true}
	o := &Client{VT: vt}
	msg := o.exitMessage()
	expected := "process not responding (killed)"
	if msg != expected {
		t.Fatalf("expected %q, got %q", expected, msg)
	}
}

func TestExitMessage_ExitCode(t *testing.T) {
	// Run a command that exits with a known code to get an *exec.ExitError.
	err := exec.Command("sh", "-c", "exit 42").Run()
	if err == nil {
		t.Fatal("expected command to fail")
	}

	vt := &virtualterminal.VT{ExitError: err}
	o := &Client{VT: vt}
	msg := o.exitMessage()
	expected := "process exited (code 42)"
	if msg != expected {
		t.Fatalf("expected %q, got %q", expected, msg)
	}
}

func TestExitMessage_Signal(t *testing.T) {
	// Start a long-running command and kill it to get a signal exit.
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	cmd.Process.Kill()
	err := cmd.Wait()

	vt := &virtualterminal.VT{ExitError: err}
	o := &Client{VT: vt}
	msg := o.exitMessage()
	if !strings.Contains(msg, "process killed") {
		t.Fatalf("expected message containing 'process killed', got %q", msg)
	}
	if !strings.Contains(msg, "killed") {
		t.Fatalf("expected message containing signal name, got %q", msg)
	}
}
