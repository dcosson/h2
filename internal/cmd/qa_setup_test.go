package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunQASetup_BuildArgsInConfig(t *testing.T) {
	// Verify the config properly loads build_args that would be
	// passed to docker build.
	dir := t.TempDir()
	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: Dockerfile
  build_args:
    APP_VERSION: "2.0"
    DEBUG: "true"
`), 0o644)

	cfg, err := LoadQAConfig(configPath)
	if err != nil {
		t.Fatalf("LoadQAConfig: %v", err)
	}

	if cfg.Sandbox.BuildArgs["APP_VERSION"] != "2.0" {
		t.Errorf("APP_VERSION = %q, want %q", cfg.Sandbox.BuildArgs["APP_VERSION"], "2.0")
	}
	if cfg.Sandbox.BuildArgs["DEBUG"] != "true" {
		t.Errorf("DEBUG = %q, want %q", cfg.Sandbox.BuildArgs["DEBUG"], "true")
	}
}

func TestRunQASetup_DockerfileResolvesRelativeToConfig(t *testing.T) {
	// Config in a subdirectory — Dockerfile path should resolve from there.
	dir := t.TempDir()
	subDir := filepath.Join(dir, "project", "qa")
	os.MkdirAll(subDir, 0o755)
	configPath := filepath.Join(subDir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: Dockerfile
`), 0o644)

	cfg, err := LoadQAConfig(configPath)
	if err != nil {
		t.Fatalf("LoadQAConfig: %v", err)
	}

	resolved := cfg.ResolvedDockerfile()
	expected := filepath.Join(subDir, "Dockerfile")
	if resolved != expected {
		t.Errorf("ResolvedDockerfile = %q, want %q", resolved, expected)
	}
}

// TestRunQASetup_Integration builds a trivial Dockerfile to verify the full pipeline.
// Skipped if Docker is not available.
func TestRunQASetup_Integration(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	// Quick check that daemon is running.
	if err := dockerAvailable(); err != nil {
		t.Skip("docker daemon not running")
	}

	dir := t.TempDir()

	// Write a trivial Dockerfile.
	dockerfilePath := filepath.Join(dir, "Dockerfile")
	os.WriteFile(dockerfilePath, []byte("FROM alpine:latest\nRUN echo 'qa test'\n"), 0o644)

	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: Dockerfile
`), 0o644)

	// Run setup.
	err := runQASetup(configPath)
	if err != nil {
		t.Fatalf("runQASetup: %v", err)
	}

	// Verify image exists with correct tag.
	tag := projectImageTag(configPath)
	if !imageExists(tag) {
		t.Errorf("image %q should exist after setup", tag)
	}

	// Cleanup: remove the image.
	t.Cleanup(func() {
		exec.Command("docker", "rmi", "-f", tag).Run()
	})
}

func TestRunQASetup_IntegrationWithBuildArgs(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if err := dockerAvailable(); err != nil {
		t.Skip("docker daemon not running")
	}

	dir := t.TempDir()

	// Dockerfile that uses a build arg.
	dockerfilePath := filepath.Join(dir, "Dockerfile")
	os.WriteFile(dockerfilePath, []byte("FROM alpine:latest\nARG TEST_VAR=default\nRUN echo $TEST_VAR > /test.txt\n"), 0o644)

	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: Dockerfile
  build_args:
    TEST_VAR: "custom_value"
`), 0o644)

	err := runQASetup(configPath)
	if err != nil {
		t.Fatalf("runQASetup: %v", err)
	}

	tag := projectImageTag(configPath)
	if !imageExists(tag) {
		t.Errorf("image %q should exist after setup", tag)
	}

	// Verify the build arg was used by checking the file content in the image.
	stdout, _, err := dockerExec("run", "--rm", tag, "cat", "/test.txt")
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}
	if !strings.Contains(stdout, "custom_value") {
		t.Errorf("expected build arg to be used, got: %q", stdout)
	}

	t.Cleanup(func() {
		exec.Command("docker", "rmi", "-f", tag).Run()
	})
}
