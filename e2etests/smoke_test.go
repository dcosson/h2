package e2etests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// §1.1 Basic version output
func TestVersion_BasicOutput(t *testing.T) {
	result := runH2(t, "", "version")
	if result.ExitCode != 0 {
		t.Fatalf("h2 version failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	output := strings.TrimSpace(result.Stdout)
	if output == "" {
		t.Fatal("h2 version output is empty")
	}
	// Version should be a semver-like string (e.g. "0.1.0").
	if !strings.Contains(output, ".") {
		t.Errorf("h2 version output = %q, expected semver-like format", output)
	}
}

// §1.2 Version is consistent with marker file
func TestVersion_ConsistentWithMarker(t *testing.T) {
	h2Dir := createTestH2Dir(t)

	// Read marker file.
	markerData, err := os.ReadFile(filepath.Join(h2Dir, ".h2-dir.txt"))
	if err != nil {
		t.Fatalf("read .h2-dir.txt: %v", err)
	}
	markerVersion := strings.TrimSpace(string(markerData)) // e.g. "v0.1.0"

	// Get version from command.
	result := runH2(t, "", "version")
	if result.ExitCode != 0 {
		t.Fatalf("h2 version failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	cmdVersion := strings.TrimSpace(result.Stdout) // e.g. "0.1.0"

	// Marker has "v" prefix, command output does not.
	if markerVersion != "v"+cmdVersion {
		t.Errorf("marker version = %q, command version = %q, expected marker to be 'v'+command", markerVersion, cmdVersion)
	}
}

// §2.1 Init in a new directory — all expected dirs/files exist
func TestInit_CreatesFullStructure(t *testing.T) {
	h2Dir := createTestH2Dir(t)

	for _, sub := range []string{
		".h2-dir.txt",
		"config.yaml",
		"roles",
		"sessions",
		"sockets",
		filepath.Join("claude-config", "default"),
		"projects",
		"worktrees",
		filepath.Join("pods", "roles"),
		filepath.Join("pods", "templates"),
	} {
		path := filepath.Join(h2Dir, sub)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to exist: %v", sub, err)
		}
	}

	// h2 list should work with a freshly initialized dir.
	result := runH2(t, h2Dir, "list")
	if result.ExitCode != 0 {
		t.Fatalf("h2 list failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "No running agents") {
		t.Errorf("h2 list output = %q, want 'No running agents'", result.Stdout)
	}
}

// §2.2 Init refuses to overwrite
func TestInit_RefusesOverwrite(t *testing.T) {
	h2Dir := createTestH2Dir(t)

	result := runH2(t, "", "init", h2Dir)
	if result.ExitCode == 0 {
		t.Fatal("expected h2 init to fail on existing h2 dir")
	}
	combined := result.Stdout + result.Stderr
	if !strings.Contains(combined, "already an h2 directory") {
		t.Errorf("error output = %q, want 'already an h2 directory'", combined)
	}
}

// §2.3 Init with --global
func TestInit_Global(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	result := runH2WithEnv(t, "", []string{"HOME=" + fakeHome}, "init", "--global")
	if result.ExitCode != 0 {
		t.Fatalf("h2 init --global failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}

	h2Dir := filepath.Join(fakeHome, ".h2")
	markerPath := filepath.Join(h2Dir, ".h2-dir.txt")
	if _, err := os.Stat(markerPath); err != nil {
		t.Errorf("expected %s to exist after init --global: %v", markerPath, err)
	}

	// Verify subdirectories.
	for _, sub := range []string{"roles", "sessions", "sockets", "projects", "worktrees"} {
		if _, err := os.Stat(filepath.Join(h2Dir, sub)); err != nil {
			t.Errorf("expected %s/ to exist: %v", sub, err)
		}
	}
}

// §2.4 Init creates parent directories
func TestInit_CreatesParentDirs(t *testing.T) {
	base := t.TempDir()
	nested := filepath.Join(base, "deep", "nested", "path")

	result := runH2(t, "", "init", nested)
	if result.ExitCode != 0 {
		t.Fatalf("h2 init failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}

	if _, err := os.Stat(filepath.Join(nested, ".h2-dir.txt")); err != nil {
		t.Errorf("expected .h2-dir.txt to exist in nested path: %v", err)
	}
}
