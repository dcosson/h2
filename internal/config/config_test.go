package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFrom_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	yaml := `users:
  dcosson:
    bridges:
      telegram:
        bot_token: "123456:ABC-DEF"
        chat_id: 789
      macos_notify:
        enabled: true
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	u, ok := cfg.Users["dcosson"]
	if !ok {
		t.Fatal("expected user dcosson")
	}

	if u.Bridges.Telegram == nil {
		t.Fatal("expected telegram config")
	}
	if u.Bridges.Telegram.BotToken != "123456:ABC-DEF" {
		t.Errorf("bot_token = %q, want %q", u.Bridges.Telegram.BotToken, "123456:ABC-DEF")
	}
	if u.Bridges.Telegram.ChatID != 789 {
		t.Errorf("chat_id = %d, want 789", u.Bridges.Telegram.ChatID)
	}

	if u.Bridges.MacOSNotify == nil {
		t.Fatal("expected macos_notify config")
	}
	if !u.Bridges.MacOSNotify.Enabled {
		t.Error("expected macos_notify.enabled = true")
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

	data := `users:
  dcosson:
    bridges:
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

	cmds := cfg.Users["dcosson"].Bridges.Telegram.AllowedCommands
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

			data := `users:
  dcosson:
    bridges:
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

	data := `users:
  dcosson:
    bridges:
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

	cmds := cfg.Users["dcosson"].Bridges.Telegram.AllowedCommands
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

	u := cfg.Users["alice"]
	if u == nil {
		t.Fatal("expected user alice")
	}
	if u.Bridges.Telegram != nil {
		t.Error("expected nil telegram config")
	}
	if u.Bridges.MacOSNotify != nil {
		t.Error("expected nil macos_notify config")
	}
}
