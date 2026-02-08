package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Role defines a named configuration bundle for an h2 agent.
type Role struct {
	Name         string      `yaml:"name"`
	Description  string      `yaml:"description,omitempty"`
	Model        string      `yaml:"model,omitempty"`
	Instructions string      `yaml:"instructions"`
	Permissions  Permissions `yaml:"permissions,omitempty"`
	Hooks        yaml.Node   `yaml:"hooks,omitempty"`   // passed through as-is to settings.json
	Settings     yaml.Node   `yaml:"settings,omitempty"` // extra settings.json keys
}

// Permissions defines the permission configuration for a role.
type Permissions struct {
	Allow []string         `yaml:"allow,omitempty"`
	Deny  []string         `yaml:"deny,omitempty"`
	Agent *PermissionAgent `yaml:"agent,omitempty"`
}

// PermissionAgent configures the AI permission reviewer.
type PermissionAgent struct {
	Enabled      *bool  `yaml:"enabled,omitempty"` // defaults to true if instructions are set
	Instructions string `yaml:"instructions,omitempty"`
}

// IsEnabled returns whether the permission agent is enabled.
// Defaults to true when instructions are present.
func (pa *PermissionAgent) IsEnabled() bool {
	if pa.Enabled != nil {
		return *pa.Enabled
	}
	return pa.Instructions != ""
}

// RolesDir returns the directory where role files are stored (~/.h2/roles/).
func RolesDir() string {
	return filepath.Join(ConfigDir(), "roles")
}

// LoadRole loads a role by name from ~/.h2/roles/<name>.yaml.
func LoadRole(name string) (*Role, error) {
	path := filepath.Join(RolesDir(), name+".yaml")
	return LoadRoleFrom(path)
}

// LoadRoleFrom loads a role from the given file path.
func LoadRoleFrom(path string) (*Role, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read role file: %w", err)
	}

	var role Role
	if err := yaml.Unmarshal(data, &role); err != nil {
		return nil, fmt.Errorf("parse role YAML: %w", err)
	}

	if err := role.Validate(); err != nil {
		return nil, fmt.Errorf("invalid role %q: %w", path, err)
	}

	return &role, nil
}

// ListRoles returns all available roles from ~/.h2/roles/.
func ListRoles() ([]*Role, error) {
	dir := RolesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read roles dir: %w", err)
	}

	var roles []*Role
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		role, err := LoadRoleFrom(filepath.Join(dir, entry.Name()))
		if err != nil {
			// Skip invalid role files but could log a warning.
			continue
		}
		roles = append(roles, role)
	}
	return roles, nil
}

// Validate checks that a role has the minimum required fields.
func (r *Role) Validate() error {
	if r.Name == "" {
		return fmt.Errorf("name is required")
	}
	if r.Instructions == "" {
		return fmt.Errorf("instructions are required")
	}
	return nil
}
