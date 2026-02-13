package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newQARunCmd() *cobra.Command {
	var configPath string
	var all bool
	var noDocker bool

	cmd := &cobra.Command{
		Use:   "run [plan]",
		Short: "Run a QA test plan",
		Long:  "Launches a container, injects the test plan, and runs the QA orchestrator agent.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if all {
				return runQAAll(configPath, noDocker)
			}
			if len(args) < 1 {
				return fmt.Errorf("usage: h2 qa run <plan-name> or h2 qa run --all")
			}
			return runQARun(configPath, args[0], noDocker)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "Path to h2-qa.yaml config file")
	cmd.Flags().BoolVar(&all, "all", false, "Run all test plans sequentially")
	cmd.Flags().BoolVar(&noDocker, "no-docker", false, "Use H2_DIR sandbox instead of Docker")

	return cmd
}

// runQARun executes a single test plan.
func runQARun(configPath string, planName string, noDocker bool) error {
	cfg, err := DiscoverQAConfig(configPath)
	if err != nil {
		return err
	}

	// Resolve test plan.
	planPath := filepath.Join(cfg.ResolvedPlansDir(), planName+".md")
	planContent, err := os.ReadFile(planPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("test plan %q not found at %s", planName, planPath)
		}
		return fmt.Errorf("read test plan: %w", err)
	}

	if noDocker {
		return runQANoDocker(cfg, planName, string(planContent))
	}
	return runQADocker(cfg, planName, string(planContent))
}

// runQADocker runs a test plan in a Docker container.
func runQADocker(cfg *QAConfig, planName string, planContent string) error {
	if err := dockerAvailable(); err != nil {
		return err
	}

	authTag := authedImageTag(cfg.configPath)
	baseTag := projectImageTag(cfg.configPath)

	// Prefer authed image, fall back to base.
	imageTag := authTag
	if !imageExists(authTag) {
		if !imageExists(baseTag) {
			return fmt.Errorf("no QA image found; run 'h2 qa setup' (and optionally 'h2 qa auth') first")
		}
		fmt.Fprintf(os.Stderr, "Warning: using base image (no auth). Run 'h2 qa auth' for Claude Code authentication.\n")
		imageTag = baseTag
	}

	// Create results directory on host.
	resultsDir, err := createResultsDir(cfg, planName)
	if err != nil {
		return err
	}

	// Save plan.md to results dir for reference.
	if err := os.WriteFile(filepath.Join(resultsDir, "plan.md"), []byte(planContent), 0o644); err != nil {
		return fmt.Errorf("write plan to results dir: %w", err)
	}

	// Generate orchestrator instructions (plain text, not YAML).
	instructions := GenerateOrchestratorInstructions(
		cfg.Orchestrator.ExtraInstructions,
		planContent,
	)

	containerName := fmt.Sprintf("h2-qa-run-%s-%d", planName, time.Now().Unix())

	// Build docker run args using helper functions.
	runArgs := []string{"run", "-d", "--name", containerName}
	runArgs = append(runArgs, buildVolumeArgs(cfg, resultsDir)...)
	runArgs = append(runArgs, buildEnvArgs(cfg)...)
	runArgs = append(runArgs, imageTag, "sleep", "infinity")

	fmt.Fprintf(os.Stderr, "Starting QA container %s...\n", containerName)

	// Start container in background.
	_, stderr, err := dockerExec(runArgs...)
	if err != nil {
		return fmt.Errorf("docker run failed: %s", stderr)
	}

	// Ensure cleanup on exit.
	defer func() {
		fmt.Fprintf(os.Stderr, "\nCleaning up container %s...\n", containerName)
		exec.Command("docker", "rm", "-f", containerName).Run()
	}()

	// Run setup commands with streaming output.
	for _, setupCmd := range cfg.Sandbox.Setup {
		fmt.Fprintf(os.Stderr, "Running setup: %s\n", setupCmd)
		if err := dockerExecStreaming("exec", containerName, "sh", "-c", setupCmd); err != nil {
			return fmt.Errorf("setup command failed (%s): %w", setupCmd, err)
		}
	}

	// Create evidence directory inside container.
	dockerExec("exec", containerName, "mkdir", "-p", "/root/results/evidence")

	// Launch the orchestrator agent and attach terminal.
	fmt.Fprintf(os.Stderr, "Launching QA orchestrator (model: %s, plan: %s)...\n\n", cfg.Orchestrator.Model, planName)

	// Use docker exec -it to run claude with --system-prompt (plain text instructions).
	execCmd := exec.Command("docker", "exec", "-it", containerName,
		"claude", "--system-prompt", instructions, "--permission-mode", "bypassPermissions",
		"--model", cfg.Orchestrator.Model,
		"Execute the test plan in your instructions. Write results to ~/results/report.md and ~/results/metadata.json.")
	execCmd.Stdin = os.Stdin
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr

	execErr := execCmd.Run()

	// Update latest symlink.
	updateLatestSymlink(cfg, resultsDir)

	if execErr != nil {
		fmt.Fprintf(os.Stderr, "\nOrchestrator exited with: %v\n", execErr)
	}

	fmt.Fprintf(os.Stderr, "Results saved to: %s\n", resultsDir)
	return nil
}

// runQANoDocker runs a test plan using H2_DIR-based isolation (no Docker).
func runQANoDocker(cfg *QAConfig, planName string, planContent string) error {
	// Create temp dir for isolated H2 environment.
	tmpDir, err := os.MkdirTemp("", "h2-qa-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create results directory on host.
	resultsDir, err := createResultsDir(cfg, planName)
	if err != nil {
		return err
	}

	// Save plan.md to results dir for reference.
	if err := os.WriteFile(filepath.Join(resultsDir, "plan.md"), []byte(planContent), 0o644); err != nil {
		return fmt.Errorf("write plan to results dir: %w", err)
	}

	// Initialize h2 in temp dir via h2 init.
	initCmd := exec.Command("h2", "init", tmpDir)
	initCmd.Env = append(os.Environ(), "H2_DIR="+tmpDir)
	if err := initCmd.Run(); err != nil {
		// Fallback: manually create minimal directory structure.
		os.MkdirAll(filepath.Join(tmpDir, "roles"), 0o755)
	}

	// Generate orchestrator instructions (plain text).
	instructions := GenerateOrchestratorInstructions(
		cfg.Orchestrator.ExtraInstructions,
		planContent,
	)

	fmt.Fprintf(os.Stderr, "Running QA without Docker (H2_DIR=%s)...\n", tmpDir)
	fmt.Fprintf(os.Stderr, "Launching QA orchestrator (model: %s, plan: %s)...\n\n", cfg.Orchestrator.Model, planName)

	// Run claude with --system-prompt (plain text instructions).
	cmd := exec.Command("claude",
		"--system-prompt", instructions,
		"--permission-mode", "bypassPermissions",
		"--model", cfg.Orchestrator.Model,
		"Execute the test plan in your instructions. Write results to "+resultsDir+"/report.md and "+resultsDir+"/metadata.json.")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "H2_DIR="+tmpDir)

	execErr := cmd.Run()

	updateLatestSymlink(cfg, resultsDir)

	if execErr != nil {
		fmt.Fprintf(os.Stderr, "\nOrchestrator exited with: %v\n", execErr)
	}

	fmt.Fprintf(os.Stderr, "Results saved to: %s\n", resultsDir)
	return nil
}

// runQAAll runs all test plans in the plans directory sequentially.
func runQAAll(configPath string, noDocker bool) error {
	cfg, err := DiscoverQAConfig(configPath)
	if err != nil {
		return err
	}

	plans, err := DiscoverPlans(cfg.ResolvedPlansDir())
	if err != nil {
		return err
	}

	if len(plans) == 0 {
		return fmt.Errorf("no test plans found in %s", cfg.ResolvedPlansDir())
	}

	fmt.Fprintf(os.Stderr, "Running %d test plan(s)...\n\n", len(plans))

	var failures []string
	for _, plan := range plans {
		fmt.Fprintf(os.Stderr, "=== Running plan: %s ===\n", plan)
		if err := runQARun(configPath, plan, noDocker); err != nil {
			fmt.Fprintf(os.Stderr, "Plan %q failed: %v\n\n", plan, err)
			failures = append(failures, plan)
		} else {
			fmt.Fprintf(os.Stderr, "Plan %q completed.\n\n", plan)
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("%d plan(s) failed: %s", len(failures), strings.Join(failures, ", "))
	}
	return nil
}

// DiscoverPlans finds all .md files in the plans directory and returns plan names (without .md).
func DiscoverPlans(plansDir string) ([]string, error) {
	entries, err := os.ReadDir(plansDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read plans dir: %w", err)
	}

	var plans []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".md") {
			plans = append(plans, strings.TrimSuffix(name, ".md"))
		}
	}
	return plans, nil
}

// createResultsDir creates a timestamped results directory for a plan run.
func createResultsDir(cfg *QAConfig, planName string) (string, error) {
	timestamp := time.Now().Format("2006-01-02_1504")
	dirName := fmt.Sprintf("%s-%s", timestamp, planName)
	resultsDir := filepath.Join(cfg.ResolvedResultsDir(), dirName)

	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		return "", fmt.Errorf("create results dir: %w", err)
	}

	// Create evidence subdirectory.
	if err := os.MkdirAll(filepath.Join(resultsDir, "evidence"), 0o755); err != nil {
		return "", fmt.Errorf("create evidence dir: %w", err)
	}

	return resultsDir, nil
}

// updateLatestSymlink updates the "latest" symlink in results_dir to point to the given run dir.
func updateLatestSymlink(cfg *QAConfig, runDir string) {
	latestLink := filepath.Join(cfg.ResolvedResultsDir(), "latest")
	os.Remove(latestLink) // remove existing symlink if present
	// Create relative symlink.
	relPath, err := filepath.Rel(cfg.ResolvedResultsDir(), runDir)
	if err != nil {
		relPath = runDir
	}
	os.Symlink(relPath, latestLink)
}

// buildVolumeArgs constructs Docker volume mount arguments from a QAConfig.
func buildVolumeArgs(cfg *QAConfig, resultsDir string) []string {
	var args []string

	// Results volume.
	args = append(args, "-v", resultsDir+":/root/results")

	// Config-specified volumes.
	for _, vol := range cfg.Sandbox.Volumes {
		parts := strings.SplitN(vol, ":", 2)
		if len(parts) == 2 && !filepath.IsAbs(parts[0]) {
			parts[0] = cfg.ResolvePath(parts[0])
			vol = parts[0] + ":" + parts[1]
		}
		args = append(args, "-v", vol)
	}

	return args
}

// buildEnvArgs constructs Docker environment arguments from a QAConfig.
func buildEnvArgs(cfg *QAConfig) []string {
	var args []string
	for _, env := range cfg.Sandbox.Env {
		if strings.Contains(env, "=") {
			args = append(args, "-e", env)
		} else {
			if val, ok := os.LookupEnv(env); ok {
				args = append(args, "-e", env+"="+val)
			}
		}
	}
	return args
}
