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
		"projects",
		"worktrees",
		filepath.Join("pods", "roles"),
		filepath.Join("pods", "templates"),
	}
}

// setupFakeHome sets HOME and H2_ROOT_DIR to a temp dir so route registration
// doesn't touch the real filesystem.
func setupFakeHome(t *testing.T) string {
	t.Helper()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("H2_ROOT_DIR", "")
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
