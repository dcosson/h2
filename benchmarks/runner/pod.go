package runner

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"h2/internal/sandbox"
)

const (
	defaultPodTemplate  = "benchmark"
	defaultAgentTimeout = 60 * time.Second
	defaultIdleTimeout  = 30 * time.Minute
	defaultPollInterval = 10 * time.Second
)

// PodRunner manages h2 pod lifecycle for multi-agent benchmark tasks.
// It launches a pod within a sandbox, sends the task to the concierge,
// waits for agents to complete, and cleans up.
type PodRunner struct {
	PodTemplate  string        // Pod template name (default: "benchmark").
	AgentTimeout time.Duration // Max wait for agents to come up (default: 60s).
	IdleTimeout  time.Duration // Max wait for all agents to go idle (default: 30m).
	PollInterval time.Duration // Polling interval for status checks (default: 10s).

	// ExpectedAgents is the list of agent names we expect the pod to launch.
	// Used by waitForAgents to verify all agents are running.
	// Default: {"concierge", "coder-1", "coder-2", "reviewer"}.
	ExpectedAgents []string

	// ExecInSandbox runs a command within the sandbox environment.
	// Pluggable for testing. Default: exec with H2_DIR set to sandbox dir.
	ExecInSandbox func(ctx context.Context, sb *sandbox.Sandbox, args []string) ([]byte, error)
}

// NewPodRunner creates a PodRunner with defaults.
func NewPodRunner(podTemplate string) *PodRunner {
	if podTemplate == "" {
		podTemplate = defaultPodTemplate
	}
	return &PodRunner{
		PodTemplate:    podTemplate,
		AgentTimeout:   defaultAgentTimeout,
		IdleTimeout:    defaultIdleTimeout,
		PollInterval:   defaultPollInterval,
		ExpectedAgents: []string{"concierge", "coder-1", "coder-2", "reviewer"},
		ExecInSandbox:  defaultExecInSandbox,
	}
}

// defaultExecInSandbox runs a command with H2_DIR set to the sandbox directory.
func defaultExecInSandbox(ctx context.Context, sb *sandbox.Sandbox, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = sb.Env()
	return cmd.CombinedOutput()
}

// RunAgent returns a function compatible with Runner.RunAgent that executes
// a multi-agent pod for each benchmark task.
func (p *PodRunner) RunAgent(ctx context.Context, sb *sandbox.Sandbox, workDir, issue string) error {
	// 1. Launch pod with working directory.
	launchArgs := []string{
		"h2", "pod", "launch",
		"--template", p.PodTemplate,
		"--var", "working_dir=" + workDir,
	}
	if out, err := p.ExecInSandbox(ctx, sb, launchArgs); err != nil {
		return fmt.Errorf("pod launch: %s: %w", truncateOutput(out, 500), err)
	}

	// 2. Wait for all agents to come up.
	if err := p.waitForAgents(ctx, sb); err != nil {
		// Try to stop the pod before returning.
		p.stopPod(ctx, sb)
		return fmt.Errorf("wait for agents: %w", err)
	}

	// 3. Send task to concierge.
	sendArgs := []string{"h2", "send", "concierge", issue}
	if out, err := p.ExecInSandbox(ctx, sb, sendArgs); err != nil {
		p.stopPod(ctx, sb)
		return fmt.Errorf("send task to concierge: %s: %w", truncateOutput(out, 500), err)
	}

	// 4. Wait for all agents to go idle.
	if err := p.waitForIdle(ctx, sb); err != nil {
		p.stopPod(ctx, sb)
		return fmt.Errorf("wait for idle: %w", err)
	}

	// 5. Stop pod (cleanup).
	if err := p.stopPod(ctx, sb); err != nil {
		return fmt.Errorf("pod stop: %w", err)
	}

	return nil
}

// waitForAgents polls h2 list until all expected agents are running.
func (p *PodRunner) waitForAgents(ctx context.Context, sb *sandbox.Sandbox) error {
	timeout := p.AgentTimeout
	if timeout == 0 {
		timeout = defaultAgentTimeout
	}

	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %v waiting for agents to start", timeout)
		}

		out, err := p.ExecInSandbox(ctx, sb, []string{"h2", "list"})
		if err == nil && p.allAgentsRunning(string(out)) {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(p.PollInterval):
		}
	}
}

// allAgentsRunning checks if all expected agents appear in the h2 list output.
func (p *PodRunner) allAgentsRunning(listOutput string) bool {
	for _, agent := range p.ExpectedAgents {
		if !strings.Contains(listOutput, agent) {
			return false
		}
	}
	return true
}

// waitForIdle polls h2 status until all agents report idle, or the timeout expires.
// Agents are considered idle when `h2 status` reports no active agents.
func (p *PodRunner) waitForIdle(ctx context.Context, sb *sandbox.Sandbox) error {
	timeout := p.IdleTimeout
	if timeout == 0 {
		timeout = defaultIdleTimeout
	}

	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %v waiting for agents to go idle", timeout)
		}

		out, err := p.ExecInSandbox(ctx, sb, []string{"h2", "status", "--idle"})
		if err == nil && strings.TrimSpace(string(out)) == "idle" {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(p.PollInterval):
		}
	}
}

// stopPod sends pod stop command. Best-effort — logs but does not fail.
func (p *PodRunner) stopPod(ctx context.Context, sb *sandbox.Sandbox) error {
	stopArgs := []string{"h2", "pod", "stop", p.PodTemplate}
	_, err := p.ExecInSandbox(ctx, sb, stopArgs)
	return err
}

// AgentCount returns the number of expected agents for result tracking.
func (p *PodRunner) AgentCount() int {
	return len(p.ExpectedAgents)
}
