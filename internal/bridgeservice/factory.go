package bridgeservice

import (
	"h2/internal/bridge"
	"h2/internal/bridge/macos_notify"
	"h2/internal/bridge/telegram"
	"h2/internal/config"
)

// FromConfig instantiates bridge instances from a user's bridge configuration.
// attachmentDir is the private local directory for incoming images; an empty
// path disables image downloads. It is created lazily on the first image.
func FromConfig(cfg *config.BridgesConfig, attachmentDir string) []bridge.Bridge {
	var bridges []bridge.Bridge
	if cfg.Telegram != nil {
		bridges = append(bridges, &telegram.Telegram{
			Token:           cfg.Telegram.BotToken,
			AttachmentDir:   attachmentDir,
			ChatID:          cfg.Telegram.ChatID,
			AllowedCommands: cfg.Telegram.AllowedCommands,
		})
	}
	if cfg.MacOSNotify != nil && cfg.MacOSNotify.Enabled {
		bridges = append(bridges, &macos_notify.MacOSNotify{})
	}
	return bridges
}
