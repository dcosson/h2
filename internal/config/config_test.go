package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2/internal/version"
)

// setupFakeHome isolates tests from the real filesystem by setting HOME,
// H2_ROOT_DIR, and H2_DIR to temp directories and resetting the resolve cache.
// Returns the fake home dir.
func setupFakeHome(t *testing.T) string {
	t.Helper()
	fakeHome := t.TempDir()
	fakeRootDir := filepath.Join(fakeHome, ".h2")
	// Create the fake h2 dir with a marker so resolveDir() finds it
	// via H2_DIR instead of walking up from CWD to the real h2 dir.
	if err := os.MkdirAll(fakeRootDir, 0o755); err != nil {
		t.Fatalf("create fake h2 dir: %v", err)
	}
	if err := WriteMarker(fakeRootDir); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	t.Setenv("HOME", fakeHome)
	t.Setenv("H2_ROOT_DIR", fakeRootDir)
	t.Setenv("H2_DIR", fakeRootDir)
	ResetResolveCache()
	t.Cleanup(ResetResolveCache)
	return fakeHome
}

func TestLoadFrom_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	yaml := `bridges:
  personal:
    telegram:
      bot_token: "123456:ABC-DEF"
      chat_id: 789
    macos_notify:
      enabled: true
users:
  dcosson: {}
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	bc, ok := cfg.Bridges["personal"]
	if !ok {
		t.Fatal("expected bridge config 'personal'")
	}

	if bc.Telegram == nil {
		t.Fatal("expected telegram config")
	}
	if bc.Telegram.BotToken != "123456:ABC-DEF" {
		t.Errorf("bot_token = %q, want %q", bc.Telegram.BotToken, "123456:ABC-DEF")
	}
	if bc.Telegram.ChatID != 789 {
		t.Errorf("chat_id = %d, want 789", bc.Telegram.ChatID)
	}

	if bc.MacOSNotify == nil {
		t.Fatal("expected macos_notify config")
	}
	if !bc.MacOSNotify.Enabled {
		t.Error("expected macos_notify.enabled = true")
	}
}

func TestLoadFrom_RuntimeEnvConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	data := `runtime:
  env:
    PATH: "/opt/h2/bin:/usr/bin"
    TEAM: backend
  env_passthrough:
    - TEAM_CONTEXT
    - EXTRA_TOKEN
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Runtime == nil {
		t.Fatal("expected runtime config")
	}
	if cfg.Runtime.Env["PATH"] != "/opt/h2/bin:/usr/bin" {
		t.Errorf("runtime.env.PATH = %q", cfg.Runtime.Env["PATH"])
	}
	if cfg.Runtime.Env["TEAM"] != "backend" {
		t.Errorf("runtime.env.TEAM = %q", cfg.Runtime.Env["TEAM"])
	}
	wantPassthrough := []string{"TEAM_CONTEXT", "EXTRA_TOKEN"}
	if len(cfg.Runtime.EnvPassthrough) != len(wantPassthrough) {
		t.Fatalf("env_passthrough = %v, want %v", cfg.Runtime.EnvPassthrough, wantPassthrough)
	}
	for i, want := range wantPassthrough {
		if cfg.Runtime.EnvPassthrough[i] != want {
			t.Errorf("env_passthrough[%d] = %q, want %q", i, cfg.Runtime.EnvPassthrough[i], want)
		}
	}
}

func TestLoadFrom_MissingFile(t *testing.T) {
	cfg, err := LoadFrom("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.Users != nil {
		t.Errorf("expected nil Users, got %v", cfg.Users)
	}
}

func TestLoadFrom_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte("{{invalid yaml"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadFrom(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadFrom_AllowedCommands_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	data := `bridges:
  personal:
    telegram:
      bot_token: "tok"
      chat_id: 1
      allowed_commands:
        - h2
        - bd
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	cmds := cfg.Bridges["personal"].Telegram.AllowedCommands
	if len(cmds) != 2 || cmds[0] != "h2" || cmds[1] != "bd" {
		t.Errorf("AllowedCommands = %v, want [h2 bd]", cmds)
	}
}

func TestLoadFrom_AllowedCommands_Invalid(t *testing.T) {
	tests := []struct {
		name string
		cmds string
	}{
		{"slash in path", `["/usr/bin/h2"]`},
		{"space in name", `["rm -rf"]`},
		{"semicolon", `["h2;echo"]`},
		{"empty string", `[""]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")

			data := `bridges:
  personal:
    telegram:
      bot_token: "tok"
      chat_id: 1
      allowed_commands: ` + tt.cmds + "\n"
			if err := os.WriteFile(path, []byte(data), 0644); err != nil {
				t.Fatal(err)
			}

			_, err := LoadFrom(path)
			if err == nil {
				t.Fatalf("expected error for allowed_commands %s", tt.cmds)
			}
		})
	}
}

func TestLoadFrom_AllowedCommands_NotSet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	data := `bridges:
  personal:
    telegram:
      bot_token: "tok"
      chat_id: 1
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	cmds := cfg.Bridges["personal"].Telegram.AllowedCommands
	if len(cmds) != 0 {
		t.Errorf("AllowedCommands = %v, want empty", cmds)
	}
}

func TestLoadFrom_NoBridges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	yaml := `users:
  alice: {}
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	if len(cfg.Bridges) != 0 {
		t.Errorf("expected no bridges, got %d", len(cfg.Bridges))
	}
}

func TestLoadFrom_ExpectsResponse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	data := `bridges:
  personal:
    telegram:
      bot_token: "tok"
      chat_id: 1
      expects_response: true
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	tc := cfg.Bridges["personal"].Telegram
	if !tc.ExpectsResponse {
		t.Error("expected ExpectsResponse=true")
	}
}

func TestLoadFrom_MultipleBridges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	data := `bridges:
  personal:
    telegram:
      bot_token: "tok1"
      chat_id: 1
  work:
    telegram:
      bot_token: "tok2"
      chat_id: 2
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	if len(cfg.Bridges) != 2 {
		t.Fatalf("expected 2 bridges, got %d", len(cfg.Bridges))
	}
	if cfg.Bridges["personal"].Telegram.BotToken != "tok1" {
		t.Errorf("personal bot_token = %q, want tok1", cfg.Bridges["personal"].Telegram.BotToken)
	}
	if cfg.Bridges["work"].Telegram.BotToken != "tok2" {
		t.Errorf("work bot_token = %q, want tok2", cfg.Bridges["work"].Telegram.BotToken)
	}
}

func TestLookupBridge(t *testing.T) {
	cfg := &Config{
		Bridges: map[string]*BridgesConfig{
			"personal": {Telegram: &TelegramConfig{BotToken: "tok"}},
		},
	}

	bc, err := cfg.LookupBridge("personal")
	if err != nil {
		t.Fatalf("LookupBridge: %v", err)
	}
	if bc.Telegram.BotToken != "tok" {
		t.Errorf("BotToken = %q, want tok", bc.Telegram.BotToken)
	}

	_, err = cfg.LookupBridge("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent bridge")
	}
}

func TestLookupBridge_NoBridges(t *testing.T) {
	cfg := &Config{}
	_, err := cfg.LookupBridge("anything")
	if err == nil {
		t.Fatal("expected error when no bridges configured")
	}
}

// --- Marker file tests ---

func TestIsH2Dir(t *testing.T) {
	dir := t.TempDir()

	if IsH2Dir(dir) {
		t.Error("expected false for dir without marker")
	}

	if err := WriteMarker(dir); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}

	if !IsH2Dir(dir) {
		t.Error("expected true for dir with marker")
	}
}

func TestReadMarkerVersion(t *testing.T) {
	dir := t.TempDir()

	if err := WriteMarker(dir); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}

	got, err := ReadMarkerVersion(dir)
	if err != nil {
		t.Fatalf("ReadMarkerVersion: %v", err)
	}
	want := version.DisplayVersion()
	if got != want {
		t.Errorf("ReadMarkerVersion = %q, want %q", got, want)
	}
}

func TestReadMarkerVersion_Missing(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadMarkerVersion(dir)
	if err == nil {
		t.Error("expected error for missing marker file")
	}
}

func TestWriteMarker(t *testing.T) {
	dir := t.TempDir()

	if err := WriteMarker(dir); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".h2-dir.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := strings.TrimSpace(string(data))
	want := version.DisplayVersion()
	if content != want {
		t.Errorf("marker content = %q, want %q", content, want)
	}
}

func TestLooksLikeH2Dir(t *testing.T) {
	t.Run("with expected subdirs", func(t *testing.T) {
		dir := t.TempDir()
		for _, sub := range []string{"roles", "sessions", "sockets"} {
			os.MkdirAll(filepath.Join(dir, sub), 0o755)
		}
		if !looksLikeH2Dir(dir) {
			t.Error("expected true for dir with roles/sessions/sockets")
		}
	})

	t.Run("missing subdirs", func(t *testing.T) {
		dir := t.TempDir()
		os.MkdirAll(filepath.Join(dir, "roles"), 0o755)
		// missing sessions and sockets
		if looksLikeH2Dir(dir) {
			t.Error("expected false for dir missing subdirs")
		}
	})

	t.Run("empty dir", func(t *testing.T) {
		dir := t.TempDir()
		if looksLikeH2Dir(dir) {
			t.Error("expected false for empty dir")
		}
	})
}

// --- ResolveDir tests ---

// setupH2Dir creates a temporary h2 directory with a marker file.
func setupH2Dir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := WriteMarker(dir); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	return dir
}

func TestResolveDir_H2DIR_Valid(t *testing.T) {
	ResetResolveCache()
	defer ResetResolveCache()

	dir := setupH2Dir(t)
	t.Setenv("H2_DIR", dir)

	got, err := ResolveDir()
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	if got != dir {
		t.Errorf("ResolveDir = %q, want %q", got, dir)
	}
}

func TestResolveDir_H2DIR_Invalid(t *testing.T) {
	ResetResolveCache()
	defer ResetResolveCache()

	dir := t.TempDir() // no marker file
	t.Setenv("H2_DIR", dir)

	_, err := ResolveDir()
	if err == nil {
		t.Fatal("expected error for H2_DIR without marker")
	}
	if !strings.Contains(err.Error(), "not an h2 directory") {
		t.Errorf("error = %q, want it to contain 'not an h2 directory'", err.Error())
	}
}

func TestResolveDir_WalkUp(t *testing.T) {
	ResetResolveCache()
	defer ResetResolveCache()

	// Create h2 dir and a nested child dir.
	h2Dir := setupH2Dir(t)
	// Resolve symlinks (macOS /var -> /private/var).
	h2Dir, _ = filepath.EvalSymlinks(h2Dir)
	nested := filepath.Join(h2Dir, "some", "nested", "dir")
	os.MkdirAll(nested, 0o755)

	// Unset H2_DIR so walk-up is used.
	t.Setenv("H2_DIR", "")

	// Chdir to nested so walk-up finds h2Dir.
	origDir, _ := os.Getwd()
	os.Chdir(nested)
	defer os.Chdir(origDir)

	got, err := ResolveDir()
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	if got != h2Dir {
		t.Errorf("ResolveDir = %q, want %q", got, h2Dir)
	}
}

func TestResolveDir_FallbackHome(t *testing.T) {
	ResetResolveCache()
	defer ResetResolveCache()

	// Create a fake home with a valid .h2 dir.
	fakeHome := t.TempDir()
	h2Home := filepath.Join(fakeHome, ".h2")
	os.MkdirAll(h2Home, 0o755)
	WriteMarker(h2Home)

	t.Setenv("H2_DIR", "")
	t.Setenv("HOME", fakeHome)
	t.Setenv("H2_ROOT_DIR", h2Home)

	// Chdir to a place with no marker in any parent.
	isolated := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(isolated)
	defer os.Chdir(origDir)

	got, err := ResolveDir()
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	if got != h2Home {
		t.Errorf("ResolveDir = %q, want %q", got, h2Home)
	}
}

func TestResolveDir_MigrationAutoCreatesMarker(t *testing.T) {
	ResetResolveCache()
	defer ResetResolveCache()

	// Create a fake home with an existing ~/.h2 dir (no marker, but has subdirs).
	fakeHome := t.TempDir()
	h2Home := filepath.Join(fakeHome, ".h2")
	for _, sub := range []string{"roles", "sessions", "sockets"} {
		os.MkdirAll(filepath.Join(h2Home, sub), 0o755)
	}

	t.Setenv("H2_DIR", "")
	t.Setenv("HOME", fakeHome)
	t.Setenv("H2_ROOT_DIR", h2Home)

	// Chdir to a place with no marker.
	isolated := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(isolated)
	defer os.Chdir(origDir)

	got, err := ResolveDir()
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	if got != h2Home {
		t.Errorf("ResolveDir = %q, want %q", got, h2Home)
	}

	// Verify marker was created.
	if !IsH2Dir(h2Home) {
		t.Error("expected marker to be auto-created during migration")
	}
}
