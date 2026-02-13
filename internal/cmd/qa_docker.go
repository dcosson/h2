package cmd

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// projectImageTag returns a deterministic Docker image tag based on the config
// file's absolute path. Format: h2-qa-<hash>:base
func projectImageTag(configPath string) string {
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		absPath = configPath
	}
	hash := sha256.Sum256([]byte(absPath))
	return fmt.Sprintf("h2-qa-%x:base", hash[:4])
}

// authedImageTag returns the Docker image tag for the auth-committed layer.
// Format: h2-qa-<hash>:authed
func authedImageTag(configPath string) string {
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		absPath = configPath
	}
	hash := sha256.Sum256([]byte(absPath))
	return fmt.Sprintf("h2-qa-%x:authed", hash[:4])
}

// imageExists checks whether a Docker image with the given tag exists locally.
func imageExists(tag string) bool {
	cmd := exec.Command("docker", "inspect", "--type=image", tag)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// dockerExec runs a docker command and returns stdout, stderr, and any error.
func dockerExec(args ...string) (string, string, error) {
	cmd := exec.Command("docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

// dockerAvailable checks whether the docker CLI is available and the daemon is running.
func dockerAvailable() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker is not installed or not in PATH")
	}
	_, stderr, err := dockerExec("info")
	if err != nil {
		return fmt.Errorf("docker daemon is not running: %s", stderr)
	}
	return nil
}
