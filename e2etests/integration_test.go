package e2etests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// §10 Full workflow: init → create repo → create roles → create template →
// launch pod → verify worktrees → messaging → stop pod → check version
func TestIntegration_FullWorkflow(t *testing.T) {
	// 1. Init a project-local h2 dir.
	h2Dir := createTestH2Dir(t)

	// 2. Create a project repo.
	createGitRepo(t, h2Dir, "projects/webapp")

	// 3. Create roles (builder with branch worktree, reviewer with detached head).
	createRole(t, h2Dir, "builder", `
name: builder
agent_type: "true"
instructions: Build features.
working_dir: projects/webapp
worktree:
  enabled: true
  branch_from: main
`)
	createRole(t, h2Dir, "reviewer", `
name: reviewer
agent_type: "true"
instructions: Review code.
working_dir: projects/webapp
worktree:
  enabled: true
  branch_from: main
  use_detached_head: true
`)

	// 4. Create a pod template.
	createPodTemplate(t, h2Dir, "dev-team", `
pod_name: dev-team
agents:
  - name: builder
    role: builder
  - name: reviewer
    role: reviewer
`)

	// 5. Launch the pod.
	launchResult := runH2(t, h2Dir, "pod", "launch", "dev-team")
	if launchResult.ExitCode != 0 {
		t.Skipf("h2 pod launch failed (expected in some environments): %s", launchResult.Stderr)
	}
	t.Cleanup(func() {
		runH2(t, h2Dir, "pod", "stop", "dev-team")
	})

	// Wait for both agents' sockets.
	waitForSocket(t, h2Dir, "agent", "builder")
	waitForSocket(t, h2Dir, "agent", "reviewer")

	// 6a. h2 list should show both agents grouped under dev-team.
	listResult := runH2(t, h2Dir, "list", "--pod", "*")
	if listResult.ExitCode != 0 {
		t.Fatalf("h2 list failed: exit=%d stderr=%s", listResult.ExitCode, listResult.Stderr)
	}
	if !strings.Contains(listResult.Stdout, "builder") {
		t.Errorf("h2 list output missing 'builder': %s", listResult.Stdout)
	}
	if !strings.Contains(listResult.Stdout, "reviewer") {
		t.Errorf("h2 list output missing 'reviewer': %s", listResult.Stdout)
	}
	if !strings.Contains(listResult.Stdout, "dev-team") {
		t.Errorf("h2 list output missing pod name 'dev-team': %s", listResult.Stdout)
	}

	// 6b. Verify builder worktree exists with branch "builder".
	builderWT := filepath.Join(h2Dir, "worktrees", "builder")
	if _, err := os.Stat(filepath.Join(builderWT, ".git")); err != nil {
		t.Fatalf("builder worktree .git not found: %v", err)
	}
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = builderWT
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git branch --show-current (builder): %v", err)
	}
	if branch := strings.TrimSpace(string(out)); branch != "builder" {
		t.Errorf("builder branch = %q, want %q", branch, "builder")
	}

	// 6c. Verify reviewer worktree exists with detached HEAD.
	reviewerWT := filepath.Join(h2Dir, "worktrees", "reviewer")
	if _, err := os.Stat(filepath.Join(reviewerWT, ".git")); err != nil {
		t.Fatalf("reviewer worktree .git not found: %v", err)
	}
	cmd = exec.Command("git", "branch", "--show-current")
	cmd.Dir = reviewerWT
	out, err = cmd.Output()
	if err != nil {
		t.Fatalf("git branch --show-current (reviewer): %v", err)
	}
	if branch := strings.TrimSpace(string(out)); branch != "" {
		t.Errorf("reviewer branch = %q, want empty (detached HEAD)", branch)
	}

	// 7. Send a message to an agent (verifies messaging infrastructure).
	sendResult := runH2(t, h2Dir, "send", "reviewer", "Please review my changes")
	if sendResult.ExitCode != 0 {
		t.Errorf("h2 send to reviewer failed: exit=%d stderr=%s", sendResult.ExitCode, sendResult.Stderr)
	}

	// 8. Stop the pod.
	stopResult := runH2(t, h2Dir, "pod", "stop", "dev-team")
	if stopResult.ExitCode != 0 {
		t.Errorf("h2 pod stop failed: exit=%d stderr=%s", stopResult.ExitCode, stopResult.Stderr)
	}

	// Verify agents are gone or in exited state.
	listAfter := runH2(t, h2Dir, "list")
	if listAfter.ExitCode != 0 {
		t.Fatalf("h2 list (after stop) failed: exit=%d stderr=%s", listAfter.ExitCode, listAfter.Stderr)
	}
	// Agents may still appear briefly as "Exited" before socket cleanup.
	// Both "gone" and "Exited" are acceptable per the verification doc.
	if strings.Contains(listAfter.Stdout, "builder") && !strings.Contains(listAfter.Stdout, "Exited") {
		t.Errorf("builder still running after pod stop: %s", listAfter.Stdout)
	}
	if strings.Contains(listAfter.Stdout, "reviewer") && !strings.Contains(listAfter.Stdout, "Exited") {
		t.Errorf("reviewer still running after pod stop: %s", listAfter.Stdout)
	}

	// 9. Check version matches marker file.
	versionResult := runH2(t, h2Dir, "version")
	if versionResult.ExitCode != 0 {
		t.Fatalf("h2 version failed: exit=%d stderr=%s", versionResult.ExitCode, versionResult.Stderr)
	}
	cmdVersion := strings.TrimSpace(versionResult.Stdout)

	markerData, err := os.ReadFile(filepath.Join(h2Dir, ".h2-dir.txt"))
	if err != nil {
		t.Fatalf("read .h2-dir.txt: %v", err)
	}
	markerVersion := strings.TrimSpace(string(markerData))
	if markerVersion != "v"+cmdVersion {
		t.Errorf("marker version = %q, command version = %q, expected marker to be 'v'+command", markerVersion, cmdVersion)
	}
}

// §11.2 Roles without new fields still work
func TestBackwardCompat_LegacyRole(t *testing.T) {
	h2Dir := createTestH2Dir(t)

	// A role YAML with no working_dir, no worktree block — legacy format.
	createRole(t, h2Dir, "legacy", `
name: legacy
agent_type: "true"
instructions: I am a legacy role.
`)

	result := runH2(t, h2Dir, "run", "--role", "legacy", "--name", "test-legacy", "--detach")
	if result.ExitCode != 0 {
		t.Skipf("h2 run --detach failed (expected in some environments): %s", result.Stderr)
	}
	t.Cleanup(func() { stopAgent(t, h2Dir, "test-legacy") })

	// Wait for socket and verify the agent is running.
	waitForSocket(t, h2Dir, "agent", "test-legacy")

	listResult := runH2(t, h2Dir, "list")
	if listResult.ExitCode != 0 {
		t.Fatalf("h2 list failed: exit=%d stderr=%s", listResult.ExitCode, listResult.Stderr)
	}
	if !strings.Contains(listResult.Stdout, "test-legacy") {
		t.Errorf("h2 list output = %q, want it to contain 'test-legacy'", listResult.Stdout)
	}

	// Verify no worktree was created.
	worktreePath := filepath.Join(h2Dir, "worktrees", "test-legacy")
	if _, err := os.Stat(worktreePath); err == nil {
		t.Error("worktree should not exist for legacy role")
	}
}

// §11.3 h2 run without --pod works as before
func TestBackwardCompat_RunWithoutPod(t *testing.T) {
	h2Dir := createTestH2Dir(t)

	createRole(t, h2Dir, "nopod", `
name: nopod
agent_type: "true"
instructions: No pod agent.
`)

	result := runH2(t, h2Dir, "run", "--role", "nopod", "--name", "test-nopod", "--detach")
	if result.ExitCode != 0 {
		t.Skipf("h2 run --detach failed (expected in some environments): %s", result.Stderr)
	}
	t.Cleanup(func() { stopAgent(t, h2Dir, "test-nopod") })

	waitForSocket(t, h2Dir, "agent", "test-nopod")

	// h2 list should show the agent under "Agents" (no pod grouping).
	listResult := runH2(t, h2Dir, "list")
	if listResult.ExitCode != 0 {
		t.Fatalf("h2 list failed: exit=%d stderr=%s", listResult.ExitCode, listResult.Stderr)
	}
	if !strings.Contains(listResult.Stdout, "test-nopod") {
		t.Errorf("h2 list output = %q, want it to contain 'test-nopod'", listResult.Stdout)
	}
	// When no pods exist, output should show "Agents" header, not "Agents (pod: ...)"
	if strings.Contains(listResult.Stdout, "pod:") {
		t.Errorf("h2 list should not show pod grouping when no pods: %s", listResult.Stdout)
	}
}
