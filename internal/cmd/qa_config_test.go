package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadQAConfig_AllFields(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: qa/Dockerfile
  compose: qa/docker-compose.yaml
  service: qa-agent
  build_args:
    APP_VERSION: "1.0"
    DEBUG: "true"
  setup:
    - "h2 init ~/.h2"
    - "npm install"
  env:
    - ANTHROPIC_API_KEY
    - FOO=bar
  volumes:
    - ./src:/app/src
    - ./config:/app/config
orchestrator:
  model: sonnet
  extra_instructions: |
    You are testing a web app.
plans_dir: tests/plans/
results_dir: tests/results/
`), 0o644)

	cfg, err := LoadQAConfig(configPath)
	if err != nil {
		t.Fatalf("LoadQAConfig: %v", err)
	}

	// Sandbox fields.
	if cfg.Sandbox.Dockerfile != "qa/Dockerfile" {
		t.Errorf("Dockerfile = %q, want %q", cfg.Sandbox.Dockerfile, "qa/Dockerfile")
	}
	if cfg.Sandbox.Compose != "qa/docker-compose.yaml" {
		t.Errorf("Compose = %q, want %q", cfg.Sandbox.Compose, "qa/docker-compose.yaml")
	}
	if cfg.Sandbox.Service != "qa-agent" {
		t.Errorf("Service = %q, want %q", cfg.Sandbox.Service, "qa-agent")
	}
	if len(cfg.Sandbox.BuildArgs) != 2 {
		t.Errorf("BuildArgs len = %d, want 2", len(cfg.Sandbox.BuildArgs))
	}
	if cfg.Sandbox.BuildArgs["APP_VERSION"] != "1.0" {
		t.Errorf("BuildArgs[APP_VERSION] = %q, want %q", cfg.Sandbox.BuildArgs["APP_VERSION"], "1.0")
	}
	if cfg.Sandbox.BuildArgs["DEBUG"] != "true" {
		t.Errorf("BuildArgs[DEBUG] = %q, want %q", cfg.Sandbox.BuildArgs["DEBUG"], "true")
	}
	if len(cfg.Sandbox.Setup) != 2 {
		t.Errorf("Setup len = %d, want 2", len(cfg.Sandbox.Setup))
	}
	if cfg.Sandbox.Setup[0] != "h2 init ~/.h2" {
		t.Errorf("Setup[0] = %q, want %q", cfg.Sandbox.Setup[0], "h2 init ~/.h2")
	}
	if len(cfg.Sandbox.Env) != 2 {
		t.Errorf("Env len = %d, want 2", len(cfg.Sandbox.Env))
	}
	if cfg.Sandbox.Env[0] != "ANTHROPIC_API_KEY" {
		t.Errorf("Env[0] = %q, want %q", cfg.Sandbox.Env[0], "ANTHROPIC_API_KEY")
	}
	if cfg.Sandbox.Env[1] != "FOO=bar" {
		t.Errorf("Env[1] = %q, want %q", cfg.Sandbox.Env[1], "FOO=bar")
	}
	if len(cfg.Sandbox.Volumes) != 2 {
		t.Errorf("Volumes len = %d, want 2", len(cfg.Sandbox.Volumes))
	}

	// Orchestrator fields.
	if cfg.Orchestrator.Model != "sonnet" {
		t.Errorf("Model = %q, want %q", cfg.Orchestrator.Model, "sonnet")
	}
	if cfg.Orchestrator.ExtraInstructions != "You are testing a web app.\n" {
		t.Errorf("ExtraInstructions = %q, want %q", cfg.Orchestrator.ExtraInstructions, "You are testing a web app.\n")
	}

	// Top-level fields.
	if cfg.PlansDir != "tests/plans/" {
		t.Errorf("PlansDir = %q, want %q", cfg.PlansDir, "tests/plans/")
	}
	if cfg.ResultsDir != "tests/results/" {
		t.Errorf("ResultsDir = %q, want %q", cfg.ResultsDir, "tests/results/")
	}
}

func TestLoadQAConfig_Minimal(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: Dockerfile
`), 0o644)

	cfg, err := LoadQAConfig(configPath)
	if err != nil {
		t.Fatalf("LoadQAConfig: %v", err)
	}

	if cfg.Sandbox.Dockerfile != "Dockerfile" {
		t.Errorf("Dockerfile = %q, want %q", cfg.Sandbox.Dockerfile, "Dockerfile")
	}

	// Defaults should be applied.
	if cfg.PlansDir != DefaultPlansDir {
		t.Errorf("PlansDir = %q, want default %q", cfg.PlansDir, DefaultPlansDir)
	}
	if cfg.ResultsDir != DefaultResultsDir {
		t.Errorf("ResultsDir = %q, want default %q", cfg.ResultsDir, DefaultResultsDir)
	}
	if cfg.Orchestrator.Model != DefaultOrchestratorModel {
		t.Errorf("Model = %q, want default %q", cfg.Orchestrator.Model, DefaultOrchestratorModel)
	}
}

func TestDiscoverQAConfig_RootDir(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: Dockerfile
`), 0o644)

	// Change to the temp dir so discovery works.
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cfg, err := DiscoverQAConfig("")
	if err != nil {
		t.Fatalf("DiscoverQAConfig: %v", err)
	}
	if cfg.Sandbox.Dockerfile != "Dockerfile" {
		t.Errorf("Dockerfile = %q, want %q", cfg.Sandbox.Dockerfile, "Dockerfile")
	}
}

func TestDiscoverQAConfig_QASubdir(t *testing.T) {
	dir := t.TempDir()
	qaDir := filepath.Join(dir, "qa")
	os.MkdirAll(qaDir, 0o755)
	configPath := filepath.Join(qaDir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: Dockerfile
`), 0o644)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cfg, err := DiscoverQAConfig("")
	if err != nil {
		t.Fatalf("DiscoverQAConfig: %v", err)
	}
	if cfg.Sandbox.Dockerfile != "Dockerfile" {
		t.Errorf("Dockerfile = %q, want %q", cfg.Sandbox.Dockerfile, "Dockerfile")
	}
}

func TestDiscoverQAConfig_ExplicitPath(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "custom-config.yaml")
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: my/Dockerfile
`), 0o644)

	cfg, err := DiscoverQAConfig(configPath)
	if err != nil {
		t.Fatalf("DiscoverQAConfig: %v", err)
	}
	if cfg.Sandbox.Dockerfile != "my/Dockerfile" {
		t.Errorf("Dockerfile = %q, want %q", cfg.Sandbox.Dockerfile, "my/Dockerfile")
	}
}

func TestDiscoverQAConfig_NotFound(t *testing.T) {
	dir := t.TempDir()

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	_, err := DiscoverQAConfig("")
	if err == nil {
		t.Fatal("expected error when no config found")
	}
	// Should contain helpful message.
	errMsg := err.Error()
	if !strings.Contains(errMsg, "h2-qa.yaml") {
		t.Errorf("error should mention h2-qa.yaml, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "--config") {
		t.Errorf("error should mention --config, got: %s", errMsg)
	}
}

func TestQAConfig_ResolvePath(t *testing.T) {
	cfg := &QAConfig{
		configDir: "/home/user/project",
	}

	// Relative path should resolve against configDir.
	got := cfg.ResolvePath("qa/Dockerfile")
	want := "/home/user/project/qa/Dockerfile"
	if got != want {
		t.Errorf("ResolvePath(relative) = %q, want %q", got, want)
	}

	// Absolute path should be returned as-is.
	got = cfg.ResolvePath("/absolute/path/Dockerfile")
	want = "/absolute/path/Dockerfile"
	if got != want {
		t.Errorf("ResolvePath(absolute) = %q, want %q", got, want)
	}
}

func TestQAConfig_ResolvedPaths(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: qa/Dockerfile
plans_dir: my-plans/
results_dir: my-results/
`), 0o644)

	cfg, err := LoadQAConfig(configPath)
	if err != nil {
		t.Fatalf("LoadQAConfig: %v", err)
	}

	if got := cfg.ResolvedDockerfile(); got != filepath.Join(dir, "qa/Dockerfile") {
		t.Errorf("ResolvedDockerfile() = %q, want %q", got, filepath.Join(dir, "qa/Dockerfile"))
	}
	if got := cfg.ResolvedPlansDir(); got != filepath.Join(dir, "my-plans/") {
		t.Errorf("ResolvedPlansDir() = %q, want %q", got, filepath.Join(dir, "my-plans/"))
	}
	if got := cfg.ResolvedResultsDir(); got != filepath.Join(dir, "my-results/") {
		t.Errorf("ResolvedResultsDir() = %q, want %q", got, filepath.Join(dir, "my-results/"))
	}
}

func TestQAConfig_EnvPassthroughSyntax(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: Dockerfile
  env:
    - ANTHROPIC_API_KEY
    - FOO=bar
    - EMPTY=
    - MULTI_EQUAL=a=b=c
`), 0o644)

	cfg, err := LoadQAConfig(configPath)
	if err != nil {
		t.Fatalf("LoadQAConfig: %v", err)
	}

	if len(cfg.Sandbox.Env) != 4 {
		t.Fatalf("Env len = %d, want 4", len(cfg.Sandbox.Env))
	}

	// KEY only (passthrough from host).
	if cfg.Sandbox.Env[0] != "ANTHROPIC_API_KEY" {
		t.Errorf("Env[0] = %q, want passthrough key", cfg.Sandbox.Env[0])
	}
	// KEY=VALUE.
	if cfg.Sandbox.Env[1] != "FOO=bar" {
		t.Errorf("Env[1] = %q, want KEY=VALUE", cfg.Sandbox.Env[1])
	}
	// KEY= (empty value).
	if cfg.Sandbox.Env[2] != "EMPTY=" {
		t.Errorf("Env[2] = %q, want EMPTY=", cfg.Sandbox.Env[2])
	}
	// KEY=val with multiple = signs.
	if cfg.Sandbox.Env[3] != "MULTI_EQUAL=a=b=c" {
		t.Errorf("Env[3] = %q, want MULTI_EQUAL=a=b=c", cfg.Sandbox.Env[3])
	}
}

func TestLoadQAConfig_DefaultValues(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "h2-qa.yaml")
	// Config with no plans_dir, results_dir, or orchestrator.model.
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: Dockerfile
`), 0o644)

	cfg, err := LoadQAConfig(configPath)
	if err != nil {
		t.Fatalf("LoadQAConfig: %v", err)
	}

	if cfg.PlansDir != "qa/plans/" {
		t.Errorf("PlansDir default = %q, want %q", cfg.PlansDir, "qa/plans/")
	}
	if cfg.ResultsDir != "qa/results/" {
		t.Errorf("ResultsDir default = %q, want %q", cfg.ResultsDir, "qa/results/")
	}
	if cfg.Orchestrator.Model != "opus" {
		t.Errorf("Orchestrator.Model default = %q, want %q", cfg.Orchestrator.Model, "opus")
	}
}

func TestLoadQAConfig_SetsConfigPath(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: Dockerfile
`), 0o644)

	cfg, err := LoadQAConfig(configPath)
	if err != nil {
		t.Fatalf("LoadQAConfig: %v", err)
	}

	absPath, _ := filepath.Abs(configPath)
	if cfg.configPath != absPath {
		t.Errorf("configPath = %q, want %q", cfg.configPath, absPath)
	}
}

func TestQAConfig_Validate_MissingDockerfile(t *testing.T) {
	cfg := &QAConfig{}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing dockerfile")
	}
	if !strings.Contains(err.Error(), "sandbox.dockerfile") {
		t.Errorf("error should mention sandbox.dockerfile, got: %v", err)
	}
}

func TestQAConfig_Validate_WithDockerfile(t *testing.T) {
	cfg := &QAConfig{
		Sandbox: QASandboxConfig{
			Dockerfile: "Dockerfile",
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() should pass with dockerfile set, got: %v", err)
	}
}

func TestLoadQAConfig_ValidationError(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox: {}
`), 0o644)

	_, err := LoadQAConfig(configPath)
	if err == nil {
		t.Fatal("expected validation error for missing dockerfile")
	}
	if !strings.Contains(err.Error(), "sandbox.dockerfile") {
		t.Errorf("error should mention sandbox.dockerfile, got: %v", err)
	}
}

func TestDiscoverQAConfig_PrefersRootOverSubdir(t *testing.T) {
	dir := t.TempDir()

	// Create both root and qa/ configs with different dockerfiles.
	os.WriteFile(filepath.Join(dir, "h2-qa.yaml"), []byte(`
sandbox:
  dockerfile: root-Dockerfile
`), 0o644)

	qaDir := filepath.Join(dir, "qa")
	os.MkdirAll(qaDir, 0o755)
	os.WriteFile(filepath.Join(qaDir, "h2-qa.yaml"), []byte(`
sandbox:
  dockerfile: subdir-Dockerfile
`), 0o644)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cfg, err := DiscoverQAConfig("")
	if err != nil {
		t.Fatalf("DiscoverQAConfig: %v", err)
	}
	// Should find root config first.
	if cfg.Sandbox.Dockerfile != "root-Dockerfile" {
		t.Errorf("Dockerfile = %q, want %q (should prefer root)", cfg.Sandbox.Dockerfile, "root-Dockerfile")
	}
}
