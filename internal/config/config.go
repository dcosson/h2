package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"h2/internal/version"
)

const markerFile = ".h2-dir.txt"

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
	BotToken string `yaml:"bot_token"`
	ChatID   int64  `yaml:"chat_id"`
}

type MacOSNotifyConfig struct {
	Enabled bool `yaml:"enabled"`
}

// IsH2Dir checks if dir contains a valid .h2-dir.txt marker file.
func IsH2Dir(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, markerFile))
	return err == nil && !info.IsDir()
}

// ReadMarkerVersion reads the version string from .h2-dir.txt.
func ReadMarkerVersion(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, markerFile))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// WriteMarker writes .h2-dir.txt with the current version.
func WriteMarker(dir string) error {
	return os.WriteFile(filepath.Join(dir, markerFile), []byte("v"+version.Version+"\n"), 0o644)
}

// looksLikeH2Dir returns true if dir exists and contains expected h2 subdirectories,
// even without a .h2-dir.txt marker. Used for one-time migration of existing ~/.h2/.
func looksLikeH2Dir(dir string) bool {
	for _, sub := range []string{"roles", "sessions", "sockets"} {
		if _, err := os.Stat(filepath.Join(dir, sub)); err != nil {
			return false
		}
	}
	return true
}

var (
	resolvedDir  string
	resolvedErr  error
	resolveOnce  sync.Once
)

// ResolveDir finds the h2 root directory.
// Order: H2_DIR env var -> walk up CWD -> ~/.h2/ fallback.
// Result is cached for the process lifetime.
func ResolveDir() (string, error) {
	resolveOnce.Do(func() {
		resolvedDir, resolvedErr = resolveDir()
	})
	return resolvedDir, resolvedErr
}

// ResetResolveCache resets the cached ResolveDir result. For testing only.
func ResetResolveCache() {
	resolveOnce = sync.Once{}
	resolvedDir = ""
	resolvedErr = nil
}

func resolveDir() (string, error) {
	// 1. Check H2_DIR env var
	if dir := os.Getenv("H2_DIR"); dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", fmt.Errorf("H2_DIR: %w", err)
		}
		if !IsH2Dir(abs) {
			return "", fmt.Errorf("H2_DIR=%s is not an h2 directory (missing %s)", abs, markerFile)
		}
		return abs, nil
	}

	// 2. Walk up from CWD
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for {
		if IsH2Dir(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		dir = parent
	}

	// 3. Fall back to ~/.h2/
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	global := filepath.Join(home, ".h2")
	if IsH2Dir(global) {
		return global, nil
	}

	// 3a. Migration: auto-create marker for existing ~/.h2/ directories
	if looksLikeH2Dir(global) {
		if err := WriteMarker(global); err != nil {
			return "", fmt.Errorf("migrate %s: %w", global, err)
		}
		return global, nil
	}

	return "", fmt.Errorf("no h2 directory found; run 'h2 init' to create one")
}

// ConfigDir returns the resolved h2 dir or panics.
// Retained for backward compatibility with existing callers.
func ConfigDir() string {
	dir, err := ResolveDir()
	if err != nil {
		// Fall back to ~/.h2/ to avoid breaking existing code paths
		// that call ConfigDir() before an h2 dir is initialized.
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return filepath.Join(".", ".h2")
		}
		return filepath.Join(home, ".h2")
	}
	return dir
}

// Load reads the h2 config from <h2-dir>/config.yaml.
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
	return &cfg, nil
}
