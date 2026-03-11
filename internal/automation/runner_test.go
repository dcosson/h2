package automation

import (
	"fmt"
	"os"
	"testing"
	"time"

	"h2/internal/session/message"
)

// Test helpers (mockEnqueuer, enqueuedMsg) are in trigger_test.go to avoid
// redeclaration across test files in the same package.

// failEnqueuer always returns an error.
type failEnqueuer struct{}

func (f *failEnqueuer) EnqueueMessage(string, string, string, message.Priority) (string, error) {
	return "", fmt.Errorf("enqueue failed")
}

func TestActionRunner_MessageAction(t *testing.T) {
	eq := &mockEnqueuer{}
	r := NewActionRunner(eq, nil, nil)

	err := r.Run(Action{Message: "hello", From: "h2-trigger", Priority: "idle"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := eq.getMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].From != "h2-trigger" {
		t.Errorf("from = %q, want h2-trigger", msgs[0].From)
	}
	if msgs[0].Body != "hello" {
		t.Errorf("body = %q, want hello", msgs[0].Body)
	}
	if msgs[0].Priority != message.PriorityIdle {
		t.Errorf("priority = %v, want idle", msgs[0].Priority)
	}
}

func TestActionRunner_MessageAction_DefaultFrom(t *testing.T) {
	eq := &mockEnqueuer{}
	r := NewActionRunner(eq, nil, nil)

	err := r.Run(Action{Message: "nudge"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := eq.getMessages()
	if msgs[0].From != "h2-automation" {
		t.Errorf("from = %q, want h2-automation", msgs[0].From)
	}
	if msgs[0].Priority != message.PriorityNormal {
		t.Errorf("priority = %v, want normal", msgs[0].Priority)
	}
}

func TestActionRunner_MessageAction_BadPriority(t *testing.T) {
	eq := &mockEnqueuer{}
	r := NewActionRunner(eq, nil, nil)

	err := r.Run(Action{Message: "hello", Priority: "bogus"}, nil)
	if err == nil {
		t.Fatal("expected error for bad priority")
	}
}

func TestActionRunner_MessageAction_EnqueueError(t *testing.T) {
	r := NewActionRunner(&failEnqueuer{}, nil, nil)

	err := r.Run(Action{Message: "hello"}, nil)
	if err == nil {
		t.Fatal("expected error from failing enqueuer")
	}
}

func TestActionRunner_ExecAction_Success(t *testing.T) {
	eq := &mockEnqueuer{}
	r := NewActionRunner(eq, nil, nil)

	err := r.Run(Action{Exec: "true"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r.Wait()
}

func TestActionRunner_ExecAction_Failure(t *testing.T) {
	eq := &mockEnqueuer{}
	r := NewActionRunner(eq, nil, nil)

	err := r.Run(Action{Exec: "false"}, nil)
	if err != nil {
		t.Fatalf("unexpected error (exec is async): %v", err)
	}
	r.Wait()
}

func TestActionRunner_ExecAction_Async(t *testing.T) {
	eq := &mockEnqueuer{}
	r := NewActionRunner(eq, nil, nil)

	start := time.Now()
	err := r.Run(Action{Exec: "sleep 0.1"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 50*time.Millisecond {
		t.Errorf("Run() blocked for %v, expected non-blocking dispatch", elapsed)
	}
	r.Wait()
}

func TestActionRunner_ExecAction_Environment(t *testing.T) {
	eq := &mockEnqueuer{}
	baseEnv := map[string]string{"H2_ACTOR": "test-agent"}
	r := NewActionRunner(eq, baseEnv, nil)

	tmp := t.TempDir() + "/env.txt"
	err := r.Run(Action{Exec: fmt.Sprintf(`echo "$H2_ACTOR:$H2_TRIGGER_ID" > %s`, tmp)},
		map[string]string{"H2_TRIGGER_ID": "abc12345"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r.Wait()

	got, readErr := os.ReadFile(tmp)
	if readErr != nil {
		t.Fatalf("failed to read output: %v", readErr)
	}
	expected := "test-agent:abc12345\n"
	if string(got) != expected {
		t.Errorf("env output = %q, want %q", string(got), expected)
	}
}

func TestActionRunner_ExecAction_ConcurrencyLimit(t *testing.T) {
	eq := &mockEnqueuer{}
	r := NewActionRunner(eq, nil, nil)

	for i := 0; i < MaxConcurrentExec; i++ {
		err := r.Run(Action{Exec: "sleep 1"}, nil)
		if err != nil {
			t.Fatalf("expected slot %d to succeed: %v", i, err)
		}
	}

	err := r.Run(Action{Exec: "echo dropped"}, nil)
	if err == nil {
		t.Fatal("expected error from dropped action")
	}

	r.Wait()
}

func TestActionRunner_ExecAction_Timeout(t *testing.T) {
	orig := ExecTimeout
	ExecTimeout = 200 * time.Millisecond
	defer func() { ExecTimeout = orig }()

	eq := &mockEnqueuer{}
	r := NewActionRunner(eq, nil, nil)

	err := r.Run(Action{Exec: "sleep 10"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	start := time.Now()
	r.Wait()
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Errorf("exec took %v, expected timeout around 200ms", elapsed)
	}
}

func TestActionRunner_BaseEnvMerge(t *testing.T) {
	eq := &mockEnqueuer{}
	baseEnv := map[string]string{"A": "base", "B": "base"}
	r := NewActionRunner(eq, baseEnv, nil)

	merged := r.MergeEnv(map[string]string{"B": "override", "C": "extra"})
	if merged["A"] != "base" {
		t.Errorf("A = %q, want base", merged["A"])
	}
	if merged["B"] != "override" {
		t.Errorf("B = %q, want override", merged["B"])
	}
	if merged["C"] != "extra" {
		t.Errorf("C = %q, want extra", merged["C"])
	}
}
