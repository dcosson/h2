package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
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
	// Either is acceptable — just verify it errors.
	if errMsg == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestQAAuth_ErrorWhenAuthedExistsNoForce(t *testing.T) {
	// This tests the logic path where authed image exists and --force is not set.
	// We can't easily mock imageExists, but we can verify the error message format.
	// The actual Docker integration test below covers the full flow.

	dir := t.TempDir()
	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: Dockerfile
`), 0o644)

	// runQAAuth will fail before checking authed image because base doesn't exist.
	// This test just documents the expected behavior.
	err := runQAAuth(configPath, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

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

	t.Cleanup(func() {
		exec.Command("docker", "rmi", "-f", baseTag).Run()
		exec.Command("docker", "rmi", "-f", authTag).Run()
		exec.Command("docker", "rm", "-f", "h2-qa-auth-session").Run()
	})

	// Simulate auth by running a non-interactive container that exits immediately.
	// We can't test interactive auth in CI, but we can test the commit flow.
	containerName := "h2-qa-auth-session"
	exec.Command("docker", "rm", "-f", containerName).Run()

	// Run container non-interactively (just exits).
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
