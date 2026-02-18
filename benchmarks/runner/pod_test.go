package runner

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"h2/internal/sandbox"
)

// mockExec records commands and returns pre-configured responses.
type mockExec struct {
	calls    [][]string
	handlers map[string]func(args []string) ([]byte, error)
}

func newMockExec() *mockExec {
	return &mockExec{
		handlers: make(map[string]func(args []string) ([]byte, error)),
	}
}

func (m *mockExec) exec(ctx context.Context, sb *sandbox.Sandbox, args []string) ([]byte, error) {
	m.calls = append(m.calls, args)
	key := strings.Join(args[:min(3, len(args))], " ")
	if handler, ok := m.handlers[key]; ok {
		return handler(args)
	}
	// Default: success.
	return nil, nil
}

// onCommand registers a handler for commands matching the first N args.
func (m *mockExec) onCommand(prefix string, handler func(args []string) ([]byte, error)) {
	m.handlers[prefix] = handler
}

// assertCalled checks that a command with the given prefix was called.
func (m *mockExec) assertCalled(t *testing.T, prefix string) {
	t.Helper()
	for _, call := range m.calls {
		joined := strings.Join(call, " ")
		if strings.HasPrefix(joined, prefix) {
			return
		}
	}
	t.Errorf("expected command with prefix %q to be called, got calls: %v", prefix, m.calls)
}

// assertNotCalled checks that a command with the given prefix was NOT called.
func (m *mockExec) assertNotCalled(t *testing.T, prefix string) {
	t.Helper()
	for _, call := range m.calls {
		joined := strings.Join(call, " ")
		if strings.HasPrefix(joined, prefix) {
			t.Errorf("expected command with prefix %q to NOT be called, but it was: %v", prefix, call)
			return
		}
	}
}

func (m *mockExec) callCount() int {
	return len(m.calls)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- NewPodRunner tests ---

func TestNewPodRunner_Defaults(t *testing.T) {
	p := NewPodRunner("")
	if p.PodTemplate != "benchmark" {
		t.Errorf("PodTemplate = %q, want %q", p.PodTemplate, "benchmark")
	}
	if p.AgentTimeout != 60*time.Second {
		t.Errorf("AgentTimeout = %v, want 60s", p.AgentTimeout)
	}
	if p.IdleTimeout != 30*time.Minute {
		t.Errorf("IdleTimeout = %v, want 30m", p.IdleTimeout)
	}
	if p.PollInterval != 10*time.Second {
		t.Errorf("PollInterval = %v, want 10s", p.PollInterval)
	}
	if len(p.ExpectedAgents) != 4 {
		t.Errorf("ExpectedAgents len = %d, want 4", len(p.ExpectedAgents))
	}
}

func TestNewPodRunner_CustomTemplate(t *testing.T) {
	p := NewPodRunner("custom-pod")
	if p.PodTemplate != "custom-pod" {
		t.Errorf("PodTemplate = %q, want %q", p.PodTemplate, "custom-pod")
	}
}

func TestPodRunner_AgentCount(t *testing.T) {
	p := NewPodRunner("")
	if p.AgentCount() != 4 {
		t.Errorf("AgentCount() = %d, want 4", p.AgentCount())
	}

	p.ExpectedAgents = []string{"a", "b"}
	if p.AgentCount() != 2 {
		t.Errorf("AgentCount() = %d, want 2", p.AgentCount())
	}
}

// --- RunAgent full flow tests ---

func TestPodRunner_RunAgent_FullFlow(t *testing.T) {
	baseDir := t.TempDir()
	sb := createTestSandbox(t, "pod-flow", baseDir)

	mock := newMockExec()

	// h2 list returns all expected agents.
	mock.onCommand("h2 list", func(args []string) ([]byte, error) {
		return []byte("concierge  coder-1  coder-2  reviewer"), nil
	})

	// h2 status --idle returns idle.
	mock.onCommand("h2 status --idle", func(args []string) ([]byte, error) {
		return []byte("idle"), nil
	})

	p := NewPodRunner("benchmark")
	p.PollInterval = 1 * time.Millisecond // Fast polling for tests.
	p.ExecInSandbox = mock.exec

	err := p.RunAgent(context.Background(), sb, "/tmp/workdir", "Fix the bug in module X")
	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}

	// Verify commands were called in order.
	mock.assertCalled(t, "h2 pod launch --template benchmark --var working_dir=/tmp/workdir")
	mock.assertCalled(t, "h2 list")
	mock.assertCalled(t, "h2 send concierge Fix the bug in module X")
	mock.assertCalled(t, "h2 status --idle")
	mock.assertCalled(t, "h2 pod stop benchmark")
}

func TestPodRunner_RunAgent_LaunchFails(t *testing.T) {
	baseDir := t.TempDir()
	sb := createTestSandbox(t, "pod-launch-fail", baseDir)

	mock := newMockExec()
	mock.onCommand("h2 pod launch", func(args []string) ([]byte, error) {
		return []byte("template not found"), fmt.Errorf("exit code 1")
	})

	p := NewPodRunner("benchmark")
	p.ExecInSandbox = mock.exec

	err := p.RunAgent(context.Background(), sb, "/tmp/work", "Fix bug")
	if err == nil {
		t.Fatal("expected error when pod launch fails")
	}
	if !strings.Contains(err.Error(), "pod launch") {
		t.Errorf("error should mention 'pod launch', got: %v", err)
	}

	// Should not try to send task or wait for idle.
	mock.assertNotCalled(t, "h2 send")
}

func TestPodRunner_RunAgent_AgentsTimeout(t *testing.T) {
	baseDir := t.TempDir()
	sb := createTestSandbox(t, "pod-agent-timeout", baseDir)

	mock := newMockExec()

	// h2 list never returns expected agents.
	mock.onCommand("h2 list", func(args []string) ([]byte, error) {
		return []byte("concierge"), nil // Missing coder-1, coder-2, reviewer.
	})

	p := NewPodRunner("benchmark")
	p.PollInterval = 1 * time.Millisecond
	p.AgentTimeout = 20 * time.Millisecond
	p.ExecInSandbox = mock.exec

	err := p.RunAgent(context.Background(), sb, "/tmp/work", "Fix bug")
	if err == nil {
		t.Fatal("expected error when agents don't start")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error should mention 'timed out', got: %v", err)
	}

	// Should attempt to stop the pod on failure.
	mock.assertCalled(t, "h2 pod stop")
}

func TestPodRunner_RunAgent_SendFails(t *testing.T) {
	baseDir := t.TempDir()
	sb := createTestSandbox(t, "pod-send-fail", baseDir)

	mock := newMockExec()
	mock.onCommand("h2 list", func(args []string) ([]byte, error) {
		return []byte("concierge  coder-1  coder-2  reviewer"), nil
	})
	mock.onCommand("h2 send concierge", func(args []string) ([]byte, error) {
		return []byte("agent not found"), fmt.Errorf("exit code 1")
	})

	p := NewPodRunner("benchmark")
	p.PollInterval = 1 * time.Millisecond
	p.ExecInSandbox = mock.exec

	err := p.RunAgent(context.Background(), sb, "/tmp/work", "Fix bug")
	if err == nil {
		t.Fatal("expected error when send fails")
	}
	if !strings.Contains(err.Error(), "send task") {
		t.Errorf("error should mention 'send task', got: %v", err)
	}

	// Should attempt pod stop on failure.
	mock.assertCalled(t, "h2 pod stop")
}

func TestPodRunner_RunAgent_IdleTimeout(t *testing.T) {
	baseDir := t.TempDir()
	sb := createTestSandbox(t, "pod-idle-timeout", baseDir)

	mock := newMockExec()
	mock.onCommand("h2 list", func(args []string) ([]byte, error) {
		return []byte("concierge  coder-1  coder-2  reviewer"), nil
	})
	mock.onCommand("h2 status --idle", func(args []string) ([]byte, error) {
		return []byte("busy"), nil // Never goes idle.
	})

	p := NewPodRunner("benchmark")
	p.PollInterval = 1 * time.Millisecond
	p.IdleTimeout = 20 * time.Millisecond
	p.ExecInSandbox = mock.exec

	err := p.RunAgent(context.Background(), sb, "/tmp/work", "Fix bug")
	if err == nil {
		t.Fatal("expected error when agents never go idle")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error should mention 'timed out', got: %v", err)
	}

	// Should attempt pod stop on failure.
	mock.assertCalled(t, "h2 pod stop")
}

func TestPodRunner_RunAgent_ContextCanceled(t *testing.T) {
	baseDir := t.TempDir()
	sb := createTestSandbox(t, "pod-ctx-cancel", baseDir)

	mock := newMockExec()
	mock.onCommand("h2 list", func(args []string) ([]byte, error) {
		return []byte("concierge  coder-1  coder-2  reviewer"), nil
	})

	// h2 status never returns idle — will block until context canceled.
	mock.onCommand("h2 status --idle", func(args []string) ([]byte, error) {
		return []byte("busy"), nil
	})

	p := NewPodRunner("benchmark")
	p.PollInterval = 1 * time.Millisecond
	p.IdleTimeout = 10 * time.Minute // Long timeout — should be preempted by ctx.
	p.ExecInSandbox = mock.exec

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	err := p.RunAgent(ctx, sb, "/tmp/work", "Fix bug")
	if err == nil {
		t.Fatal("expected error when context is cancelled")
	}
}

// --- allAgentsRunning tests ---

func TestAllAgentsRunning(t *testing.T) {
	p := NewPodRunner("")

	tests := []struct {
		name     string
		output   string
		expected bool
	}{
		{
			"all present",
			"NAME         STATUS\nconcierge    running\ncoder-1      running\ncoder-2      running\nreviewer     running",
			true,
		},
		{
			"missing one",
			"NAME         STATUS\nconcierge    running\ncoder-1      running\nreviewer     running",
			false,
		},
		{
			"empty output",
			"",
			false,
		},
		{
			"partial match",
			"concierge",
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.allAgentsRunning(tt.output)
			if got != tt.expected {
				t.Errorf("allAgentsRunning(%q) = %v, want %v", tt.output, got, tt.expected)
			}
		})
	}
}

// --- waitForAgents tests ---

func TestWaitForAgents_ImmediateSuccess(t *testing.T) {
	baseDir := t.TempDir()
	sb := createTestSandbox(t, "wait-agents-ok", baseDir)

	mock := newMockExec()
	mock.onCommand("h2 list", func(args []string) ([]byte, error) {
		return []byte("concierge  coder-1  coder-2  reviewer"), nil
	})

	p := NewPodRunner("")
	p.PollInterval = 1 * time.Millisecond
	p.ExecInSandbox = mock.exec

	err := p.waitForAgents(context.Background(), sb)
	if err != nil {
		t.Fatalf("waitForAgents: %v", err)
	}
}

func TestWaitForAgents_EventualSuccess(t *testing.T) {
	baseDir := t.TempDir()
	sb := createTestSandbox(t, "wait-agents-eventual", baseDir)

	mock := newMockExec()
	callCount := 0
	mock.onCommand("h2 list", func(args []string) ([]byte, error) {
		callCount++
		if callCount < 3 {
			return []byte("concierge"), nil // Not all agents yet.
		}
		return []byte("concierge  coder-1  coder-2  reviewer"), nil
	})

	p := NewPodRunner("")
	p.PollInterval = 1 * time.Millisecond
	p.AgentTimeout = 1 * time.Second
	p.ExecInSandbox = mock.exec

	err := p.waitForAgents(context.Background(), sb)
	if err != nil {
		t.Fatalf("waitForAgents: %v", err)
	}
	if callCount < 3 {
		t.Errorf("expected at least 3 calls to h2 list, got %d", callCount)
	}
}

// --- waitForIdle tests ---

func TestWaitForIdle_ImmediateSuccess(t *testing.T) {
	baseDir := t.TempDir()
	sb := createTestSandbox(t, "wait-idle-ok", baseDir)

	mock := newMockExec()
	mock.onCommand("h2 status --idle", func(args []string) ([]byte, error) {
		return []byte("idle"), nil
	})

	p := NewPodRunner("")
	p.PollInterval = 1 * time.Millisecond
	p.ExecInSandbox = mock.exec

	err := p.waitForIdle(context.Background(), sb)
	if err != nil {
		t.Fatalf("waitForIdle: %v", err)
	}
}

func TestWaitForIdle_EventualSuccess(t *testing.T) {
	baseDir := t.TempDir()
	sb := createTestSandbox(t, "wait-idle-eventual", baseDir)

	mock := newMockExec()
	callCount := 0
	mock.onCommand("h2 status --idle", func(args []string) ([]byte, error) {
		callCount++
		if callCount < 3 {
			return []byte("busy"), nil
		}
		return []byte("idle"), nil
	})

	p := NewPodRunner("")
	p.PollInterval = 1 * time.Millisecond
	p.IdleTimeout = 1 * time.Second
	p.ExecInSandbox = mock.exec

	err := p.waitForIdle(context.Background(), sb)
	if err != nil {
		t.Fatalf("waitForIdle: %v", err)
	}
	if callCount < 3 {
		t.Errorf("expected at least 3 calls to h2 status, got %d", callCount)
	}
}

func TestWaitForIdle_IgnoresCommandErrors(t *testing.T) {
	baseDir := t.TempDir()
	sb := createTestSandbox(t, "wait-idle-errors", baseDir)

	mock := newMockExec()
	callCount := 0
	mock.onCommand("h2 status --idle", func(args []string) ([]byte, error) {
		callCount++
		if callCount < 3 {
			return nil, fmt.Errorf("status check failed")
		}
		return []byte("idle"), nil
	})

	p := NewPodRunner("")
	p.PollInterval = 1 * time.Millisecond
	p.IdleTimeout = 1 * time.Second
	p.ExecInSandbox = mock.exec

	err := p.waitForIdle(context.Background(), sb)
	if err != nil {
		t.Fatalf("waitForIdle should succeed despite transient errors: %v", err)
	}
}

// --- Integration with Runner ---

func TestPodRunner_IntegrationWithRunner(t *testing.T) {
	baseDir := t.TempDir()

	_, err := sandbox.Create("pod-integration", "empty", "", baseDir)
	if err != nil {
		t.Fatalf("sandbox create: %v", err)
	}

	mock := newMockExec()
	mock.onCommand("h2 list", func(args []string) ([]byte, error) {
		return []byte("concierge  coder-1  coder-2  reviewer"), nil
	})
	mock.onCommand("h2 status --idle", func(args []string) ([]byte, error) {
		return []byte("idle"), nil
	})

	podRunner := NewPodRunner("benchmark")
	podRunner.PollInterval = 1 * time.Millisecond
	podRunner.ExecInSandbox = mock.exec

	r := &Runner{
		Config: BenchmarkConfig{
			Name:        "test_bench",
			Mode:        ModeH2,
			Preset:      "empty",
			PodTemplate: "benchmark",
		},
		SandboxName: "pod-integration",
		SandboxBase: baseDir,
		ResultsDir:  t.TempDir(),
		CloneRepo: func(ctx context.Context, repoURL, baseCommit string) (string, error) {
			return t.TempDir(), nil
		},
		RunAgent: podRunner.RunAgent,
		GeneratePatch: func(dir, baseCommit string) (string, error) {
			return "diff content", nil
		},
	}

	task := BenchmarkTask{
		ID:    "pod-task-1",
		Issue: "Implement feature X",
		EvalFunc: func(dir string) (bool, error) {
			return true, nil
		},
	}

	result := r.RunTask(context.Background(), task, r.ResultsDir)

	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
	if !result.Resolved {
		t.Error("expected task to be resolved")
	}
	if result.Mode != ModeH2 {
		t.Errorf("Mode = %q, want %q", result.Mode, ModeH2)
	}

	// Verify pod commands were executed.
	mock.assertCalled(t, "h2 pod launch")
	mock.assertCalled(t, "h2 send concierge")
	mock.assertCalled(t, "h2 pod stop")
}

// --- stopPod tests ---

func TestStopPod_Success(t *testing.T) {
	baseDir := t.TempDir()
	sb := createTestSandbox(t, "stop-ok", baseDir)

	mock := newMockExec()
	p := NewPodRunner("benchmark")
	p.ExecInSandbox = mock.exec

	err := p.stopPod(context.Background(), sb)
	if err != nil {
		t.Fatalf("stopPod: %v", err)
	}
	mock.assertCalled(t, "h2 pod stop benchmark")
}

func TestStopPod_ErrorReturned(t *testing.T) {
	baseDir := t.TempDir()
	sb := createTestSandbox(t, "stop-fail", baseDir)

	mock := newMockExec()
	mock.onCommand("h2 pod stop", func(args []string) ([]byte, error) {
		return nil, fmt.Errorf("pod not found")
	})

	p := NewPodRunner("benchmark")
	p.ExecInSandbox = mock.exec

	err := p.stopPod(context.Background(), sb)
	if err == nil {
		t.Fatal("expected error from stopPod")
	}
}

// --- Helper ---

func createTestSandbox(t *testing.T, name, baseDir string) *sandbox.Sandbox {
	t.Helper()
	sb, err := sandbox.Create(name, "empty", "", baseDir)
	if err != nil {
		t.Fatalf("create sandbox %q: %v", name, err)
	}
	return sb
}
