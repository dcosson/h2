package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverPlans_FindsMDFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "messaging.md"), []byte("# Messaging"), 0o644)
	os.WriteFile(filepath.Join(dir, "lifecycle.md"), []byte("# Lifecycle"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a plan"), 0o644)
	os.MkdirAll(filepath.Join(dir, "subdir"), 0o755)

	plans, err := DiscoverPlans(dir)
	if err != nil {
		t.Fatalf("DiscoverPlans: %v", err)
	}

	if len(plans) != 2 {
		t.Fatalf("expected 2 plans, got %d: %v", len(plans), plans)
	}

	// Plans should be without .md extension.
	for _, p := range plans {
		if strings.HasSuffix(p, ".md") {
			t.Errorf("plan name should not have .md suffix: %q", p)
		}
	}
}

func TestDiscoverPlans_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	plans, err := DiscoverPlans(dir)
	if err != nil {
		t.Fatalf("DiscoverPlans: %v", err)
	}
	if len(plans) != 0 {
		t.Errorf("expected 0 plans, got %d", len(plans))
	}
}

func TestDiscoverPlans_NonexistentDir(t *testing.T) {
	plans, err := DiscoverPlans("/nonexistent/dir")
	if err != nil {
		t.Fatalf("DiscoverPlans should not error for nonexistent dir: %v", err)
	}
	if plans != nil {
		t.Errorf("expected nil plans, got %v", plans)
	}
}

func TestCreateResultsDir_Format(t *testing.T) {
	dir := t.TempDir()
	cfg := &QAConfig{
		ResultsDir: "results/",
		configDir:  dir,
	}

	resultsDir, err := createResultsDir(cfg, "messaging")
	if err != nil {
		t.Fatalf("createResultsDir: %v", err)
	}

	// Should be under the resolved results dir.
	if !strings.HasPrefix(resultsDir, filepath.Join(dir, "results")) {
		t.Errorf("results dir should be under config's results dir: %s", resultsDir)
	}

	// Should contain the plan name.
	if !strings.Contains(filepath.Base(resultsDir), "messaging") {
		t.Errorf("results dir should contain plan name: %s", resultsDir)
	}

	// Should contain a timestamp-like pattern (YYYY-MM-DD_HHMM).
	base := filepath.Base(resultsDir)
	if len(base) < 15 { // "2026-02-13_1500" is 15 chars
		t.Errorf("results dir name too short, missing timestamp: %s", base)
	}

	// Evidence dir should exist.
	evidenceDir := filepath.Join(resultsDir, "evidence")
	if _, err := os.Stat(evidenceDir); os.IsNotExist(err) {
		t.Error("evidence directory should be created")
	}
}

func TestUpdateLatestSymlink(t *testing.T) {
	dir := t.TempDir()
	resultsBase := filepath.Join(dir, "results")
	os.MkdirAll(resultsBase, 0o755)

	cfg := &QAConfig{
		ResultsDir: "results/",
		configDir:  dir,
	}

	runDir := filepath.Join(resultsBase, "2026-02-13_1500-messaging")
	os.MkdirAll(runDir, 0o755)

	updateLatestSymlink(cfg, runDir)

	latestLink := filepath.Join(resultsBase, "latest")
	target, err := os.Readlink(latestLink)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}

	if target != "2026-02-13_1500-messaging" {
		t.Errorf("latest symlink target = %q, want relative path", target)
	}
}

func TestUpdateLatestSymlink_OverwritesPrevious(t *testing.T) {
	dir := t.TempDir()
	resultsBase := filepath.Join(dir, "results")
	os.MkdirAll(resultsBase, 0o755)

	cfg := &QAConfig{
		ResultsDir: "results/",
		configDir:  dir,
	}

	// Create first run and symlink.
	run1 := filepath.Join(resultsBase, "run1")
	os.MkdirAll(run1, 0o755)
	updateLatestSymlink(cfg, run1)

	// Create second run and update symlink.
	run2 := filepath.Join(resultsBase, "run2")
	os.MkdirAll(run2, 0o755)
	updateLatestSymlink(cfg, run2)

	latestLink := filepath.Join(resultsBase, "latest")
	target, err := os.Readlink(latestLink)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}

	if target != "run2" {
		t.Errorf("latest symlink should point to run2, got %q", target)
	}
}

func TestBuildVolumeArgs(t *testing.T) {
	cfg := &QAConfig{
		Sandbox: QASandboxConfig{
			Volumes: []string{
				"./src:/app/src",
				"/absolute/path:/container/path",
			},
		},
		configDir: "/home/user/project",
	}

	args := buildVolumeArgs(cfg, "/tmp/results")

	// First should be results volume.
	if len(args) < 2 || args[0] != "-v" || args[1] != "/tmp/results:/root/results" {
		t.Errorf("first volume should be results: %v", args[:2])
	}

	// Relative volume should be resolved.
	found := false
	for i, arg := range args {
		if arg == "-v" && i+1 < len(args) && strings.Contains(args[i+1], "/home/user/project/src:/app/src") {
			found = true
		}
	}
	if !found {
		t.Errorf("relative volume should be resolved against configDir: %v", args)
	}

	// Absolute volume should be unchanged.
	foundAbs := false
	for i, arg := range args {
		if arg == "-v" && i+1 < len(args) && args[i+1] == "/absolute/path:/container/path" {
			foundAbs = true
		}
	}
	if !foundAbs {
		t.Errorf("absolute volume should be unchanged: %v", args)
	}
}

func TestBuildEnvArgs_Passthrough(t *testing.T) {
	// Set a test env var.
	os.Setenv("H2_QA_TEST_VAR", "test_value")
	defer os.Unsetenv("H2_QA_TEST_VAR")

	cfg := &QAConfig{
		Sandbox: QASandboxConfig{
			Env: []string{
				"H2_QA_TEST_VAR",    // passthrough
				"FOO=bar",           // explicit
				"MISSING_VAR",       // not in host env
			},
		},
	}

	args := buildEnvArgs(cfg)

	// Should have passthrough resolved.
	foundPassthrough := false
	foundExplicit := false
	for i, arg := range args {
		if arg == "-e" && i+1 < len(args) {
			if args[i+1] == "H2_QA_TEST_VAR=test_value" {
				foundPassthrough = true
			}
			if args[i+1] == "FOO=bar" {
				foundExplicit = true
			}
		}
	}

	if !foundPassthrough {
		t.Errorf("passthrough env var should be resolved: %v", args)
	}
	if !foundExplicit {
		t.Errorf("explicit env var should be passed through: %v", args)
	}

	// Missing var should not be included.
	for i, arg := range args {
		if arg == "-e" && i+1 < len(args) && strings.HasPrefix(args[i+1], "MISSING_VAR") {
			t.Errorf("missing env var should not be included: %v", args)
		}
	}
}

func TestRunQARun_PlanNotFound(t *testing.T) {
	dir := t.TempDir()
	plansDir := filepath.Join(dir, "plans")
	os.MkdirAll(plansDir, 0o755)

	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: Dockerfile
plans_dir: plans/
`), 0o644)

	err := runQARun(configPath, "nonexistent-plan", true, false) // --no-docker to avoid docker dependency
	if err == nil {
		t.Fatal("expected error for missing plan")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found': %v", err)
	}
}

func TestRunQARun_ErrorWhenNoImage(t *testing.T) {
	dir := t.TempDir()
	plansDir := filepath.Join(dir, "plans")
	os.MkdirAll(plansDir, 0o755)
	os.WriteFile(filepath.Join(plansDir, "test.md"), []byte("# Test Plan"), 0o644)

	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: Dockerfile
plans_dir: plans/
`), 0o644)

	// Docker mode — should fail because no image exists.
	err := runQARun(configPath, "test", false, false)
	if err == nil {
		t.Fatal("expected error when no Docker image exists")
	}
}

func TestRunQAAll_DiscoverAndRun(t *testing.T) {
	// Mock execCommand so no real processes are spawned.
	orig := execCommand
	var claudeCalls [][]string
	execCommand = func(name string, arg ...string) *exec.Cmd {
		if name == "claude" {
			claudeCalls = append(claudeCalls, append([]string{name}, arg...))
		}
		return exec.Command("true")
	}
	t.Cleanup(func() { execCommand = orig })

	dir := t.TempDir()
	plansDir := filepath.Join(dir, "plans")
	os.MkdirAll(plansDir, 0o755)
	os.WriteFile(filepath.Join(plansDir, "plan-a.md"), []byte("# Plan A"), 0o644)
	os.WriteFile(filepath.Join(plansDir, "plan-b.md"), []byte("# Plan B"), 0o644)

	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: Dockerfile
plans_dir: plans/
`), 0o644)

	err := runQAAll(configPath, true, false)
	if err != nil {
		t.Fatalf("runQAAll: %v", err)
	}

	// Should have invoked claude once per plan.
	if len(claudeCalls) != 2 {
		t.Fatalf("expected 2 claude invocations, got %d", len(claudeCalls))
	}

	// Verify both calls use --print (non-interactive) with --system-prompt and bypassPermissions.
	for i, call := range claudeCalls {
		args := strings.Join(call, " ")
		if !strings.Contains(args, "--print") {
			t.Errorf("call %d missing --print: %v", i, call)
		}
		if !strings.Contains(args, "--system-prompt") {
			t.Errorf("call %d missing --system-prompt: %v", i, call)
		}
		if !strings.Contains(args, "bypassPermissions") {
			t.Errorf("call %d missing bypassPermissions: %v", i, call)
		}
		if strings.Contains(args, "--verbose") {
			t.Errorf("call %d should not have --verbose when verbose=false: %v", i, call)
		}
	}
}

func TestRunQAAll_VerbosePassesFlag(t *testing.T) {
	orig := execCommand
	var claudeCalls [][]string
	execCommand = func(name string, arg ...string) *exec.Cmd {
		if name == "claude" {
			claudeCalls = append(claudeCalls, append([]string{name}, arg...))
		}
		return exec.Command("true")
	}
	t.Cleanup(func() { execCommand = orig })

	dir := t.TempDir()
	plansDir := filepath.Join(dir, "plans")
	os.MkdirAll(plansDir, 0o755)
	os.WriteFile(filepath.Join(plansDir, "plan-a.md"), []byte("# Plan A"), 0o644)

	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: Dockerfile
plans_dir: plans/
`), 0o644)

	err := runQAAll(configPath, true, true) // verbose=true
	if err != nil {
		t.Fatalf("runQAAll: %v", err)
	}

	if len(claudeCalls) != 1 {
		t.Fatalf("expected 1 claude invocation, got %d", len(claudeCalls))
	}

	args := strings.Join(claudeCalls[0], " ")
	if !strings.Contains(args, "--verbose") {
		t.Errorf("expected --verbose in claude args: %v", claudeCalls[0])
	}
}
