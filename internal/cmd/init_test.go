package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2/internal/config"
)

// expectedDirs returns the subdirectories that h2 init should create.
func expectedDirs() []string {
	return []string{
		"roles",
		"sessions",
		"sockets",
		filepath.Join("claude-config", "default"),
		filepath.Join("codex-config", "default"),
		"projects",
		"worktrees",
		filepath.Join("pods", "roles"),
		filepath.Join("pods", "templates"),
	}
}

// setupFakeHome isolates tests from the real filesystem by setting HOME,
// H2_ROOT_DIR, and H2_DIR to temp directories. Returns the fake home dir.
func setupFakeHome(t *testing.T) string {
	t.Helper()
	fakeHome := t.TempDir()
	fakeRootDir := filepath.Join(fakeHome, ".h2")
	t.Setenv("HOME", fakeHome)
	t.Setenv("H2_ROOT_DIR", fakeRootDir)
	t.Setenv("H2_DIR", "")
	return fakeHome
}

func TestInitCmd_CreatesStructure(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := filepath.Join(fakeHome, "myh2")

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init command failed: %v", err)
	}

	// Marker file should exist.
	if !config.IsH2Dir(dir) {
		t.Error("expected .h2-dir.txt marker to exist")
	}

	// config.yaml should exist.
	configPath := filepath.Join(dir, "config.yaml")
	if _, err := os.Stat(configPath); err != nil {
		t.Errorf("expected config.yaml to exist: %v", err)
	}

	// All expected directories should exist.
	for _, sub := range expectedDirs() {
		path := filepath.Join(dir, sub)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected directory %s to exist: %v", sub, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", sub)
		}
	}

	// Output should mention the path.
	abs, _ := filepath.Abs(dir)
	if !strings.Contains(buf.String(), abs) {
		t.Errorf("output = %q, want it to contain %q", buf.String(), abs)
	}

	// Default role should be created (as .yaml.tmpl since it has template syntax).
	roleFound := false
	for _, ext := range []string{".yaml.tmpl", ".yaml"} {
		if _, err := os.Stat(filepath.Join(dir, "roles", "default"+ext)); err == nil {
			roleFound = true
			break
		}
	}
	if !roleFound {
		t.Error("expected default role to be created in roles/")
	}
}

func TestInitCmd_RefusesOverwrite(t *testing.T) {
	setupFakeHome(t)
	dir := t.TempDir()

	// Pre-create marker so it's already an h2 dir.
	if err := config.WriteMarker(dir); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{dir})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when h2 dir already exists")
	}
	if !strings.Contains(err.Error(), "already an h2 directory") {
		t.Errorf("error = %q, want it to contain 'already an h2 directory'", err.Error())
	}
}

func TestInitCmd_Global(t *testing.T) {
	fakeHome := setupFakeHome(t)

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--global"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --global failed: %v", err)
	}

	h2Dir := filepath.Join(fakeHome, ".h2")
	if !config.IsH2Dir(h2Dir) {
		t.Error("expected ~/.h2 to be an h2 directory")
	}

	// Verify subdirectories.
	for _, sub := range expectedDirs() {
		path := filepath.Join(h2Dir, sub)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected directory %s to exist: %v", sub, err)
		}
	}

	// --global should register as "root" prefix.
	routes, err := config.ReadRoutes(h2Dir)
	if err != nil {
		t.Fatalf("ReadRoutes: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Prefix != "root" {
		t.Errorf("prefix = %q, want %q", routes[0].Prefix, "root")
	}
}

func TestInitCmd_NoArgs_InitsGlobal(t *testing.T) {
	fakeHome := setupFakeHome(t)

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init with no args failed: %v", err)
	}

	h2Dir := filepath.Join(fakeHome, ".h2")
	if !config.IsH2Dir(h2Dir) {
		t.Error("expected ~/.h2 to be an h2 directory")
	}

	// No args should register as "root" prefix.
	routes, err := config.ReadRoutes(h2Dir)
	if err != nil {
		t.Fatalf("ReadRoutes: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Prefix != "root" {
		t.Errorf("prefix = %q, want %q", routes[0].Prefix, "root")
	}
}

func TestInitCmd_CreatesParentDirs(t *testing.T) {
	fakeHome := setupFakeHome(t)
	nested := filepath.Join(fakeHome, "a", "b", "c")

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{nested})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init with nested path failed: %v", err)
	}

	if !config.IsH2Dir(nested) {
		t.Error("expected nested dir to be an h2 directory")
	}
}

func TestInitCmd_RegistersRoute(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := filepath.Join(fakeHome, "myproject")
	rootDir := filepath.Join(fakeHome, ".h2")

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init command failed: %v", err)
	}

	// Route should be registered.
	routes, err := config.ReadRoutes(rootDir)
	if err != nil {
		t.Fatalf("ReadRoutes: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Prefix != "myproject" {
		t.Errorf("prefix = %q, want %q", routes[0].Prefix, "myproject")
	}

	abs, _ := filepath.Abs(dir)
	if routes[0].Path != abs {
		t.Errorf("path = %q, want %q", routes[0].Path, abs)
	}

	// Output should mention the prefix.
	if !strings.Contains(buf.String(), "myproject") {
		t.Errorf("output = %q, want it to contain prefix", buf.String())
	}
}

func TestInitCmd_PrefixFlag(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := filepath.Join(fakeHome, "myproject")
	rootDir := filepath.Join(fakeHome, ".h2")

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir, "--prefix", "custom-name"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init command failed: %v", err)
	}

	routes, err := config.ReadRoutes(rootDir)
	if err != nil {
		t.Fatalf("ReadRoutes: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Prefix != "custom-name" {
		t.Errorf("prefix = %q, want %q", routes[0].Prefix, "custom-name")
	}
}

func TestInitCmd_PrefixConflict(t *testing.T) {
	fakeHome := setupFakeHome(t)
	rootDir := filepath.Join(fakeHome, ".h2")
	os.MkdirAll(rootDir, 0o755)

	// Pre-register a route with prefix "taken".
	if err := config.RegisterRoute(rootDir, config.Route{Prefix: "taken", Path: "/other"}); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	dir := filepath.Join(fakeHome, "newproject")
	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{dir, "--prefix", "taken"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for conflicting prefix")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("error = %q, want it to contain 'already registered'", err.Error())
	}
}

func TestInitCmd_AutoIncrementPrefix(t *testing.T) {
	fakeHome := setupFakeHome(t)
	rootDir := filepath.Join(fakeHome, ".h2")
	os.MkdirAll(rootDir, 0o755)

	// Pre-register "myproject" prefix.
	if err := config.RegisterRoute(rootDir, config.Route{Prefix: "myproject", Path: "/other"}); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	dir := filepath.Join(fakeHome, "myproject")
	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init command failed: %v", err)
	}

	routes, err := config.ReadRoutes(rootDir)
	if err != nil {
		t.Fatalf("ReadRoutes: %v", err)
	}
	// Should have 2 routes: the pre-registered one and the new one.
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}
	if routes[1].Prefix != "myproject-2" {
		t.Errorf("prefix = %q, want %q", routes[1].Prefix, "myproject-2")
	}
}

func TestInitCmd_RootInit(t *testing.T) {
	fakeHome := setupFakeHome(t)
	rootDir := filepath.Join(fakeHome, ".h2")

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{rootDir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init root dir failed: %v", err)
	}

	routes, err := config.ReadRoutes(rootDir)
	if err != nil {
		t.Fatalf("ReadRoutes: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Prefix != "root" {
		t.Errorf("prefix = %q, want %q", routes[0].Prefix, "root")
	}
}

func TestInitCmd_WritesCLAUDEMD(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := filepath.Join(fakeHome, "myh2")

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init command failed: %v", err)
	}

	// CLAUDE.md should exist.
	claudeMDPath := filepath.Join(dir, "claude-config", "default", "CLAUDE.md")
	data, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("expected CLAUDE.md to exist: %v", err)
	}
	if len(data) == 0 {
		t.Error("CLAUDE.md should not be empty")
	}
	content := string(data)
	if !strings.Contains(content, "h2 Messaging Protocol") {
		t.Error("CLAUDE.md should contain h2 Messaging Protocol")
	}
}

func TestInitCmd_CreatesAGENTSMDSymlink(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := filepath.Join(fakeHome, "myh2")

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init command failed: %v", err)
	}

	// AGENTS.md should be a symlink.
	agentsMDPath := filepath.Join(dir, "codex-config", "default", "AGENTS.md")
	target, err := os.Readlink(agentsMDPath)
	if err != nil {
		t.Fatalf("expected AGENTS.md to be a symlink: %v", err)
	}
	expectedTarget := filepath.Join("..", "..", "claude-config", "default", "CLAUDE.md")
	if target != expectedTarget {
		t.Errorf("AGENTS.md symlink target = %q, want %q", target, expectedTarget)
	}

	// Symlink should resolve to valid content.
	data, err := os.ReadFile(agentsMDPath)
	if err != nil {
		t.Fatalf("could not read through AGENTS.md symlink: %v", err)
	}
	if len(data) == 0 {
		t.Error("AGENTS.md (via symlink) should not be empty")
	}
}

func TestInitCmd_VerboseOutput(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := filepath.Join(fakeHome, "myh2")

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init command failed: %v", err)
	}

	output := buf.String()
	expectedPhrases := []string{
		"Creating h2 directory at",
		"Created roles/",
		"Created sessions/",
		"Wrote config.yaml",
		"Wrote claude-config/default/CLAUDE.md",
		"Symlinked codex-config/default/AGENTS.md",
		"Wrote roles/default.yaml", // may be default.yaml.tmpl
		"Registered route",
		"Initialized h2 directory at",
	}
	for _, phrase := range expectedPhrases {
		if !strings.Contains(output, phrase) {
			t.Errorf("output missing %q\nfull output:\n%s", phrase, output)
		}
	}
}

func TestInitCmd_FailsOnUnexpectedContent(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := filepath.Join(fakeHome, "populated")
	os.MkdirAll(dir, 0o755)

	// Create an unexpected file.
	os.WriteFile(filepath.Join(dir, "unexpected.txt"), []byte("hello"), 0o644)

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{dir})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for directory with unexpected content")
	}
	if !strings.Contains(err.Error(), "already has content") {
		t.Errorf("error = %q, want it to contain 'already has content'", err.Error())
	}
}

func TestInitCmd_AllowsRootDirFiles(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := filepath.Join(fakeHome, "myh2")
	os.MkdirAll(dir, 0o755)

	// Pre-create expected root-dir files.
	os.WriteFile(filepath.Join(dir, "routes.jsonl"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dir, "terminal-colors.json"), []byte("{}"), 0o644)

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init should succeed with only root-dir files present: %v", err)
	}

	if !config.IsH2Dir(dir) {
		t.Error("expected directory to be initialized as h2 dir")
	}
}

func TestInitCmd_FailsOnUnexpectedSubdir(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := filepath.Join(fakeHome, "populated")
	os.MkdirAll(filepath.Join(dir, "some-subdir"), 0o755)

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{dir})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for directory with unexpected subdirectory")
	}
	if !strings.Contains(err.Error(), "already has content") {
		t.Errorf("error = %q, want it to contain 'already has content'", err.Error())
	}
}

// --- --generate tests ---

// initH2Dir is a helper that runs a full h2 init and returns the abs path.
func initH2Dir(t *testing.T, fakeHome string) string {
	t.Helper()
	dir := filepath.Join(fakeHome, "myh2")
	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init command failed: %v", err)
	}
	return dir
}

func TestInitCmd_GenerateRequiresH2Dir(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := filepath.Join(fakeHome, "notanh2dir")
	os.MkdirAll(dir, 0o755)

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{dir, "--generate", "roles"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --generate used on non-h2 dir")
	}
	if !strings.Contains(err.Error(), "not an h2 directory") {
		t.Errorf("error = %q, want it to contain 'not an h2 directory'", err.Error())
	}
}

func TestInitCmd_GenerateInstructions(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := initH2Dir(t, fakeHome)

	// Remove CLAUDE.md to test regeneration.
	claudeMDPath := filepath.Join(dir, "claude-config", "default", "CLAUDE.md")
	os.Remove(claudeMDPath)

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir, "--generate", "instructions"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--generate instructions failed: %v", err)
	}

	// CLAUDE.md should be regenerated.
	if _, err := os.Stat(claudeMDPath); err != nil {
		t.Fatalf("expected CLAUDE.md to be regenerated: %v", err)
	}

	if !strings.Contains(buf.String(), "Wrote claude-config/default/CLAUDE.md") {
		t.Errorf("output should mention writing CLAUDE.md, got: %s", buf.String())
	}
}

func TestInitCmd_GenerateInstructionsSkipsExisting(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := initH2Dir(t, fakeHome)

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir, "--generate", "instructions"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--generate instructions failed: %v", err)
	}

	if !strings.Contains(buf.String(), "Skipped") {
		t.Errorf("output should mention skipping, got: %s", buf.String())
	}
}

func TestInitCmd_GenerateInstructionsForce(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := initH2Dir(t, fakeHome)

	// Overwrite CLAUDE.md with custom content.
	claudeMDPath := filepath.Join(dir, "claude-config", "default", "CLAUDE.md")
	os.WriteFile(claudeMDPath, []byte("custom content"), 0o644)

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir, "--generate", "instructions", "--force"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--generate instructions --force failed: %v", err)
	}

	// CLAUDE.md should be overwritten.
	data, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	if string(data) == "custom content" {
		t.Error("CLAUDE.md should have been overwritten with --force")
	}
	if !strings.Contains(buf.String(), "Wrote claude-config/default/CLAUDE.md") {
		t.Errorf("output should mention writing, got: %s", buf.String())
	}
}

func TestInitCmd_GenerateRoles(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := initH2Dir(t, fakeHome)

	// Remove default role (either extension) to test regeneration.
	for _, ext := range []string{".yaml", ".yaml.tmpl"} {
		os.Remove(filepath.Join(dir, "roles", "default"+ext))
	}

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir, "--generate", "roles"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--generate roles failed: %v", err)
	}

	// Default role should be regenerated (as either extension).
	roleFound := false
	for _, ext := range []string{".yaml.tmpl", ".yaml"} {
		if _, err := os.Stat(filepath.Join(dir, "roles", "default"+ext)); err == nil {
			roleFound = true
			break
		}
	}
	if !roleFound {
		t.Fatal("expected default role to be regenerated")
	}

	if !strings.Contains(buf.String(), "Wrote roles/default.yaml") {
		t.Errorf("output should mention writing role, got: %s", buf.String())
	}
}

func TestInitCmd_GenerateRolesSkipsExisting(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := initH2Dir(t, fakeHome)

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir, "--generate", "roles"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--generate roles failed: %v", err)
	}

	if !strings.Contains(buf.String(), "Skipped") {
		t.Errorf("output should mention skipping, got: %s", buf.String())
	}
}

func TestInitCmd_GenerateConfig(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := initH2Dir(t, fakeHome)

	// Remove config to test regeneration.
	configPath := filepath.Join(dir, "config.yaml")
	os.Remove(configPath)

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir, "--generate", "config"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--generate config failed: %v", err)
	}

	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("expected config.yaml to be regenerated: %v", err)
	}
}

func TestInitCmd_GenerateAll(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := initH2Dir(t, fakeHome)

	// Remove files to test regeneration.
	os.Remove(filepath.Join(dir, "config.yaml"))
	os.Remove(filepath.Join(dir, "claude-config", "default", "CLAUDE.md"))
	os.Remove(filepath.Join(dir, "codex-config", "default", "AGENTS.md"))
	os.Remove(filepath.Join(dir, "roles", "default.yaml"))
	os.Remove(filepath.Join(dir, "roles", "default.yaml.tmpl"))

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir, "--generate", "all"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--generate all failed: %v", err)
	}

	output := buf.String()
	for _, phrase := range []string{"config.yaml", "CLAUDE.md", "AGENTS.md", "default.yaml"} {
		if !strings.Contains(output, phrase) {
			t.Errorf("output missing %q\nfull output:\n%s", phrase, output)
		}
	}
}

func TestInitCmd_GenerateInvalidType(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := initH2Dir(t, fakeHome)

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{dir, "--generate", "invalid"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid --generate type")
	}
	if !strings.Contains(err.Error(), "unknown --generate type") {
		t.Errorf("error = %q, want it to contain 'unknown --generate type'", err.Error())
	}
}
