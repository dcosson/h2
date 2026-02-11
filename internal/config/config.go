package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Users map[string]*UserConfig `yaml:"users"`
}

type UserConfig struct {
	Bridges BridgesConfig `yaml:"bridges"`
}

type BridgesConfig struct {
	Telegram    *TelegramConfig    `yaml:"telegram"`
	MacOSNotify *MacOSNotifyConfig `yaml:"macos_notify"`
}

type TelegramConfig struct {
	BotToken        string   `yaml:"bot_token"`
	ChatID          int64    `yaml:"chat_id"`
	AllowedCommands []string `yaml:"allowed_commands,omitempty"`
}

type MacOSNotifyConfig struct {
	Enabled bool `yaml:"enabled"`
}

// ConfigDir returns the h2 configuration directory (~/.h2/).
func ConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".h2")
	}
	return filepath.Join(home, ".h2")
}

// Load reads the h2 config from ~/.h2/config.yaml.
// If the file does not exist, it returns an empty Config with no error.
func Load() (*Config, error) {
	return LoadFrom(filepath.Join(ConfigDir(), "config.yaml"))
}

// LoadFrom reads the h2 config from the given path.
// If the file does not exist, it returns an empty Config with no error.
func LoadFrom(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

var allowedCommandRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func (c *Config) validate() error {
	for username, u := range c.Users {
		if u == nil || u.Bridges.Telegram == nil {
			continue
		}
		if err := validateAllowedCommands(u.Bridges.Telegram.AllowedCommands); err != nil {
			return fmt.Errorf("user %s: bridges.telegram: %w", username, err)
		}
	}
	return nil
}

func validateAllowedCommands(cmds []string) error {
	for _, cmd := range cmds {
		if cmd == "" {
			return fmt.Errorf("allowed_commands: empty string not permitted")
		}
		if !allowedCommandRe.MatchString(cmd) {
			return fmt.Errorf("allowed_commands: invalid command name %q (must match [a-zA-Z0-9_-]+)", cmd)
		}
	}
	return nil
}
