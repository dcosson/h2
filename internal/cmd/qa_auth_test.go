package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestQAAuth_AuthedTagDiffersFromBase(t *testing.T) {
	path := "/some/project/h2-qa.yaml"
	base := projectImageTag(path)
	authed := authedImageTag(path)

	if base == authed {
		t.Errorf("base and authed tags should differ: both %q", base)
	}
}

func TestQAAuth_AuthedTagDerivedFromConfig(t *testing.T) {
	path1 := "/project-a/h2-qa.yaml"
	path2 := "/project-b/h2-qa.yaml"

	tag1 := authedImageTag(path1)
	tag2 := authedImageTag(path2)

	if tag1 == tag2 {
		t.Errorf("authed tags should differ for different projects: both %q", tag1)
	}
}

func TestAuthContainerName_IncludesHash(t *testing.T) {
	name := authContainerName("/some/project/h2-qa.yaml")
	if !strings.HasPrefix(name, "h2-qa-auth-") {
		t.Errorf("container name should start with h2-qa-auth-, got %q", name)
	}
	// Should differ by project.
	name2 := authContainerName("/other/project/h2-qa.yaml")
	if name == name2 {
		t.Errorf("container names should differ for different projects: both %q", name)
	}
}

func TestQAAuth_ErrorWhenBaseImageMissing(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: Dockerfile
`), 0o644)

	err := runQAAuth(configPath, false)
	if err == nil {
		t.Fatal("expected error when base image missing")
	}
	errMsg := err.Error()
	// Could be "docker not installed" or "base image not found".
	if errMsg == "" {
		t.Fatal("expected non-empty error message")
	}
}

// --- Mock-based unit tests ---

func mockDeps(baseExists, authedExists bool) qaAuthDeps {
	return qaAuthDeps{
		dockerAvailable: func() error { return nil },
		imageExists: func(tag string) bool {
			if strings.HasSuffix(tag, ":base") {
				return baseExists
			}
			if strings.HasSuffix(tag, ":authed") {
				return authedExists
			}
			return false
		},
		dockerExec: func(args ...string) (string, string, error) {
			// Simulate successful commit.
			return "", "", nil
		},
		runInteractive: func(name, image string) error {
			return nil // successful exit
		},
		removeContainer: func(name string) {
			// no-op
		},
	}
}

func TestQAAuthDeps_BaseImageMissing(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte("sandbox:\n  dockerfile: Dockerfile\n"), 0o644)

	deps := mockDeps(false, false)
	err := runQAAuthWithDeps(configPath, false, deps)
	if err == nil {
		t.Fatal("expected error for missing base image")
	}
	if !strings.Contains(err.Error(), "base image") {
		t.Errorf("error should mention base image, got: %v", err)
	}
}

func TestQAAuthDeps_AuthedExistsNoForce(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte("sandbox:\n  dockerfile: Dockerfile\n"), 0o644)

	deps := mockDeps(true, true)
	err := runQAAuthWithDeps(configPath, false, deps)
	if err == nil {
		t.Fatal("expected error when authed exists without --force")
	}
	if !strings.Contains(err.Error(), "authed image already exists") {
		t.Errorf("error should mention authed image exists, got: %v", err)
	}
}

func TestQAAuthDeps_AuthedExistsWithForce(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte("sandbox:\n  dockerfile: Dockerfile\n"), 0o644)

	deps := mockDeps(true, true)
	err := runQAAuthWithDeps(configPath, true, deps) // force=true
	if err != nil {
		t.Fatalf("expected success with --force, got: %v", err)
	}
}

func TestQAAuthDeps_CommitFailure(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte("sandbox:\n  dockerfile: Dockerfile\n"), 0o644)

	var removedContainers []string
	deps := mockDeps(true, false)
	deps.dockerExec = func(args ...string) (string, string, error) {
		if len(args) > 0 && args[0] == "commit" {
			return "", "no such container", fmt.Errorf("commit failed")
		}
		return "", "", nil
	}
	deps.removeContainer = func(name string) {
		removedContainers = append(removedContainers, name)
	}

	err := runQAAuthWithDeps(configPath, false, deps)
	if err == nil {
		t.Fatal("expected error on commit failure")
	}
	if !strings.Contains(err.Error(), "docker commit failed") {
		t.Errorf("error should mention commit failure, got: %v", err)
	}
	// Should clean up container after commit failure.
	if len(removedContainers) < 2 {
		t.Errorf("expected at least 2 removeContainer calls (pre-cleanup + post-failure), got %d", len(removedContainers))
	}
}

func TestQAAuthDeps_ContainerExitNonZero_StillCommits(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte("sandbox:\n  dockerfile: Dockerfile\n"), 0o644)

	var committed bool
	deps := mockDeps(true, false)
	deps.runInteractive = func(name, image string) error {
		return fmt.Errorf("exit status 1") // non-zero exit
	}
	deps.dockerExec = func(args ...string) (string, string, error) {
		if len(args) > 0 && args[0] == "commit" {
			committed = true
		}
		return "", "", nil
	}

	err := runQAAuthWithDeps(configPath, false, deps)
	if err != nil {
		t.Fatalf("expected success (warn-but-succeed), got: %v", err)
	}
	if !committed {
		t.Error("commit should still be called when container exits non-zero")
	}
}

func TestQAAuthDeps_ContainerCleanupAfterSuccess(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte("sandbox:\n  dockerfile: Dockerfile\n"), 0o644)

	var removedContainers []string
	deps := mockDeps(true, false)
	deps.removeContainer = func(name string) {
		removedContainers = append(removedContainers, name)
	}

	err := runQAAuthWithDeps(configPath, false, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should remove container twice: pre-cleanup and post-commit.
	if len(removedContainers) != 2 {
		t.Errorf("expected 2 removeContainer calls, got %d", len(removedContainers))
	}
}

// --- Integration test ---

// TestQAAuth_Integration tests the full auth flow with a trivial image.
// Skipped if Docker is not available.
func TestQAAuth_Integration(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if err := dockerAvailable(); err != nil {
		t.Skip("docker daemon not running")
	}

	dir := t.TempDir()

	// Build a base image first.
	dockerfilePath := filepath.Join(dir, "Dockerfile")
	os.WriteFile(dockerfilePath, []byte("FROM alpine:latest\nRUN echo 'base'\n"), 0o644)

	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: Dockerfile
`), 0o644)

	// Build base image.
	if err := runQASetup(configPath); err != nil {
		t.Fatalf("setup: %v", err)
	}

	baseTag := projectImageTag(configPath)
	authTag := authedImageTag(configPath)
	containerName := authContainerName(configPath)

	t.Cleanup(func() {
		exec.Command("docker", "rmi", "-f", baseTag).Run()
		exec.Command("docker", "rmi", "-f", authTag).Run()
		exec.Command("docker", "rm", "-f", containerName).Run()
	})

	// Simulate auth by running a non-interactive container that exits immediately.
	exec.Command("docker", "rm", "-f", containerName).Run()

	cmd := exec.Command("docker", "run", "--name", containerName, baseTag, "echo", "authed")
	if err := cmd.Run(); err != nil {
		t.Fatalf("docker run: %v", err)
	}

	// Commit manually (simulating what runQAAuth does after interactive exit).
	_, stderr, err := dockerExec("commit", containerName, authTag)
	if err != nil {
		t.Fatalf("docker commit: %s", stderr)
	}
	exec.Command("docker", "rm", "-f", containerName).Run()

	// Verify authed image exists.
	if !imageExists(authTag) {
		t.Errorf("authed image %q should exist after commit", authTag)
	}
}
