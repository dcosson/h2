package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"h2/internal/config"
)

func extractClaudeAllowedCommands(t *testing.T, settingsPath string) []string {
	t.Helper()
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}
	perms, ok := parsed["permissions"].(map[string]any)
	if !ok {
		t.Fatal("settings.json missing permissions object")
	}
	allow, ok := perms["allow"].([]any)
	if !ok {
		t.Fatal("settings.json missing permissions.allow array")
	}
	re := regexp.MustCompile(`^Bash\(([^ )]+) \*\)$`)
	var cmds []string
	for _, item := range allow {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("permissions.allow contains non-string value: %#v", item)
		}
		m := re.FindStringSubmatch(s)
		if len(m) != 2 {
			t.Fatalf("unexpected command allow format %q", s)
		}
		cmds = append(cmds, m[1])
	}
	sort.Strings(cmds)
	return cmds
}

func extractCodexAllowedCommands(t *testing.T, requirementsPath string) []string {
	t.Helper()
	data, err := os.ReadFile(requirementsPath)
	if err != nil {
		t.Fatalf("read requirements.toml: %v", err)
	}
	re := regexp.MustCompile(`"([^"]+)"`)
	matches := re.FindAllStringSubmatch(string(data), -1)
	var cmds []string
	for _, m := range matches {
		if len(m) == 2 {
			cmds = append(cmds, m[1])
		}
	}
	sort.Strings(cmds)
	return cmds
}

// expectedDirs returns the subdirectories that h2 init should create.
func expectedDirs() []string {
	return []string{
		"roles",
		"sessions",
		"sockets",
		filepath.Join("claude-config", "default"),
		filepath.Join("codex-config", "default"),
		filepath.Join("profiles-shared", "default", "skills"),
		"projects",
		"worktrees",
		"pods",
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

func TestInitCmd_NoArgs_RequiresDirOrGlobal(t *testing.T) {
	setupFakeHome(t)

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no args and no --global")
	}
	if !strings.Contains(err.Error(), "--global") {
		t.Errorf("expected error mentioning --global, got: %v", err)
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
	expectedTarget := filepath.Join("..", "..", "profiles-shared", "default", "CLAUDE_AND_AGENTS.md")
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

func TestInitCmd_CreatesClaudeSettingsAndCodexRequirements(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := filepath.Join(fakeHome, "myh2")

	cmd := newInitCmd()
	cmd.SetArgs([]string{dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init command failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "claude-config", "default", "settings.json")); err != nil {
		t.Fatalf("expected claude settings.json to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "codex-config", "default", "config.toml")); err != nil {
		t.Fatalf("expected codex config.toml to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "codex-config", "default", "requirements.toml")); err != nil {
		t.Fatalf("expected codex requirements.toml to exist: %v", err)
	}
}

func TestInitCmd_CreatesCLAUDEMDSymlink(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := filepath.Join(fakeHome, "myh2")

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init command failed: %v", err)
	}

	claudeMDPath := filepath.Join(dir, "claude-config", "default", "CLAUDE.md")
	target, err := os.Readlink(claudeMDPath)
	if err != nil {
		t.Fatalf("expected CLAUDE.md to be a symlink: %v", err)
	}
	expectedTarget := filepath.Join("..", "..", "profiles-shared", "default", "CLAUDE_AND_AGENTS.md")
	if target != expectedTarget {
		t.Errorf("CLAUDE.md symlink target = %q, want %q", target, expectedTarget)
	}
}

func TestInitCmd_CreatesSharedSkillsSymlinks(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := filepath.Join(fakeHome, "myh2")

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init command failed: %v", err)
	}

	tests := []struct {
		path string
	}{
		{path: filepath.Join(dir, "claude-config", "default", "skills")},
		{path: filepath.Join(dir, "codex-config", "default", "skills")},
	}
	for _, tt := range tests {
		target, err := os.Readlink(tt.path)
		if err != nil {
			t.Fatalf("expected %s to be a symlink: %v", tt.path, err)
		}
		expectedTarget := filepath.Join("..", "..", "profiles-shared", "default", "skills")
		if target != expectedTarget {
			t.Errorf("%s symlink target = %q, want %q", tt.path, target, expectedTarget)
		}
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
		"Wrote profiles-shared/default/CLAUDE_AND_AGENTS.md",
		"Symlinked claude-config/default/CLAUDE.md",
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
	os.WriteFile(filepath.Join(dir, "terminal.json"), []byte("{}"), 0o644)

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

// --- --update-config tests ---

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

func TestInitCmd_UpdateConfigRequiresH2Dir(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := filepath.Join(fakeHome, "notanh2dir")
	os.MkdirAll(dir, 0o755)

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{dir, "--update-config"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --update-config used on non-h2 dir")
	}
	if !strings.Contains(err.Error(), "not an h2 directory") {
		t.Errorf("error = %q, want it to contain 'not an h2 directory'", err.Error())
	}
}

func TestInitCmd_UpdateConfigRefreshesManagedDefaults(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := initH2Dir(t, fakeHome)

	// Overwrite generated files with stale content.
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("stale config"), 0o644); err != nil {
		t.Fatalf("write stale config: %v", err)
	}
	sharedPath := filepath.Join(dir, "profiles-shared", "default", "CLAUDE_AND_AGENTS.md")
	if err := os.WriteFile(sharedPath, []byte("stale instructions"), 0o644); err != nil {
		t.Fatalf("write stale instructions: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "claude-config", "default", "settings.json"), []byte("stale settings"), 0o644); err != nil {
		t.Fatalf("write stale claude settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "codex-config", "default", "config.toml"), []byte("stale codex config"), 0o644); err != nil {
		t.Fatalf("write stale codex config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "codex-config", "default", "requirements.toml"), []byte("stale codex requirements"), 0o644); err != nil {
		t.Fatalf("write stale codex requirements: %v", err)
	}

	// Replace symlinks with files so update must restore symlink structure.
	claudeMDPath := filepath.Join(dir, "claude-config", "default", "CLAUDE.md")
	_ = os.Remove(claudeMDPath)
	if err := os.WriteFile(claudeMDPath, []byte("not a symlink"), 0o644); err != nil {
		t.Fatalf("write claude md as file: %v", err)
	}
	agentsMDPath := filepath.Join(dir, "codex-config", "default", "AGENTS.md")
	_ = os.Remove(agentsMDPath)
	if err := os.WriteFile(agentsMDPath, []byte("not a symlink"), 0o644); err != nil {
		t.Fatalf("write agents md as file: %v", err)
	}

	// Replace default role content with stale data and keep a custom role.
	for _, ext := range []string{".yaml", ".yaml.tmpl"} {
		_ = os.Remove(filepath.Join(dir, "roles", "default"+ext))
	}
	if err := os.WriteFile(filepath.Join(dir, "roles", "default.yaml"), []byte("role_name: default\ndescription: stale\n"), 0o644); err != nil {
		t.Fatalf("write stale default role: %v", err)
	}
	customRolePath := filepath.Join(dir, "roles", "custom.yaml")
	if err := os.WriteFile(customRolePath, []byte("role_name: custom\ndescription: keep\n"), 0o644); err != nil {
		t.Fatalf("write custom role: %v", err)
	}

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dir, "--update-config"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--update-config failed: %v", err)
	}

	gotConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	if string(gotConfig) != config.ConfigTemplate(initStyleOpinionated) {
		t.Fatalf("config.yaml was not refreshed")
	}

	gotInstructions, err := os.ReadFile(sharedPath)
	if err != nil {
		t.Fatalf("read CLAUDE_AND_AGENTS.md: %v", err)
	}
	if string(gotInstructions) != config.InstructionsTemplateWithStyle(initStyleOpinionated) {
		t.Fatalf("shared instructions were not refreshed")
	}
	gotClaudeSettings, err := os.ReadFile(filepath.Join(dir, "claude-config", "default", "settings.json"))
	if err != nil {
		t.Fatalf("read claude settings: %v", err)
	}
	if string(gotClaudeSettings) != config.ClaudeSettingsTemplate(initStyleOpinionated) {
		t.Fatalf("claude settings were not refreshed")
	}
	gotCodexConfig, err := os.ReadFile(filepath.Join(dir, "codex-config", "default", "config.toml"))
	if err != nil {
		t.Fatalf("read codex config: %v", err)
	}
	if string(gotCodexConfig) != config.CodexConfigTemplate(initStyleOpinionated) {
		t.Fatalf("codex config was not refreshed")
	}
	gotCodexReqs, err := os.ReadFile(filepath.Join(dir, "codex-config", "default", "requirements.toml"))
	if err != nil {
		t.Fatalf("read codex requirements: %v", err)
	}
	if string(gotCodexReqs) != config.CodexRequirementsTemplate(initStyleOpinionated) {
		t.Fatalf("codex requirements were not refreshed")
	}

	defaultRoleFound := false
	for _, ext := range []string{".yaml.tmpl", ".yaml"} {
		data, readErr := os.ReadFile(filepath.Join(dir, "roles", "default"+ext))
		if readErr == nil {
			defaultRoleFound = true
			if !strings.Contains(string(data), "{{ .RoleName }}") {
				t.Fatalf("default role was not regenerated from template")
			}
			break
		}
	}
	if !defaultRoleFound {
		t.Fatalf("expected default role to exist after --update-config")
	}

	customRoleData, err := os.ReadFile(customRolePath)
	if err != nil {
		t.Fatalf("read custom role: %v", err)
	}
	if !strings.Contains(string(customRoleData), "description: keep") {
		t.Fatalf("custom role should remain untouched")
	}

	for _, check := range []struct {
		path   string
		target string
	}{
		{claudeMDPath, filepath.Join("..", "..", "profiles-shared", "default", "CLAUDE_AND_AGENTS.md")},
		{agentsMDPath, filepath.Join("..", "..", "profiles-shared", "default", "CLAUDE_AND_AGENTS.md")},
		{filepath.Join(dir, "claude-config", "default", "skills"), filepath.Join("..", "..", "profiles-shared", "default", "skills")},
		{filepath.Join(dir, "codex-config", "default", "skills"), filepath.Join("..", "..", "profiles-shared", "default", "skills")},
	} {
		target, linkErr := os.Readlink(check.path)
		if linkErr != nil {
			t.Fatalf("expected %s to be symlink: %v", check.path, linkErr)
		}
		if target != check.target {
			t.Fatalf("symlink %s target = %q, want %q", check.path, target, check.target)
		}
	}
}

func TestInitCmd_StyleMinimal_GeneratesMinimalInstructions(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := filepath.Join(fakeHome, "myh2-min")

	cmd := newInitCmd()
	cmd.SetArgs([]string{dir, "--style", "minimal"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --style minimal failed: %v", err)
	}

	sharedPath := filepath.Join(dir, "profiles-shared", "default", "CLAUDE_AND_AGENTS.md")
	data, err := os.ReadFile(sharedPath)
	if err != nil {
		t.Fatalf("read shared instructions: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Shared Agent Instructions") {
		t.Errorf("minimal style should write minimal shared instructions, got:\n%s", content)
	}
}

func TestInitCmd_UpdateConfig_StyleMinimal(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := initH2Dir(t, fakeHome)

	cmd := newInitCmd()
	cmd.SetArgs([]string{dir, "--update-config", "--style", "minimal"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--update-config --style minimal failed: %v", err)
	}

	sharedPath := filepath.Join(dir, "profiles-shared", "default", "CLAUDE_AND_AGENTS.md")
	data, err := os.ReadFile(sharedPath)
	if err != nil {
		t.Fatalf("read shared instructions: %v", err)
	}
	if !strings.Contains(string(data), "Shared Agent Instructions") {
		t.Errorf("expected minimal shared instructions content, got:\n%s", string(data))
	}
}

func TestInitCmd_Opinionated_PopulatesSharedSkills(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := filepath.Join(fakeHome, "myh2-op")

	cmd := newInitCmd()
	cmd.SetArgs([]string{dir, "--style", "opinionated"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --style opinionated failed: %v", err)
	}

	checks := []string{
		filepath.Join(dir, "profiles-shared", "default", "skills", "shaping", "SKILL.md"),
		filepath.Join(dir, "profiles-shared", "default", "skills", "stress-test", "SKILL.md"),
	}
	for _, p := range checks {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected opinionated skill file %s: %v", p, err)
		}
	}
}

func TestInitCmd_Minimal_LeavesSharedSkillsEmpty(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := filepath.Join(fakeHome, "myh2-min-skills")

	cmd := newInitCmd()
	cmd.SetArgs([]string{dir, "--style", "minimal"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --style minimal failed: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "profiles-shared", "default", "skills"))
	if err != nil {
		t.Fatalf("read shared skills dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("minimal style should keep shared skills empty, got %d entries", len(entries))
	}
}

func TestInitCmd_Minimal_CommandPolicyMatchesBetweenClaudeAndCodex(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := filepath.Join(fakeHome, "myh2-min-policy")

	cmd := newInitCmd()
	cmd.SetArgs([]string{dir, "--style", "minimal"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --style minimal failed: %v", err)
	}

	claude := extractClaudeAllowedCommands(t, filepath.Join(dir, "claude-config", "default", "settings.json"))
	codex := extractCodexAllowedCommands(t, filepath.Join(dir, "codex-config", "default", "requirements.toml"))
	if strings.Join(claude, ",") != strings.Join(codex, ",") {
		t.Fatalf("minimal command policy mismatch: claude=%v codex=%v", claude, codex)
	}
}

func TestInitCmd_Opinionated_CommandPolicyMatchesBetweenClaudeAndCodex(t *testing.T) {
	fakeHome := setupFakeHome(t)
	dir := filepath.Join(fakeHome, "myh2-op-policy")

	cmd := newInitCmd()
	cmd.SetArgs([]string{dir, "--style", "opinionated"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --style opinionated failed: %v", err)
	}

	claude := extractClaudeAllowedCommands(t, filepath.Join(dir, "claude-config", "default", "settings.json"))
	codex := extractCodexAllowedCommands(t, filepath.Join(dir, "codex-config", "default", "requirements.toml"))
	if strings.Join(claude, ",") != strings.Join(codex, ",") {
		t.Fatalf("opinionated command policy mismatch: claude=%v codex=%v", claude, codex)
	}
}
