package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// QASandboxConfig defines the container sandbox settings.
type QASandboxConfig struct {
	Dockerfile string            `yaml:"dockerfile"`
	Compose    string            `yaml:"compose,omitempty"`
	Service    string            `yaml:"service,omitempty"`
	BuildArgs  map[string]string `yaml:"build_args,omitempty"`
	Setup      []string          `yaml:"setup,omitempty"`
	Env        []string          `yaml:"env,omitempty"`
	Volumes    []string          `yaml:"volumes,omitempty"`
}

// QAOrchestratorConfig defines the QA orchestrator agent settings.
type QAOrchestratorConfig struct {
	Model             string `yaml:"model,omitempty"`
	ExtraInstructions string `yaml:"extra_instructions,omitempty"`
}

// QAConfig is the top-level configuration for h2 qa, parsed from h2-qa.yaml.
type QAConfig struct {
	Sandbox      QASandboxConfig      `yaml:"sandbox"`
	Orchestrator QAOrchestratorConfig `yaml:"orchestrator,omitempty"`
	PlansDir     string               `yaml:"plans_dir,omitempty"`
	ResultsDir   string               `yaml:"results_dir,omitempty"`

	// configDir is the directory containing the config file.
	// All relative paths are resolved against this directory.
	configDir string

	// configPath is the absolute path to the config file itself.
	// Used for deterministic Docker image tagging.
	configPath string
}

// DefaultPlansDir is the default directory for test plans.
const DefaultPlansDir = "qa/plans/"

// DefaultResultsDir is the default directory for test results.
const DefaultResultsDir = "qa/results/"

// DefaultOrchestratorModel is the default model for the QA orchestrator.
const DefaultOrchestratorModel = "opus"

// LoadQAConfig loads and parses a QA config from the given path.
func LoadQAConfig(path string) (*QAConfig, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg QAConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config YAML: %w", err)
	}

	cfg.configDir = filepath.Dir(absPath)
	cfg.configPath = absPath

	// Apply defaults.
	if cfg.PlansDir == "" {
		cfg.PlansDir = DefaultPlansDir
	}
	if cfg.ResultsDir == "" {
		cfg.ResultsDir = DefaultResultsDir
	}
	if cfg.Orchestrator.Model == "" {
		cfg.Orchestrator.Model = DefaultOrchestratorModel
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// Validate checks that the QA config has the minimum required fields.
func (c *QAConfig) Validate() error {
	if c.Sandbox.Dockerfile == "" {
		return fmt.Errorf("sandbox.dockerfile is required in h2-qa.yaml")
	}
	return nil
}

// ResolvePath resolves a path relative to the config file's directory.
// Absolute paths are returned as-is.
func (c *QAConfig) ResolvePath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.configDir, p)
}

// ResolvedPlansDir returns the absolute path to the plans directory.
func (c *QAConfig) ResolvedPlansDir() string {
	return c.ResolvePath(c.PlansDir)
}

// ResolvedResultsDir returns the absolute path to the results directory.
func (c *QAConfig) ResolvedResultsDir() string {
	return c.ResolvePath(c.ResultsDir)
}

// ResolvedDockerfile returns the absolute path to the Dockerfile.
func (c *QAConfig) ResolvedDockerfile() string {
	if c.Sandbox.Dockerfile == "" {
		return ""
	}
	return c.ResolvePath(c.Sandbox.Dockerfile)
}

// DiscoverQAConfig finds the QA config file using the discovery order:
// 1. Explicit configPath (if non-empty)
// 2. ./h2-qa.yaml
// 3. ./qa/h2-qa.yaml
// Returns the loaded config or an error with a helpful message.
func DiscoverQAConfig(configPath string) (*QAConfig, error) {
	if configPath != "" {
		return LoadQAConfig(configPath)
	}

	candidates := []string{
		"h2-qa.yaml",
		filepath.Join("qa", "h2-qa.yaml"),
	}

	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return LoadQAConfig(candidate)
		}
	}

	return nil, fmt.Errorf("no h2-qa.yaml found; looked in ./ and ./qa/. Create one or use --config <path>")
}
