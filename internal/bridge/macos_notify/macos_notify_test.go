package macos_notify

import (
	"context"
	"os/exec"
	"sync"
	"testing"

	"h2/internal/bridge"
)

func TestSend(t *testing.T) {
	var mu sync.Mutex
	var gotName string
	var gotArgs []string

	m := &MacOSNotify{
		ExecCommand: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			mu.Lock()
			gotName = name
			gotArgs = args
			mu.Unlock()
			// Return a command that succeeds without doing anything
			return exec.CommandContext(ctx, "true")
		},
	}

	err := m.Send(context.Background(), "build complete")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if gotName != "osascript" {
		t.Errorf("command = %q, want %q", gotName, "osascript")
	}
	if len(gotArgs) != 2 {
		t.Fatalf("args = %v, want 2 args", gotArgs)
	}
	if gotArgs[0] != "-e" {
		t.Errorf("args[0] = %q, want %q", gotArgs[0], "-e")
	}

	wantScript := `display notification "build complete" with title "h2"`
	if gotArgs[1] != wantScript {
		t.Errorf("script = %q, want %q", gotArgs[1], wantScript)
	}
}

func TestSend_Quotes(t *testing.T) {
	var mu sync.Mutex
	var gotArgs []string

	m := &MacOSNotify{
		ExecCommand: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			mu.Lock()
			gotArgs = args
			mu.Unlock()
			return exec.CommandContext(ctx, "true")
		},
	}

	err := m.Send(context.Background(), `say "hello" now`)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// fmt.Sprintf %q escapes inner double quotes with backslash
	wantScript := `display notification "say \"hello\" now" with title "h2"`
	if gotArgs[1] != wantScript {
		t.Errorf("script = %q, want %q", gotArgs[1], wantScript)
	}
}

func TestSend_Newlines(t *testing.T) {
	var mu sync.Mutex
	var gotArgs []string

	m := &MacOSNotify{
		ExecCommand: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			mu.Lock()
			gotArgs = args
			mu.Unlock()
			return exec.CommandContext(ctx, "true")
		},
	}

	err := m.Send(context.Background(), "line1\nline2\rline3")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Newlines replaced with spaces
	wantScript := `display notification "line1 line2 line3" with title "h2"`
	if gotArgs[1] != wantScript {
		t.Errorf("script = %q, want %q", gotArgs[1], wantScript)
	}
}

func TestSend_Error(t *testing.T) {
	m := &MacOSNotify{
		ExecCommand: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "false")
		},
	}

	err := m.Send(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestInterfaces(t *testing.T) {
	m := &MacOSNotify{}

	// Bridge interface
	var _ bridge.Bridge = m
	if m.Name() != "macos_notify" {
		t.Errorf("Name() = %q, want %q", m.Name(), "macos_notify")
	}

	// Sender interface
	var _ bridge.Sender = m
}
