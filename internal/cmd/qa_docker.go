package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// dockerQueryTimeout bounds quick docker queries ("docker info", image
// inspect), so a wedged Docker daemon produces an error instead of hanging
// h2. Var so tests can shorten it.
var dockerQueryTimeout = 10 * time.Second

// projectHash returns the hex-encoded SHA256 hash prefix for a config file path.
// Used by projectImageTag and authedImageTag for deterministic Docker image tagging.
func projectHash(configPath string) string {
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		absPath = configPath
	}
	hash := sha256.Sum256([]byte(absPath))
	return fmt.Sprintf("%x", hash[:4])
}

// projectImageTag returns a deterministic Docker image tag based on the config
// file's absolute path. Format: h2-qa-<hash>:base
func projectImageTag(configPath string) string {
	return fmt.Sprintf("h2-qa-%s:base", projectHash(configPath))
}

// authedImageTag returns the Docker image tag for the auth-committed layer.
// Format: h2-qa-<hash>:authed
func authedImageTag(configPath string) string {
	return fmt.Sprintf("h2-qa-%s:authed", projectHash(configPath))
}

// imageExists checks whether a Docker image with the given tag exists locally.
func imageExists(tag string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), dockerQueryTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "docker", "inspect", "--type=image", tag).Run() == nil
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

// dockerExecStreaming runs a docker command with stdout and stderr streamed to
// os.Stderr in real-time. Returns the exit error, if any.
func dockerExecStreaming(args ...string) error {
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// dockerAvailable checks whether the docker CLI is available and the daemon is running.
func dockerAvailable() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker is not installed or not in PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), dockerQueryTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "info")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("docker daemon is not responding (docker info timed out after %v)", dockerQueryTimeout)
		}
		return fmt.Errorf("docker daemon is not running: %s", stderr.String())
	}
	return nil
}

// formatImageSize formats a byte count as a human-readable string (e.g., "1.2 GB").
func formatImageSize(sizeStr string) string {
	bytes, err := strconv.ParseFloat(sizeStr, 64)
	if err != nil {
		return sizeStr
	}

	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", bytes/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", bytes/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", bytes/KB)
	default:
		return fmt.Sprintf("%.0f bytes", bytes)
	}
}
