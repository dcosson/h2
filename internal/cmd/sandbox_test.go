package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2/internal/config"
	"h2/internal/sandbox"
)

// setupSandboxTestEnv creates a minimal h2 dir for sandbox tests.
// Returns the baseDir that contains the sandboxes/ directory.
func setupSandboxTestEnv(t *testing.T) string {
	t.Helper()

	config.ResetResolveCache()
	t.Cleanup(config.ResetResolveCache)

	h2Dir := filepath.Join(t.TempDir(), "myh2")
	for _, sub := range []string{"roles", "sessions", "sockets", "claude-config/default"} {
		if err := os.MkdirAll(filepath.Join(h2Dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.WriteMarker(h2Dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("H2_DIR", h2Dir)

	return h2Dir
}

func TestSandboxCreateCmd_Basic(t *testing.T) {
	baseDir := setupSandboxTestEnv(t)

	cmd := newSandboxCreateCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"test-bench", "--preset", "hooks"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox create: %v", err)
	}

	// Verify sandbox was created.
	sbDir := filepath.Join(sandbox.SandboxesDir(baseDir), "test-bench")
	if _, err := os.Stat(sbDir); os.IsNotExist(err) {
		t.Error("sandbox directory should exist after create")
	}
}

func TestSandboxCreateCmd_DefaultPreset(t *testing.T) {
	setupSandboxTestEnv(t)

	cmd := newSandboxCreateCmd()
	cmd.SetArgs([]string{"default-preset-test"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox create: %v", err)
	}

	// Default preset is "hooks".
	sb, err := sandbox.Get("default-preset-test", "")
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Preset != "hooks" {
		t.Errorf("default preset = %q, want %q", sb.Preset, "hooks")
	}
}

func TestSandboxCreateCmd_InvalidPreset(t *testing.T) {
	setupSandboxTestEnv(t)

	cmd := newSandboxCreateCmd()
	cmd.SetArgs([]string{"bad-preset", "--preset", "nonexistent"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid preset")
	}
}

func TestSandboxCreateCmd_NoArgs(t *testing.T) {
	setupSandboxTestEnv(t)

	cmd := newSandboxCreateCmd()
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no name provided")
	}
}

func TestSandboxCreateCmd_WithAuthFrom(t *testing.T) {
	baseDir := setupSandboxTestEnv(t)

	// Create auth source.
	authDir := filepath.Join(t.TempDir(), "auth")
	os.MkdirAll(authDir, 0o755)
	authContent := `{"oauthAccount": {"accountUuid": "test-uuid"}}`
	os.WriteFile(filepath.Join(authDir, ".claude.json"), []byte(authContent), 0o644)

	cmd := newSandboxCreateCmd()
	cmd.SetArgs([]string{"auth-test", "--auth-from", authDir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox create: %v", err)
	}

	// Verify auth was copied.
	claudeJSON := filepath.Join(sandbox.SandboxesDir(baseDir), "auth-test", "claude-config", "default", ".claude.json")
	data, err := os.ReadFile(claudeJSON)
	if err != nil {
		t.Fatalf("read .claude.json: %v", err)
	}
	if string(data) != authContent {
		t.Errorf(".claude.json = %q, want %q", string(data), authContent)
	}
}

func TestSandboxListCmd_Empty(t *testing.T) {
	setupSandboxTestEnv(t)

	cmd := newSandboxListCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox list: %v", err)
	}
}

func TestSandboxListCmd_ShowsSandboxes(t *testing.T) {
	setupSandboxTestEnv(t)

	// Create sandboxes.
	sandbox.Create("alpha", "empty", "", "")
	sandbox.Create("bravo", "hooks", "", "")

	cmd := newSandboxCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"list"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox list: %v", err)
	}

	// Output goes to stdout, not cmd.Out() for tabwriter, so we verify
	// the sandboxes exist via the library instead.
	infos, err := sandbox.List("")
	if err != nil {
		t.Fatalf("sandbox.List: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 sandboxes, got %d", len(infos))
	}
	if infos[0].Name != "alpha" || infos[1].Name != "bravo" {
		t.Errorf("sandboxes = %v, %v; want alpha, bravo", infos[0].Name, infos[1].Name)
	}
}

func TestSandboxResetCmd_Basic(t *testing.T) {
	setupSandboxTestEnv(t)

	// Create a sandbox first.
	sb, err := sandbox.Create("reset-test", "hooks", "", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Add session data that should be wiped.
	os.WriteFile(filepath.Join(sb.Dir, "sessions", "test.json"), []byte("{}"), 0o644)

	cmd := newSandboxResetCmd()
	cmd.SetArgs([]string{"reset-test"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox reset: %v", err)
	}

	// Session data should be gone.
	if _, err := os.Stat(filepath.Join(sb.Dir, "sessions", "test.json")); !os.IsNotExist(err) {
		t.Error("session data should be wiped after reset")
	}
}

func TestSandboxResetCmd_ChangePreset(t *testing.T) {
	setupSandboxTestEnv(t)

	sandbox.Create("preset-change", "empty", "", "")

	cmd := newSandboxResetCmd()
	cmd.SetArgs([]string{"preset-change", "--preset", "haiku"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox reset: %v", err)
	}

	sb, err := sandbox.Get("preset-change", "")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sb.Preset != "haiku" {
		t.Errorf("preset = %q, want %q", sb.Preset, "haiku")
	}
}

func TestSandboxResetCmd_Nonexistent(t *testing.T) {
	setupSandboxTestEnv(t)

	cmd := newSandboxResetCmd()
	cmd.SetArgs([]string{"nonexistent"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent sandbox")
	}
}

func TestSandboxResetCmd_NoArgs(t *testing.T) {
	setupSandboxTestEnv(t)

	cmd := newSandboxResetCmd()
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no name provided")
	}
}

func TestSandboxDestroyCmd_Basic(t *testing.T) {
	baseDir := setupSandboxTestEnv(t)

	sb, err := sandbox.Create("destroy-me", "empty", "", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	cmd := newSandboxDestroyCmd()
	cmd.SetArgs([]string{"destroy-me"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox destroy: %v", err)
	}

	// Verify sandbox dir is gone.
	sbDir := filepath.Join(sandbox.SandboxesDir(baseDir), "destroy-me")
	if _, err := os.Stat(sbDir); !os.IsNotExist(err) {
		t.Error("sandbox should be destroyed")
	}
	_ = sb // used only for verification
}

func TestSandboxDestroyCmd_Nonexistent(t *testing.T) {
	setupSandboxTestEnv(t)

	cmd := newSandboxDestroyCmd()
	cmd.SetArgs([]string{"nonexistent"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent sandbox")
	}
}

func TestSandboxDestroyCmd_NoArgs(t *testing.T) {
	setupSandboxTestEnv(t)

	cmd := newSandboxDestroyCmd()
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no name provided")
	}
}

func TestSandboxExecCmd_Basic(t *testing.T) {
	setupSandboxTestEnv(t)

	sandbox.Create("exec-test", "empty", "", "")

	cmd := newSandboxExecCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"exec-test", "--", "echo", "hello"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox exec: %v", err)
	}

	if got := strings.TrimSpace(out.String()); got != "hello" {
		t.Errorf("output = %q, want %q", got, "hello")
	}
}

func TestSandboxExecCmd_WithoutDashDash(t *testing.T) {
	setupSandboxTestEnv(t)

	sandbox.Create("exec-nodash", "empty", "", "")

	cmd := newSandboxExecCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"exec-nodash", "echo", "hello"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox exec without --: %v", err)
	}

	if got := strings.TrimSpace(out.String()); got != "hello" {
		t.Errorf("output = %q, want %q", got, "hello")
	}
}

func TestSandboxExecCmd_Nonexistent(t *testing.T) {
	setupSandboxTestEnv(t)

	cmd := newSandboxExecCmd()
	cmd.SetArgs([]string{"nonexistent", "--", "echo", "hello"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent sandbox")
	}
}

func TestSandboxExecCmd_NoCommand(t *testing.T) {
	setupSandboxTestEnv(t)

	sandbox.Create("exec-nocmd", "empty", "", "")

	cmd := newSandboxExecCmd()
	cmd.SetArgs([]string{"exec-nocmd", "--"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no command specified")
	}
}

func TestSandboxExecCmd_NoArgs(t *testing.T) {
	setupSandboxTestEnv(t)

	cmd := newSandboxExecCmd()
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no args provided")
	}
}

func TestSandboxShellCmd_NoArgs(t *testing.T) {
	setupSandboxTestEnv(t)

	cmd := newSandboxShellCmd()
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no name provided")
	}
}

func TestSandboxShellCmd_Nonexistent(t *testing.T) {
	setupSandboxTestEnv(t)

	cmd := newSandboxShellCmd()
	cmd.SetArgs([]string{"nonexistent"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent sandbox")
	}
}

func TestParseSandboxExecArgs(t *testing.T) {
	tests := []struct {
		desc     string
		args     []string
		wantName string
		wantCmd  []string
	}{
		{
			desc:     "with double dash",
			args:     []string{"bench-1", "--", "echo", "hello"},
			wantName: "bench-1",
			wantCmd:  []string{"echo", "hello"},
		},
		{
			desc:     "without double dash",
			args:     []string{"bench-1", "echo", "hello"},
			wantName: "bench-1",
			wantCmd:  []string{"echo", "hello"},
		},
		{
			desc:     "name only with dash dash",
			args:     []string{"bench-1", "--"},
			wantName: "bench-1",
			wantCmd:  []string{},
		},
		{
			desc:     "name only",
			args:     []string{"bench-1"},
			wantName: "bench-1",
			wantCmd:  []string{},
		},
		{
			desc:     "empty",
			args:     []string{},
			wantName: "",
			wantCmd:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			name, cmdArgs := parseSandboxExecArgs(tt.args)
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if len(cmdArgs) != len(tt.wantCmd) {
				t.Fatalf("cmdArgs len = %d, want %d", len(cmdArgs), len(tt.wantCmd))
			}
			for i, arg := range cmdArgs {
				if arg != tt.wantCmd[i] {
					t.Errorf("cmdArgs[%d] = %q, want %q", i, arg, tt.wantCmd[i])
				}
			}
		})
	}
}

func TestSandboxShellEnv(t *testing.T) {
	env := sandboxShellEnv("/test/sandbox/dir")

	found := false
	for _, e := range env {
		if e == "H2_DIR=/test/sandbox/dir" {
			found = true
			break
		}
	}
	if !found {
		t.Error("H2_DIR not found in sandbox shell env")
	}
}

func TestSandboxCmd_FullFlow(t *testing.T) {
	setupSandboxTestEnv(t)

	// Create.
	createCmd := newSandboxCreateCmd()
	createCmd.SetArgs([]string{"flow-test", "--preset", "opus"})
	if err := createCmd.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Verify via Get.
	sb, err := sandbox.Get("flow-test", "")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sb.Preset != "opus" {
		t.Errorf("preset = %q, want opus", sb.Preset)
	}

	// Verify roles were created (opus has multiple roles).
	rolesDir := filepath.Join(sb.Dir, "roles")
	entries, err := os.ReadDir(rolesDir)
	if err != nil {
		t.Fatalf("read roles: %v", err)
	}
	roleNames := make([]string, 0, len(entries))
	for _, e := range entries {
		roleNames = append(roleNames, strings.TrimSuffix(e.Name(), ".yaml"))
	}
	for _, want := range []string{"default", "concierge", "coder", "reviewer"} {
		found := false
		for _, got := range roleNames {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing role %q in %v", want, roleNames)
		}
	}

	// Reset with different preset.
	resetCmd := newSandboxResetCmd()
	resetCmd.SetArgs([]string{"flow-test", "--preset", "haiku"})
	if err := resetCmd.Execute(); err != nil {
		t.Fatalf("reset: %v", err)
	}

	sb, err = sandbox.Get("flow-test", "")
	if err != nil {
		t.Fatalf("get after reset: %v", err)
	}
	if sb.Preset != "haiku" {
		t.Errorf("preset after reset = %q, want haiku", sb.Preset)
	}

	// List should show it.
	infos, err := sandbox.List("")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(infos) != 1 || infos[0].Name != "flow-test" {
		t.Errorf("list = %v, want [flow-test]", infos)
	}

	// Destroy.
	destroyCmd := newSandboxDestroyCmd()
	destroyCmd.SetArgs([]string{"flow-test"})
	if err := destroyCmd.Execute(); err != nil {
		t.Fatalf("destroy: %v", err)
	}

	// Verify gone.
	_, err = sandbox.Get("flow-test", "")
	if err == nil {
		t.Fatal("sandbox should not exist after destroy")
	}
}
