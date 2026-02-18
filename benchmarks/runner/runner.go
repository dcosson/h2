package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"h2/internal/sandbox"
)

// Runner orchestrates benchmark task execution.
type Runner struct {
	Config      BenchmarkConfig
	SandboxName string // Name of the sandbox to use.
	SandboxBase string // Base dir for sandboxes (empty = default ~/.h2/sandboxes/).
	ResultsDir  string // Where to write results (defaults to benchmarks/results/<name>/<run-id>/).

	// RunAgent executes the agent for a single task. Pluggable for testing.
	// In production, this is set to runBaseline or a pod runner.
	RunAgent func(ctx context.Context, sb *sandbox.Sandbox, workDir, issue string) error

	// CloneRepo clones a repo at a specific commit into a temp directory.
	// Pluggable for testing. Defaults to gitCloneRepo.
	CloneRepo func(ctx context.Context, repoURL, baseCommit string) (string, error)

	// GeneratePatch generates a diff from baseCommit in the work directory.
	// Pluggable for testing. Defaults to gitGeneratePatch.
	GeneratePatch func(workDir, baseCommit string) (string, error)
}

// NewRunner creates a Runner with default implementations.
func NewRunner(config BenchmarkConfig, sandboxName string) *Runner {
	r := &Runner{
		Config:      config,
		SandboxName: sandboxName,
	}
	r.CloneRepo = gitCloneRepo
	r.GeneratePatch = gitGeneratePatch
	r.RunAgent = func(ctx context.Context, sb *sandbox.Sandbox, workDir, issue string) error {
		return RunBaseline(ctx, sb, workDir, issue, config.MaxTurns)
	}
	return r
}

// RunID generates a unique run identifier.
func RunID() string {
	return time.Now().UTC().Format("20060102-150405")
}

// RunAll executes all tasks with the configured concurrency, returning results and any overall error.
func (r *Runner) RunAll(ctx context.Context, tasks []BenchmarkTask) ([]TaskResult, error) {
	filtered := r.filterTasks(tasks)
	if len(filtered) == 0 {
		return nil, fmt.Errorf("no tasks to run (filter matched 0 of %d)", len(tasks))
	}

	runID := RunID()
	resultsDir := r.resolveResultsDir(runID)
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating results dir: %w", err)
	}

	// Save run config.
	if err := saveRunConfig(resultsDir, r.Config); err != nil {
		return nil, fmt.Errorf("saving run config: %w", err)
	}

	concurrency := r.Config.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}

	results := make([]TaskResult, len(filtered))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, task := range filtered {
		wg.Add(1)
		go func(idx int, t BenchmarkTask) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			results[idx] = r.RunTask(ctx, t, resultsDir)
		}(i, task)
	}

	wg.Wait()

	// Generate and save summary.
	summary := Summarize(r.Config, results)
	if err := saveSummary(resultsDir, summary); err != nil {
		return results, fmt.Errorf("saving summary: %w", err)
	}

	return results, nil
}

// RunTask executes a single benchmark task: reset sandbox, clone repo, run agent, eval, save.
func (r *Runner) RunTask(ctx context.Context, task BenchmarkTask, resultsDir string) TaskResult {
	start := time.Now()
	result := TaskResult{
		TaskID:     task.ID,
		Mode:       r.Config.Mode,
		AgentCount: 1, // Baseline. Pod runner will override.
	}

	// Apply per-task timeout.
	if r.Config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Config.Timeout)
		defer cancel()
	}

	// 1. Reset sandbox.
	if err := sandbox.Reset(r.SandboxName, r.Config.Preset, r.SandboxBase); err != nil {
		result.Error = fmt.Sprintf("sandbox reset: %v", err)
		result.Duration = time.Since(start)
		return result
	}

	// 2. Get sandbox handle.
	sb, err := sandbox.Get(r.SandboxName, r.SandboxBase)
	if err != nil {
		result.Error = fmt.Sprintf("sandbox get: %v", err)
		result.Duration = time.Since(start)
		return result
	}

	// 3. Clone repo at base commit.
	workDir, err := r.CloneRepo(ctx, task.RepoURL, task.BaseCommit)
	if err != nil {
		result.Error = fmt.Sprintf("clone repo: %v", err)
		result.Duration = time.Since(start)
		return result
	}
	defer os.RemoveAll(workDir)

	// 4. Run agent(s).
	if err := r.RunAgent(ctx, sb, workDir, task.Issue); err != nil {
		result.Error = fmt.Sprintf("run agent: %v", err)
		result.Duration = time.Since(start)
		// Don't return — still try to collect patch and eval.
	}

	// 5. Generate patch.
	patchContent, patchErr := r.GeneratePatch(workDir, task.BaseCommit)
	if patchErr == nil && patchContent != "" {
		patchPath := filepath.Join(resultsDir, "patches", task.ID+".patch")
		if writeErr := writePatch(patchPath, patchContent); writeErr == nil {
			result.PatchPath = patchPath
		}
	}

	// 6. Evaluate.
	if task.EvalFunc != nil {
		resolved, evalErr := task.EvalFunc(workDir)
		result.Resolved = resolved
		result.Evaluated = true
		if evalErr != nil && result.Error == "" {
			result.Error = fmt.Sprintf("eval: %v", evalErr)
		}
	}

	result.Duration = time.Since(start)

	// 7. Save per-task result.
	if resultsDir != "" {
		_ = saveTaskResult(resultsDir, result)
	}

	return result
}

func (r *Runner) filterTasks(tasks []BenchmarkTask) []BenchmarkTask {
	if len(r.Config.TaskFilter) == 0 {
		return tasks
	}
	allowed := make(map[string]bool, len(r.Config.TaskFilter))
	for _, id := range r.Config.TaskFilter {
		allowed[id] = true
	}
	var filtered []BenchmarkTask
	for _, t := range tasks {
		if allowed[t.ID] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

func (r *Runner) resolveResultsDir(runID string) string {
	if r.ResultsDir != "" {
		return r.ResultsDir
	}
	return filepath.Join("benchmarks", "results", r.Config.Name, runID)
}

func writePatch(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// gitCloneRepo clones a git repo at a specific commit into a temp directory.
func gitCloneRepo(ctx context.Context, repoURL, baseCommit string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "bench-*")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}

	// Clone the repo.
	cmd := exec.CommandContext(ctx, "git", "clone", "--quiet", repoURL, tmpDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("git clone: %s: %w", string(out), err)
	}

	// Checkout the base commit.
	if baseCommit != "" {
		cmd = exec.CommandContext(ctx, "git", "-C", tmpDir, "checkout", "--quiet", baseCommit)
		if out, err := cmd.CombinedOutput(); err != nil {
			os.RemoveAll(tmpDir)
			return "", fmt.Errorf("git checkout %s: %s: %w", baseCommit, string(out), err)
		}
	}

	return tmpDir, nil
}

// gitGeneratePatch generates a diff from baseCommit in the given working directory.
func gitGeneratePatch(workDir, baseCommit string) (string, error) {
	if baseCommit == "" {
		return "", nil
	}
	cmd := exec.Command("git", "-C", workDir, "diff", baseCommit)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	return string(out), nil
}
