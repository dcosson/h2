package bridgeservice

import (
	"testing"

	"h2/internal/bridge"
	"h2/internal/bridge/telegram"
	"h2/internal/config"
)

func TestFromConfig_AllBridges(t *testing.T) {
	cfg := &config.BridgesConfig{
		Telegram: &config.TelegramConfig{
			BotToken: "test-token",
			ChatID:   12345,
		},
		MacOSNotify: &config.MacOSNotifyConfig{
			Enabled: true,
		},
	}

	bridges := FromConfig(cfg)
	if len(bridges) != 2 {
		t.Fatalf("expected 2 bridges, got %d", len(bridges))
	}

	// Check telegram bridge.
	if bridges[0].Name() != "telegram" {
		t.Errorf("expected first bridge to be telegram, got %q", bridges[0].Name())
	}
	if _, ok := bridges[0].(bridge.Sender); !ok {
		t.Error("telegram bridge should implement Sender")
	}
	if _, ok := bridges[0].(bridge.Receiver); !ok {
		t.Error("telegram bridge should implement Receiver")
	}

	// Check macos_notify bridge.
	if bridges[1].Name() != "macos_notify" {
		t.Errorf("expected second bridge to be macos_notify, got %q", bridges[1].Name())
	}
	if _, ok := bridges[1].(bridge.Sender); !ok {
		t.Error("macos_notify bridge should implement Sender")
	}
}

func TestFromConfig_TelegramOnly(t *testing.T) {
	cfg := &config.BridgesConfig{
		Telegram: &config.TelegramConfig{
			BotToken: "tok",
			ChatID:   1,
		},
	}

	bridges := FromConfig(cfg)
	if len(bridges) != 1 {
		t.Fatalf("expected 1 bridge, got %d", len(bridges))
	}
	if bridges[0].Name() != "telegram" {
		t.Errorf("expected telegram, got %q", bridges[0].Name())
	}
}

func TestFromConfig_MacOSNotifyDisabled(t *testing.T) {
	cfg := &config.BridgesConfig{
		MacOSNotify: &config.MacOSNotifyConfig{
			Enabled: false,
		},
	}

	bridges := FromConfig(cfg)
	if len(bridges) != 0 {
		t.Fatalf("expected 0 bridges when disabled, got %d", len(bridges))
	}
}

func TestFromConfig_AllowedCommandsPropagation(t *testing.T) {
	cfg := &config.BridgesConfig{
		Telegram: &config.TelegramConfig{
			BotToken:        "tok",
			ChatID:          1,
			AllowedCommands: []string{"h2", "bd"},
		},
	}

	bridges := FromConfig(cfg)
	if len(bridges) != 1 {
		t.Fatalf("expected 1 bridge, got %d", len(bridges))
	}

	tg, ok := bridges[0].(*telegram.Telegram)
	if !ok {
		t.Fatal("expected *telegram.Telegram")
	}
	if len(tg.AllowedCommands) != 2 || tg.AllowedCommands[0] != "h2" || tg.AllowedCommands[1] != "bd" {
		t.Errorf("AllowedCommands = %v, want [h2 bd]", tg.AllowedCommands)
	}
}

func TestFromConfig_Empty(t *testing.T) {
	cfg := &config.BridgesConfig{}

	bridges := FromConfig(cfg)
	if len(bridges) != 0 {
		t.Fatalf("expected 0 bridges, got %d", len(bridges))
	}
}
