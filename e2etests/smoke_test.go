package e2etests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSmoke_InitAndList(t *testing.T) {
	h2Dir := createTestH2Dir(t)

	// Verify init created expected structure.
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

	// h2 list should work with a freshly initialized dir (no agents).
	result := runH2(t, h2Dir, "list")
	if result.ExitCode != 0 {
		t.Fatalf("h2 list failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "No running agents") {
		t.Errorf("h2 list output = %q, want it to contain 'No running agents'", result.Stdout)
	}
}

func TestSmoke_InitRefusesOverwrite(t *testing.T) {
	h2Dir := createTestH2Dir(t)

	// Second init should fail.
	result := runH2(t, "", "init", h2Dir)
	if result.ExitCode == 0 {
		t.Fatal("expected h2 init to fail on existing h2 dir")
	}
	combined := result.Stdout + result.Stderr
	if !strings.Contains(combined, "already an h2 directory") {
		t.Errorf("error output = %q, want it to contain 'already an h2 directory'", combined)
	}
}

func TestSmoke_Version(t *testing.T) {
	result := runH2(t, "", "version")
	if result.ExitCode != 0 {
		t.Fatalf("h2 version failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	output := strings.TrimSpace(result.Stdout)
	if output == "" {
		t.Error("h2 version output is empty")
	}
}
