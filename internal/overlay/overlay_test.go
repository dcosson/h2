package overlay

import (
	"os/exec"
	"strings"
	"testing"
)

// --- HandleExitedBytes ---

func TestHandleExitedBytes_EnterRelaunches(t *testing.T) {
	o := &Overlay{
		relaunchCh: make(chan struct{}, 1),
		quitCh:     make(chan struct{}, 1),
	}

	buf := []byte{'\r'}
	n := o.HandleExitedBytes(buf, 0, len(buf))
	if n != len(buf) {
		t.Fatalf("expected %d, got %d", len(buf), n)
	}

	select {
	case <-o.relaunchCh:
	default:
		t.Fatal("expected relaunchCh to receive")
	}
}

func TestHandleExitedBytes_NewlineRelaunches(t *testing.T) {
	o := &Overlay{
		relaunchCh: make(chan struct{}, 1),
		quitCh:     make(chan struct{}, 1),
	}

	buf := []byte{'\n'}
	o.HandleExitedBytes(buf, 0, len(buf))

	select {
	case <-o.relaunchCh:
	default:
		t.Fatal("expected relaunchCh to receive")
	}
}

func TestHandleExitedBytes_QuitLowercase(t *testing.T) {
	o := &Overlay{
		relaunchCh: make(chan struct{}, 1),
		quitCh:     make(chan struct{}, 1),
	}

	buf := []byte{'q'}
	o.HandleExitedBytes(buf, 0, len(buf))

	select {
	case <-o.quitCh:
	default:
		t.Fatal("expected quitCh to receive")
	}
	if !o.Quit {
		t.Fatal("expected Quit to be true")
	}
}

func TestHandleExitedBytes_QuitUppercase(t *testing.T) {
	o := &Overlay{
		relaunchCh: make(chan struct{}, 1),
		quitCh:     make(chan struct{}, 1),
	}

	buf := []byte{'Q'}
	o.HandleExitedBytes(buf, 0, len(buf))

	select {
	case <-o.quitCh:
	default:
		t.Fatal("expected quitCh to receive")
	}
}

func TestHandleExitedBytes_IgnoresOtherKeys(t *testing.T) {
	o := &Overlay{
		relaunchCh: make(chan struct{}, 1),
		quitCh:     make(chan struct{}, 1),
	}

	buf := []byte{'a', 'b', 'c', 0x1B, ' '}
	o.HandleExitedBytes(buf, 0, len(buf))

	select {
	case <-o.relaunchCh:
		t.Fatal("unexpected relaunchCh signal")
	default:
	}
	select {
	case <-o.quitCh:
		t.Fatal("unexpected quitCh signal")
	default:
	}
}

func TestHandleExitedBytes_StartOffset(t *testing.T) {
	o := &Overlay{
		relaunchCh: make(chan struct{}, 1),
		quitCh:     make(chan struct{}, 1),
	}

	// Only bytes from index 2 onward should be processed.
	buf := []byte{'a', 'b', '\r'}
	n := o.HandleExitedBytes(buf, 2, len(buf))
	if n != len(buf) {
		t.Fatalf("expected %d, got %d", len(buf), n)
	}

	select {
	case <-o.relaunchCh:
	default:
		t.Fatal("expected relaunchCh to receive")
	}
}

func TestHandleExitedBytes_ChannelFull(t *testing.T) {
	o := &Overlay{
		relaunchCh: make(chan struct{}, 1),
		quitCh:     make(chan struct{}, 1),
	}

	// Pre-fill the channel.
	o.relaunchCh <- struct{}{}

	// Second Enter should not block (non-blocking send).
	buf := []byte{'\r'}
	o.HandleExitedBytes(buf, 0, len(buf))
	// If we got here without blocking, the test passes.
}

// --- exitMessage ---

func TestExitMessage_CleanExit(t *testing.T) {
	o := &Overlay{}
	msg := o.exitMessage()
	if msg != "process exited" {
		t.Fatalf("expected %q, got %q", "process exited", msg)
	}
}

func TestExitMessage_Hung(t *testing.T) {
	o := &Overlay{ChildHung: true}
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

	o := &Overlay{ExitError: err}
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

	o := &Overlay{ExitError: err}
	msg := o.exitMessage()
	if !strings.Contains(msg, "process killed") {
		t.Fatalf("expected message containing 'process killed', got %q", msg)
	}
	if !strings.Contains(msg, "killed") {
		t.Fatalf("expected message containing signal name, got %q", msg)
	}
}
