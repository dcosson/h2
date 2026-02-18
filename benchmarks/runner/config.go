package runner

import "time"

// RunMode specifies whether a benchmark runs in single-agent baseline or multi-agent h2 mode.
type RunMode string

const (
	ModeBaseline RunMode = "baseline" // Single Claude Code agent, no h2 orchestration.
	ModeH2       RunMode = "h2"       // Multi-agent pod via h2.
)

// BenchmarkConfig describes a benchmark run.
type BenchmarkConfig struct {
	Name        string        `json:"name"`         // Benchmark name, e.g. "swe_bench_pro".
	Mode        RunMode       `json:"mode"`         // Baseline or H2.
	Preset      string        `json:"preset"`       // Sandbox preset (empty, hooks, haiku, opus).
	PodTemplate string        `json:"pod_template"` // Pod template for H2 mode (empty for baseline).
	TaskFilter  []string      `json:"task_filter"`  // Run only these task IDs (empty = all).
	Timeout     time.Duration `json:"timeout"`      // Per-task timeout.
	Concurrency int           `json:"concurrency"`  // Parallel tasks (each gets its own sandbox).
	MaxTurns    int           `json:"max_turns"`    // Max agent turns per task (default 100).
}

// BenchmarkTask is a single benchmark problem to solve.
type BenchmarkTask struct {
	ID         string `json:"id"`
	RepoURL    string `json:"repo_url"`    // Git repo to clone.
	BaseCommit string `json:"base_commit"` // Checkout this commit.
	Issue      string `json:"issue"`       // Issue description / prompt for the agent.
	TestPatch  string `json:"test_patch"`  // Tests to apply for eval (SWE-bench style).

	// EvalFunc runs custom evaluation on the working directory after the agent finishes.
	// If nil, the runner skips evaluation and marks the task as unevaluated.
	EvalFunc func(workDir string) (bool, error) `json:"-"`
}

// TaskResult is the outcome of one benchmark task.
type TaskResult struct {
	TaskID     string        `json:"task_id"`
	Mode       RunMode       `json:"mode"`
	Resolved   bool          `json:"resolved"`
	Evaluated  bool          `json:"evaluated"` // False if no eval was run.
	Duration   time.Duration `json:"duration"`
	TokensUsed int64         `json:"tokens_used"`
	Cost       float64       `json:"cost"` // USD.
	AgentCount int           `json:"agent_count"`
	PatchPath  string        `json:"patch_path"` // Path to generated patch file.
	Error      string        `json:"error"`      // Non-empty if the task errored.
}
