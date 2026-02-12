package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	BotToken        string   `yaml:"bot_token"`
	ChatID          int64    `yaml:"chat_id"`
	AllowedCommands []string `yaml:"allowed_commands,omitempty"`
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

// ResolveDirAll discovers all h2 directories on the system.
// Returns a deduplicated, sorted list of absolute paths.
// Best-effort: silently skips inaccessible directories.
func ResolveDirAll() []string {
	seen := make(map[string]bool)
	var dirs []string

	add := func(dir string) {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return
		}
		// Resolve symlinks for dedup.
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			abs = real
		}
		if !seen[abs] {
			seen[abs] = true
			dirs = append(dirs, abs)
		}
	}

	// 1. H2_DIR env var.
	if dir := os.Getenv("H2_DIR"); dir != "" {
		if IsH2Dir(dir) {
			add(dir)
		}
	}

	// 2. Walk up from CWD, checking each level and its siblings.
	if cwd, err := os.Getwd(); err == nil {
		dir := cwd
		for {
			// Check the directory itself.
			if IsH2Dir(dir) {
				add(dir)
			}

			// Check siblings (one level only).
			parent := filepath.Dir(dir)
			if entries, err := os.ReadDir(parent); err == nil {
				for _, e := range entries {
					if !e.IsDir() {
						continue
					}
					sibling := filepath.Join(parent, e.Name())
					if IsH2Dir(sibling) {
						add(sibling)
					}
				}
			}

			if parent == dir {
				break // reached filesystem root
			}
			dir = parent
		}
	}

	// 3. ~/.h2/ fallback (including migration check).
	if home, err := os.UserHomeDir(); err == nil {
		global := filepath.Join(home, ".h2")
		if IsH2Dir(global) {
			add(global)
		} else if looksLikeH2Dir(global) {
			add(global)
		}
	}

	sort.Strings(dirs)
	return dirs
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
