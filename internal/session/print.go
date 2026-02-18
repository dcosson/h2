package session

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"h2/internal/config"
	"h2/internal/session/agent"
)

// PrintOpts holds options for running in --print (headless) mode.
type PrintOpts struct {
	Command         string   // Executable to run (e.g. "claude").
	Args            []string // Extra args for the command.
	ClaudeConfigDir string   // CLAUDE_CONFIG_DIR override.
	Model           string   // --model flag.
	PermissionMode  string   // --permission-mode flag.
	AllowedTools    []string // --allowedTools.
	DisallowedTools []string // --disallowedTools.
	Instructions    string   // --append-system-prompt.
	SystemPrompt    string   // --system-prompt.
	CWD             string   // Working directory.
	MetricsFile     string   // Path to write metrics JSON after run.

	// Stdin/Stdout/Stderr for the child process. If nil, inherits from parent.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// PrintMetrics is the JSON structure written to --metrics-file.
type PrintMetrics struct {
	TotalTokens  int64              `json:"total_tokens"`
	InputTokens  int64              `json:"input_tokens"`
	OutputTokens int64              `json:"output_tokens"`
	TotalCostUSD float64            `json:"total_cost_usd"`
	ModelCosts   map[string]float64 `json:"model_costs,omitempty"`
}

// RunPrint executes an agent command in headless --print mode.
// No PTY, no daemon, no socket. Same env setup as regular h2 run.
// If MetricsFile is set, starts an OTEL collector to capture metrics.
func RunPrint(opts PrintOpts) error {
	// Set up the agent type for env vars and OTEL.
	agentType := agent.ResolveAgentType(opts.Command)
	a := agent.New(agentType)

	// Start OTEL collector if agent type supports it and we want metrics.
	var otelPort int
	if agentType.Collectors().Otel {
		if err := a.StartOtelCollector(); err != nil {
			return fmt.Errorf("start otel collector: %w", err)
		}
		otelPort = a.OtelPort()
		defer a.StopOtelCollector()
	}

	// Build command args.
	cmdArgs := opts.Args
	if opts.Model != "" {
		cmdArgs = append(cmdArgs, "--model", opts.Model)
	}
	if opts.PermissionMode != "" {
		cmdArgs = append(cmdArgs, "--permission-mode", opts.PermissionMode)
	}
	for _, tool := range opts.AllowedTools {
		cmdArgs = append(cmdArgs, "--allowedTools", tool)
	}
	for _, tool := range opts.DisallowedTools {
		cmdArgs = append(cmdArgs, "--disallowedTools", tool)
	}
	if opts.Instructions != "" {
		cmdArgs = append(cmdArgs, "--append-system-prompt", opts.Instructions)
	}
	if opts.SystemPrompt != "" {
		cmdArgs = append(cmdArgs, "--system-prompt", opts.SystemPrompt)
	}

	cmd := exec.Command(opts.Command, cmdArgs...)

	// Build environment: inherit + filter CLAUDECODE + add h2 vars.
	env := filteredEnv(os.Environ(), "CLAUDECODE")
	if h2Dir, err := config.ResolveDir(); err == nil {
		env = setEnv(env, "H2_DIR", h2Dir)
	}
	if opts.ClaudeConfigDir != "" {
		env = setEnv(env, "CLAUDE_CONFIG_DIR", opts.ClaudeConfigDir)
	}

	// Add OTEL env vars if collector is active.
	childEnv := agentType.ChildEnv(&agent.CollectorPorts{OtelPort: otelPort})
	for k, v := range childEnv {
		env = setEnv(env, k, v)
	}
	cmd.Env = env

	if opts.CWD != "" {
		cmd.Dir = opts.CWD
	}

	// Wire stdio.
	if opts.Stdin != nil {
		cmd.Stdin = opts.Stdin
	} else {
		cmd.Stdin = os.Stdin
	}
	if opts.Stdout != nil {
		cmd.Stdout = opts.Stdout
	} else {
		cmd.Stdout = os.Stdout
	}
	if opts.Stderr != nil {
		cmd.Stderr = opts.Stderr
	} else {
		cmd.Stderr = os.Stderr
	}

	// Run the command.
	runErr := cmd.Run()

	// Write metrics if requested.
	if opts.MetricsFile != "" {
		metrics := a.Metrics()
		pm := PrintMetrics{
			TotalTokens:  metrics.TotalTokens,
			InputTokens:  metrics.InputTokens,
			OutputTokens: metrics.OutputTokens,
			TotalCostUSD: metrics.TotalCostUSD,
			ModelCosts:   metrics.ModelCosts,
		}
		if writeErr := writeMetricsFile(opts.MetricsFile, pm); writeErr != nil {
			// Don't fail the run for metrics write errors.
			fmt.Fprintf(os.Stderr, "warning: write metrics file: %v\n", writeErr)
		}
	}

	return runErr
}

// setEnv sets or replaces an environment variable in the env slice.
func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func writeMetricsFile(path string, metrics PrintMetrics) error {
	data, err := json.MarshalIndent(metrics, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metrics: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}
