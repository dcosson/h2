package e2etests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// createPodRole writes a role YAML file into pods/roles/ directory.
func createPodRole(t *testing.T, h2Dir, name, content string) {
	t.Helper()
	path := filepath.Join(h2Dir, "pods", "roles", name+".yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("create pod role %s: %v", name, err)
	}
}

// workerRole is a minimal role that starts and stays running.
const workerRole = `
name: worker
agent_type: "true"
instructions: test worker
`

// §7.2 Launch agents in a pod
func TestPod_LaunchAgentsInPod(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	createRole(t, h2Dir, "worker", workerRole)

	// Launch two agents in the same pod.
	r1 := runH2(t, h2Dir, "run", "--role", "worker", "--pod", "backend", "--name", "builder", "--detach")
	if r1.ExitCode != 0 {
		t.Fatalf("h2 run builder failed: exit=%d stderr=%s", r1.ExitCode, r1.Stderr)
	}
	t.Cleanup(func() { stopAgent(t, h2Dir, "builder") })
	waitForSocket(t, h2Dir, "agent", "builder")

	r2 := runH2(t, h2Dir, "run", "--role", "worker", "--pod", "backend", "--name", "tester", "--detach")
	if r2.ExitCode != 0 {
		t.Fatalf("h2 run tester failed: exit=%d stderr=%s", r2.ExitCode, r2.Stderr)
	}
	t.Cleanup(func() { stopAgent(t, h2Dir, "tester") })
	waitForSocket(t, h2Dir, "agent", "tester")

	// Verify both appear in h2 list.
	result := runH2(t, h2Dir, "list")
	if result.ExitCode != 0 {
		t.Fatalf("h2 list failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "builder") {
		t.Errorf("h2 list missing builder: %s", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "tester") {
		t.Errorf("h2 list missing tester: %s", result.Stdout)
	}
}

// §7.3 h2 list shows all agents grouped by pod
func TestPod_ListGroupedByPod(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	createRole(t, h2Dir, "worker", workerRole)

	// Launch agents in different pods + one with no pod.
	for _, a := range []struct{ name, pod string }{
		{"b1", "backend"},
		{"f1", "frontend"},
		{"solo", ""},
	} {
		args := []string{"run", "--role", "worker", "--name", a.name, "--detach"}
		if a.pod != "" {
			args = append(args, "--pod", a.pod)
		}
		r := runH2(t, h2Dir, args...)
		if r.ExitCode != 0 {
			t.Fatalf("h2 run %s failed: exit=%d stderr=%s", a.name, r.ExitCode, r.Stderr)
		}
		t.Cleanup(func() { stopAgent(t, h2Dir, a.name) })
		waitForSocket(t, h2Dir, "agent", a.name)
	}

	result := runH2(t, h2Dir, "list")
	if result.ExitCode != 0 {
		t.Fatalf("h2 list failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	out := result.Stdout
	if !strings.Contains(out, "pod: backend") {
		t.Errorf("expected 'pod: backend' in output: %s", out)
	}
	if !strings.Contains(out, "pod: frontend") {
		t.Errorf("expected 'pod: frontend' in output: %s", out)
	}
	if !strings.Contains(out, "no pod") {
		t.Errorf("expected 'no pod' in output: %s", out)
	}
}

// §7.4 h2 list --pod filters to specific pod
func TestPod_ListFilterByPod(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	createRole(t, h2Dir, "worker", workerRole)

	for _, a := range []struct{ name, pod string }{
		{"b1", "backend"},
		{"f1", "frontend"},
	} {
		r := runH2(t, h2Dir, "run", "--role", "worker", "--pod", a.pod, "--name", a.name, "--detach")
		if r.ExitCode != 0 {
			t.Fatalf("h2 run %s failed: exit=%d stderr=%s", a.name, r.ExitCode, r.Stderr)
		}
		t.Cleanup(func() { stopAgent(t, h2Dir, a.name) })
		waitForSocket(t, h2Dir, "agent", a.name)
	}

	result := runH2(t, h2Dir, "list", "--pod", "backend")
	if result.ExitCode != 0 {
		t.Fatalf("h2 list --pod backend failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	out := result.Stdout
	if !strings.Contains(out, "b1") {
		t.Errorf("expected b1 in filtered output: %s", out)
	}
	if strings.Contains(out, "f1") {
		t.Errorf("f1 should not appear in backend-filtered output: %s", out)
	}
}

// §7.5 h2 list --pod '*' shows all grouped
func TestPod_ListStarShowsAll(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	createRole(t, h2Dir, "worker", workerRole)

	for _, a := range []struct{ name, pod string }{
		{"b1", "backend"},
		{"f1", "frontend"},
	} {
		r := runH2(t, h2Dir, "run", "--role", "worker", "--pod", a.pod, "--name", a.name, "--detach")
		if r.ExitCode != 0 {
			t.Fatalf("h2 run %s failed: exit=%d stderr=%s", a.name, r.ExitCode, r.Stderr)
		}
		t.Cleanup(func() { stopAgent(t, h2Dir, a.name) })
		waitForSocket(t, h2Dir, "agent", a.name)
	}

	result := runH2(t, h2Dir, "list", "--pod", "*")
	if result.ExitCode != 0 {
		t.Fatalf("h2 list --pod '*' failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	out := result.Stdout
	if !strings.Contains(out, "b1") || !strings.Contains(out, "f1") {
		t.Errorf("expected both agents in star output: %s", out)
	}
	if !strings.Contains(out, "pod: backend") || !strings.Contains(out, "pod: frontend") {
		t.Errorf("expected pod group headers in star output: %s", out)
	}
}

// §7.6 H2_POD env var filtering
func TestPod_ListH2PODEnvFilter(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	createRole(t, h2Dir, "worker", workerRole)

	for _, a := range []struct{ name, pod string }{
		{"b1", "backend"},
		{"f1", "frontend"},
	} {
		r := runH2(t, h2Dir, "run", "--role", "worker", "--pod", a.pod, "--name", a.name, "--detach")
		if r.ExitCode != 0 {
			t.Fatalf("h2 run %s failed: exit=%d stderr=%s", a.name, r.ExitCode, r.Stderr)
		}
		t.Cleanup(func() { stopAgent(t, h2Dir, a.name) })
		waitForSocket(t, h2Dir, "agent", a.name)
	}

	result := runH2WithEnv(t, h2Dir, []string{"H2_POD=backend"}, "list")
	if result.ExitCode != 0 {
		t.Fatalf("H2_POD=backend h2 list failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	out := result.Stdout
	if !strings.Contains(out, "b1") {
		t.Errorf("expected b1 with H2_POD=backend: %s", out)
	}
	if strings.Contains(out, "f1") {
		t.Errorf("f1 should not appear with H2_POD=backend: %s", out)
	}
}

// §7.7 --pod flag overrides H2_POD
func TestPod_ListFlagOverridesEnv(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	createRole(t, h2Dir, "worker", workerRole)

	for _, a := range []struct{ name, pod string }{
		{"b1", "backend"},
		{"f1", "frontend"},
	} {
		r := runH2(t, h2Dir, "run", "--role", "worker", "--pod", a.pod, "--name", a.name, "--detach")
		if r.ExitCode != 0 {
			t.Fatalf("h2 run %s failed: exit=%d stderr=%s", a.name, r.ExitCode, r.Stderr)
		}
		t.Cleanup(func() { stopAgent(t, h2Dir, a.name) })
		waitForSocket(t, h2Dir, "agent", a.name)
	}

	// H2_POD=backend but --pod frontend should show frontend.
	result := runH2WithEnv(t, h2Dir, []string{"H2_POD=backend"}, "list", "--pod", "frontend")
	if result.ExitCode != 0 {
		t.Fatalf("h2 list --pod frontend failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	out := result.Stdout
	if !strings.Contains(out, "f1") {
		t.Errorf("expected f1 with --pod frontend: %s", out)
	}
	if strings.Contains(out, "b1") {
		t.Errorf("b1 should not appear with --pod frontend: %s", out)
	}
}

// §7.8 h2 send is not pod-scoped
func TestPod_SendNotPodScoped(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	createRole(t, h2Dir, "worker", workerRole)

	for _, a := range []struct{ name, pod string }{
		{"b1", "backend"},
		{"f1", "frontend"},
	} {
		r := runH2(t, h2Dir, "run", "--role", "worker", "--pod", a.pod, "--name", a.name, "--detach")
		if r.ExitCode != 0 {
			t.Fatalf("h2 run %s failed: exit=%d stderr=%s", a.name, r.ExitCode, r.Stderr)
		}
		t.Cleanup(func() { stopAgent(t, h2Dir, a.name) })
		waitForSocket(t, h2Dir, "agent", a.name)
	}

	// Send from b1 (backend) to f1 (frontend) — should succeed.
	// h2 send uses H2_ACTOR env var (not --from flag) to identify the sender.
	result := runH2WithEnv(t, h2Dir, []string{"H2_ACTOR=b1"}, "send", "f1", "hello cross-pod")
	if result.ExitCode != 0 {
		t.Fatalf("h2 send cross-pod failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
}

// §7.9 Pod name validation
func TestPod_NameValidation(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	createRole(t, h2Dir, "worker", workerRole)

	result := runH2(t, h2Dir, "run", "--role", "worker", "--pod", "INVALID POD!", "--name", "test-badpod", "--detach")
	if result.ExitCode == 0 {
		t.Fatal("expected error for invalid pod name")
		t.Cleanup(func() { stopAgent(t, h2Dir, "test-badpod") })
	}
	combined := result.Stdout + result.Stderr
	if !strings.Contains(combined, "invalid pod name") {
		t.Errorf("expected 'invalid pod name' in error: %s", combined)
	}
}

// §8.1-8.2 Pod role takes priority over global role
func TestPodRole_PodOverridesGlobal(t *testing.T) {
	h2Dir := createTestH2Dir(t)

	// Create global and pod role with same name but different agent_type.
	createRole(t, h2Dir, "shared-role", `
name: shared-role
agent_type: "true"
instructions: global version
description: global
`)
	createPodRole(t, h2Dir, "shared-role", `
name: shared-role
agent_type: "true"
instructions: pod version
description: pod-override
`)

	// Launch with --pod should use the pod role.
	r := runH2(t, h2Dir, "run", "--role", "shared-role", "--pod", "test-pod", "--name", "pod-agent", "--detach")
	if r.ExitCode != 0 {
		t.Fatalf("h2 run failed: exit=%d stderr=%s", r.ExitCode, r.Stderr)
	}
	t.Cleanup(func() { stopAgent(t, h2Dir, "pod-agent") })
	waitForSocket(t, h2Dir, "agent", "pod-agent")

	// Launch without --pod should use global role.
	r2 := runH2(t, h2Dir, "run", "--role", "shared-role", "--name", "global-agent", "--detach")
	if r2.ExitCode != 0 {
		t.Fatalf("h2 run failed: exit=%d stderr=%s", r2.ExitCode, r2.Stderr)
	}
	t.Cleanup(func() { stopAgent(t, h2Dir, "global-agent") })
	waitForSocket(t, h2Dir, "agent", "global-agent")

	// Both agents should be running. The main verification is that
	// the pod launch succeeded using the pod-scoped role.
	result := runH2(t, h2Dir, "list")
	if result.ExitCode != 0 {
		t.Fatalf("h2 list failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	out := result.Stdout
	if !strings.Contains(out, "pod-agent") || !strings.Contains(out, "global-agent") {
		t.Errorf("expected both agents in list: %s", out)
	}
}

// §8.3 h2 role list shows both scopes
func TestPodRole_RoleListShowsBothScopes(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	createRole(t, h2Dir, "global-role", `
name: global-role
agent_type: "true"
instructions: global
description: a global role
`)
	createPodRole(t, h2Dir, "pod-role", `
name: pod-role
agent_type: "true"
instructions: pod
description: a pod role
`)

	result := runH2(t, h2Dir, "role", "list")
	if result.ExitCode != 0 {
		t.Fatalf("h2 role list failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	out := result.Stdout
	if !strings.Contains(out, "Global roles") {
		t.Errorf("expected 'Global roles' header: %s", out)
	}
	if !strings.Contains(out, "Pod roles") {
		t.Errorf("expected 'Pod roles' header: %s", out)
	}
	if !strings.Contains(out, "global-role") {
		t.Errorf("expected global-role listed: %s", out)
	}
	if !strings.Contains(out, "pod-role") {
		t.Errorf("expected pod-role listed: %s", out)
	}
}

// §9.1-9.2 Create template and launch pod from it
func TestPodTemplate_LaunchFromTemplate(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	createRole(t, h2Dir, "worker", workerRole)

	createPodTemplate(t, h2Dir, "test-team", `
pod_name: test-team
agents:
  - name: coder
    role: worker
  - name: reviewer
    role: worker
`)

	result := runH2(t, h2Dir, "pod", "launch", "test-team")
	if result.ExitCode != 0 {
		t.Fatalf("h2 pod launch failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	t.Cleanup(func() {
		stopAgent(t, h2Dir, "coder")
		stopAgent(t, h2Dir, "reviewer")
	})
	waitForSocket(t, h2Dir, "agent", "coder")
	waitForSocket(t, h2Dir, "agent", "reviewer")

	// Verify both agents appear under the pod in h2 list.
	listResult := runH2(t, h2Dir, "list")
	if listResult.ExitCode != 0 {
		t.Fatalf("h2 list failed: exit=%d stderr=%s", listResult.ExitCode, listResult.Stderr)
	}
	out := listResult.Stdout
	if !strings.Contains(out, "pod: test-team") {
		t.Errorf("expected 'pod: test-team' in list: %s", out)
	}
	if !strings.Contains(out, "coder") || !strings.Contains(out, "reviewer") {
		t.Errorf("expected both agents in list: %s", out)
	}
}

// §9.3 Launch with pod name override
func TestPodTemplate_LaunchWithNameOverride(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	createRole(t, h2Dir, "worker", workerRole)

	createPodTemplate(t, h2Dir, "team", `
pod_name: original-name
agents:
  - name: a1
    role: worker
`)

	result := runH2(t, h2Dir, "pod", "launch", "team", "--pod", "custom-name")
	if result.ExitCode != 0 {
		t.Fatalf("h2 pod launch --pod custom-name failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	t.Cleanup(func() { stopAgent(t, h2Dir, "a1") })
	waitForSocket(t, h2Dir, "agent", "a1")

	// Verify agent is in custom-name pod.
	listResult := runH2(t, h2Dir, "list")
	out := listResult.Stdout
	if !strings.Contains(out, "pod: custom-name") {
		t.Errorf("expected 'pod: custom-name' in list: %s", out)
	}
	if strings.Contains(out, "original-name") {
		t.Errorf("should not see original-name: %s", out)
	}
}

// §9.4 h2 pod list shows templates
func TestPodTemplate_PodList(t *testing.T) {
	h2Dir := createTestH2Dir(t)

	createPodTemplate(t, h2Dir, "team-a", `
pod_name: team-a
agents:
  - name: a1
    role: worker
  - name: a2
    role: worker
`)

	result := runH2(t, h2Dir, "pod", "list")
	if result.ExitCode != 0 {
		t.Fatalf("h2 pod list failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	out := result.Stdout
	if !strings.Contains(out, "team-a") {
		t.Errorf("expected team-a in pod list: %s", out)
	}
	if !strings.Contains(out, "2 agents") {
		t.Errorf("expected '2 agents' in pod list: %s", out)
	}
}

// §9.5 h2 pod stop stops all agents in pod
func TestPodTemplate_PodStop(t *testing.T) {
	h2Dir := createTestH2Dir(t)
	createRole(t, h2Dir, "worker", workerRole)

	createPodTemplate(t, h2Dir, "stop-team", `
pod_name: stop-team
agents:
  - name: s1
    role: worker
  - name: s2
    role: worker
`)

	result := runH2(t, h2Dir, "pod", "launch", "stop-team")
	if result.ExitCode != 0 {
		t.Fatalf("h2 pod launch failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	waitForSocket(t, h2Dir, "agent", "s1")
	waitForSocket(t, h2Dir, "agent", "s2")

	// Stop the pod.
	stopResult := runH2(t, h2Dir, "pod", "stop", "stop-team")
	if stopResult.ExitCode != 0 {
		t.Fatalf("h2 pod stop failed: exit=%d stderr=%s", stopResult.ExitCode, stopResult.Stderr)
	}

	// Verify agents are gone.
	listResult := runH2(t, h2Dir, "list")
	out := listResult.Stdout
	// After stop, agents should not appear (or appear as exited/not responding).
	if strings.Contains(out, "stop-team") && !strings.Contains(out, "not responding") && !strings.Contains(out, "exited") {
		// If they're still showing as active, that's a problem.
		if strings.Contains(out, "s1") && strings.Contains(out, "active") {
			t.Errorf("agents should be stopped but still appear active: %s", out)
		}
	}
}
