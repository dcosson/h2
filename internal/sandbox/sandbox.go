// Package sandbox manages isolated h2 environments for benchmarks and testing.
//
// Each sandbox is a full H2_DIR stored at ~/.h2/sandboxes/<name>/ with its own
// roles, sessions, sockets, and settings. The key property is that auth
// credentials (.claude.json) survive resets while everything else is wiped
// and rebuilt from a preset.
package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"h2/internal/config"
)

// Sandbox represents an isolated h2 environment.
type Sandbox struct {
	Name      string    `json:"name"`
	Dir       string    `json:"dir"`
	Preset    string    `json:"preset"`
	CreatedAt time.Time `json:"created_at"`
}

// sandboxMeta is persisted to sandbox.json inside the sandbox directory.
type sandboxMeta struct {
	Name      string    `json:"name"`
	Preset    string    `json:"preset"`
	CreatedAt time.Time `json:"created_at"`
}

// SandboxInfo is returned by List with status information.
type SandboxInfo struct {
	Name      string `json:"name"`
	Preset    string `json:"preset"`
	HasAuth   bool   `json:"has_auth"`
	CreatedAt string `json:"created_at"`
}

// SandboxesDir returns the directory where sandboxes are stored.
// If baseDir is empty, defaults to ~/.h2/sandboxes/.
func SandboxesDir(baseDir string) string {
	if baseDir != "" {
		return filepath.Join(baseDir, "sandboxes")
	}
	return filepath.Join(config.ConfigDir(), "sandboxes")
}

// sandboxDir returns the directory for a named sandbox.
func sandboxDir(baseDir, name string) string {
	return filepath.Join(SandboxesDir(baseDir), name)
}

// subdirs that get wiped on reset (everything except claude-config).
var resettableDirs = []string{
	"roles",
	"sessions",
	"sockets",
	"worktrees",
	"pods/roles",
	"pods/templates",
}

// allDirs is the full directory structure for a sandbox.
var allDirs = []string{
	"roles",
	"sessions",
	"sockets",
	"worktrees",
	"pods/roles",
	"pods/templates",
	"claude-config/default",
	"projects",
}

// Create creates a new sandbox with the given name and preset.
// authFrom specifies where to copy .claude.json from (empty = default claude config).
func Create(name, presetName, authFrom, baseDir string) (*Sandbox, error) {
	if name == "" {
		return nil, fmt.Errorf("sandbox name is required")
	}
	if err := validateName(name); err != nil {
		return nil, err
	}

	preset, err := GetPreset(presetName)
	if err != nil {
		return nil, fmt.Errorf("invalid preset: %w", err)
	}

	dir := sandboxDir(baseDir, name)

	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("sandbox %q already exists at %s", name, dir)
	}

	// Create directory structure.
	for _, sub := range allDirs {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("create directory %s: %w", sub, err)
		}
	}

	// Write h2 dir marker.
	if err := config.WriteMarker(dir); err != nil {
		return nil, fmt.Errorf("write marker: %w", err)
	}

	// Copy auth credentials.
	if err := copyAuth(dir, authFrom); err != nil {
		return nil, fmt.Errorf("copy auth: %w", err)
	}

	// Apply preset (settings.json, roles, CLAUDE.md).
	if err := applyPreset(dir, preset); err != nil {
		// Clean up on failure.
		os.RemoveAll(dir)
		return nil, fmt.Errorf("apply preset: %w", err)
	}

	// Write config.yaml stub.
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("# h2 sandbox configuration\n"), 0o644); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("write config.yaml: %w", err)
	}

	// Write sandbox metadata.
	now := time.Now().UTC()
	meta := sandboxMeta{
		Name:      name,
		Preset:    presetName,
		CreatedAt: now,
	}
	if err := writeMeta(dir, meta); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("write metadata: %w", err)
	}

	return &Sandbox{
		Name:      name,
		Dir:       dir,
		Preset:    presetName,
		CreatedAt: now,
	}, nil
}

// Reset wipes all sandbox state except .claude.json and reapplies the preset.
// If newPreset is non-empty, the sandbox is rebuilt with that preset instead.
func Reset(name, newPreset, baseDir string) error {
	dir := sandboxDir(baseDir, name)

	meta, err := readMeta(dir)
	if err != nil {
		return fmt.Errorf("sandbox %q not found: %w", name, err)
	}

	presetName := meta.Preset
	if newPreset != "" {
		presetName = newPreset
	}

	preset, err := GetPreset(presetName)
	if err != nil {
		return fmt.Errorf("invalid preset: %w", err)
	}

	// Wipe resettable directories.
	for _, sub := range resettableDirs {
		subDir := filepath.Join(dir, sub)
		if err := os.RemoveAll(subDir); err != nil {
			return fmt.Errorf("remove %s: %w", sub, err)
		}
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			return fmt.Errorf("recreate %s: %w", sub, err)
		}
	}

	// Remove settings.json and CLAUDE.md (but NOT .claude.json).
	os.Remove(filepath.Join(dir, "claude-config", "default", "settings.json"))
	os.Remove(filepath.Join(dir, "claude-config", "default", "CLAUDE.md"))

	// Reapply preset.
	if err := applyPreset(dir, preset); err != nil {
		return fmt.Errorf("apply preset: %w", err)
	}

	// Update metadata with new preset.
	meta.Preset = presetName
	if err := writeMeta(dir, *meta); err != nil {
		return fmt.Errorf("update metadata: %w", err)
	}

	return nil
}

// Destroy removes the entire sandbox directory.
func Destroy(name, baseDir string) error {
	dir := sandboxDir(baseDir, name)

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Errorf("sandbox %q does not exist", name)
	}

	return os.RemoveAll(dir)
}

// List returns information about all sandboxes.
func List(baseDir string) ([]SandboxInfo, error) {
	dir := SandboxesDir(baseDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read sandboxes dir: %w", err)
	}

	var infos []SandboxInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		sbDir := filepath.Join(dir, entry.Name())
		meta, err := readMeta(sbDir)
		if err != nil {
			continue // skip non-sandbox directories
		}

		hasAuth := hasAuthCredentials(sbDir)

		infos = append(infos, SandboxInfo{
			Name:      meta.Name,
			Preset:    meta.Preset,
			HasAuth:   hasAuth,
			CreatedAt: meta.CreatedAt.Format("2006-01-02 15:04"),
		})
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Name < infos[j].Name
	})

	return infos, nil
}

// Get loads sandbox info by name.
func Get(name, baseDir string) (*Sandbox, error) {
	dir := sandboxDir(baseDir, name)
	meta, err := readMeta(dir)
	if err != nil {
		return nil, fmt.Errorf("sandbox %q not found: %w", name, err)
	}
	return &Sandbox{
		Name:      meta.Name,
		Dir:       dir,
		Preset:    meta.Preset,
		CreatedAt: meta.CreatedAt,
	}, nil
}

// Exec runs a command with H2_DIR set to the sandbox directory.
// Returns the combined output and any error.
func Exec(name, baseDir string, args []string) ([]byte, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("no command specified")
	}

	dir := sandboxDir(baseDir, name)
	if _, err := readMeta(dir); err != nil {
		return nil, fmt.Errorf("sandbox %q not found: %w", name, err)
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = EnvForDir(dir)
	return cmd.CombinedOutput()
}

// ClaudeConfigDir returns the claude config directory path for a sandbox.
func (s *Sandbox) ClaudeConfigDir() string {
	return filepath.Join(s.Dir, "claude-config", "default")
}

// Env returns an environment with H2_DIR set to the sandbox directory.
func (s *Sandbox) Env() []string {
	return EnvForDir(s.Dir)
}

// EnvForDir returns an environment with H2_DIR set to the given directory.
func EnvForDir(dir string) []string {
	env := os.Environ()
	// Replace or add H2_DIR.
	found := false
	for i, e := range env {
		if strings.HasPrefix(e, "H2_DIR=") {
			env[i] = "H2_DIR=" + dir
			found = true
			break
		}
	}
	if !found {
		env = append(env, "H2_DIR="+dir)
	}
	return env
}

// copyAuth copies .claude.json from authFrom to the sandbox's claude-config/default/.
// If authFrom is empty, copies from the default h2 claude config directory.
func copyAuth(sandboxDir, authFrom string) error {
	if authFrom == "" {
		authFrom = config.DefaultClaudeConfigDir()
	}

	src := filepath.Join(authFrom, ".claude.json")
	dst := filepath.Join(sandboxDir, "claude-config", "default", ".claude.json")

	data, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no auth to copy — that's OK
		}
		return fmt.Errorf("read auth: %w", err)
	}

	return os.WriteFile(dst, data, 0o644)
}

// hasAuthCredentials checks if a sandbox has a .claude.json file.
func hasAuthCredentials(sbDir string) bool {
	path := filepath.Join(sbDir, "claude-config", "default", ".claude.json")
	_, err := os.Stat(path)
	return err == nil
}

// applyPreset writes the preset's settings.json, roles, and optional CLAUDE.md.
func applyPreset(sbDir string, preset *Preset) error {
	claudeConfigDir := filepath.Join(sbDir, "claude-config", "default")

	// Write settings.json.
	settingsJSON, err := json.MarshalIndent(preset.Settings(), "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(claudeConfigDir, "settings.json"), settingsJSON, 0o644); err != nil {
		return fmt.Errorf("write settings.json: %w", err)
	}

	// Write CLAUDE.md if provided.
	if claudeMD := preset.ClaudeMD(); claudeMD != "" {
		if err := os.WriteFile(filepath.Join(claudeConfigDir, "CLAUDE.md"), []byte(claudeMD), 0o644); err != nil {
			return fmt.Errorf("write CLAUDE.md: %w", err)
		}
	}

	// Write role files.
	for name, content := range preset.Roles() {
		rolePath := filepath.Join(sbDir, "roles", name+".yaml")
		if err := os.WriteFile(rolePath, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write role %s: %w", name, err)
		}
	}

	// Write pod templates.
	for name, content := range preset.PodTemplates() {
		templatePath := filepath.Join(sbDir, "pods", "templates", name+".yaml")
		if err := os.WriteFile(templatePath, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write pod template %s: %w", name, err)
		}
	}

	return nil
}

// writeMeta writes sandbox metadata to sandbox.json.
func writeMeta(sbDir string, meta sandboxMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	return os.WriteFile(filepath.Join(sbDir, "sandbox.json"), data, 0o644)
}

// readMeta reads sandbox metadata from sandbox.json.
func readMeta(sbDir string) (*sandboxMeta, error) {
	data, err := os.ReadFile(filepath.Join(sbDir, "sandbox.json"))
	if err != nil {
		return nil, err
	}
	var meta sandboxMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse sandbox metadata: %w", err)
	}
	return &meta, nil
}

// validateName checks that a sandbox name is valid (alphanumeric, hyphens, underscores).
func validateName(name string) error {
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return fmt.Errorf("invalid sandbox name %q: only alphanumeric, hyphens, and underscores are allowed", name)
		}
	}
	return nil
}
