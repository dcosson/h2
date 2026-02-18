package runner

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"h2/internal/sandbox"
)

// --- Config and type tests ---

func TestRunMode_Values(t *testing.T) {
	if ModeBaseline != "baseline" {
		t.Errorf("ModeBaseline = %q, want %q", ModeBaseline, "baseline")
	}
	if ModeH2 != "h2" {
		t.Errorf("ModeH2 = %q, want %q", ModeH2, "h2")
	}
}

func TestBenchmarkConfig_JSON(t *testing.T) {
	config := BenchmarkConfig{
		Name:        "swe_bench_pro",
		Mode:        ModeBaseline,
		Preset:      "opus",
		PodTemplate: "",
		TaskFilter:  []string{"task-1", "task-2"},
		Timeout:     10 * time.Minute,
		Concurrency: 4,
		MaxTurns:    50,
	}

	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded BenchmarkConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Name != config.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, config.Name)
	}
	if decoded.Mode != config.Mode {
		t.Errorf("Mode = %q, want %q", decoded.Mode, config.Mode)
	}
	if decoded.Preset != config.Preset {
		t.Errorf("Preset = %q, want %q", decoded.Preset, config.Preset)
	}
	if decoded.Concurrency != config.Concurrency {
		t.Errorf("Concurrency = %d, want %d", decoded.Concurrency, config.Concurrency)
	}
	if decoded.MaxTurns != config.MaxTurns {
		t.Errorf("MaxTurns = %d, want %d", decoded.MaxTurns, config.MaxTurns)
	}
	if len(decoded.TaskFilter) != 2 {
		t.Errorf("TaskFilter len = %d, want 2", len(decoded.TaskFilter))
	}
}

func TestTaskResult_JSON(t *testing.T) {
	result := TaskResult{
		TaskID:     "django-1234",
		Mode:       ModeBaseline,
		Resolved:   true,
		Evaluated:  true,
		Duration:   5*time.Minute + 30*time.Second,
		TokensUsed: 150000,
		Cost:       0.45,
		AgentCount: 1,
		PatchPath:  "/tmp/patches/django-1234.patch",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded TaskResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.TaskID != result.TaskID {
		t.Errorf("TaskID = %q, want %q", decoded.TaskID, result.TaskID)
	}
	if decoded.Resolved != result.Resolved {
		t.Errorf("Resolved = %v, want %v", decoded.Resolved, result.Resolved)
	}
	if decoded.Cost != result.Cost {
		t.Errorf("Cost = %f, want %f", decoded.Cost, result.Cost)
	}
}

// --- Result storage tests ---

func TestSaveAndLoadRunConfig(t *testing.T) {
	dir := t.TempDir()
	config := BenchmarkConfig{
		Name:        "test_bench",
		Mode:        ModeBaseline,
		Preset:      "empty",
		Concurrency: 2,
		MaxTurns:    75,
	}

	if err := saveRunConfig(dir, config); err != nil {
		t.Fatalf("saveRunConfig: %v", err)
	}

	loaded, err := LoadRunConfig(dir)
	if err != nil {
		t.Fatalf("LoadRunConfig: %v", err)
	}

	if loaded.Name != config.Name {
		t.Errorf("Name = %q, want %q", loaded.Name, config.Name)
	}
	if loaded.Mode != config.Mode {
		t.Errorf("Mode = %q, want %q", loaded.Mode, config.Mode)
	}
	if loaded.Concurrency != config.Concurrency {
		t.Errorf("Concurrency = %d, want %d", loaded.Concurrency, config.Concurrency)
	}
}

func TestSaveAndLoadTaskResult(t *testing.T) {
	dir := t.TempDir()
	result := TaskResult{
		TaskID:     "task-42",
		Mode:       ModeBaseline,
		Resolved:   true,
		Evaluated:  true,
		Duration:   3 * time.Minute,
		TokensUsed: 50000,
		Cost:       0.15,
		AgentCount: 1,
	}

	if err := saveTaskResult(dir, result); err != nil {
		t.Fatalf("saveTaskResult: %v", err)
	}

	loaded, err := LoadTaskResult(dir, "task-42")
	if err != nil {
		t.Fatalf("LoadTaskResult: %v", err)
	}

	if loaded.TaskID != result.TaskID {
		t.Errorf("TaskID = %q, want %q", loaded.TaskID, result.TaskID)
	}
	if loaded.Resolved != result.Resolved {
		t.Errorf("Resolved = %v, want %v", loaded.Resolved, result.Resolved)
	}
	if loaded.Duration != result.Duration {
		t.Errorf("Duration = %v, want %v", loaded.Duration, result.Duration)
	}
}

func TestSaveAndLoadSummary(t *testing.T) {
	dir := t.TempDir()
	summary := RunSummary{
		Benchmark:   "test_bench",
		Mode:        ModeBaseline,
		Preset:      "empty",
		TotalTasks:  10,
		Resolved:    7,
		Evaluated:   10,
		Errored:     1,
		ResolveRate: 0.7,
		AvgDuration: 5 * time.Minute,
		TotalCost:   4.50,
		TotalTokens: 500000,
	}

	if err := saveSummary(dir, summary); err != nil {
		t.Fatalf("saveSummary: %v", err)
	}

	loaded, err := LoadSummary(dir)
	if err != nil {
		t.Fatalf("LoadSummary: %v", err)
	}

	if loaded.Benchmark != summary.Benchmark {
		t.Errorf("Benchmark = %q, want %q", loaded.Benchmark, summary.Benchmark)
	}
	if loaded.Resolved != summary.Resolved {
		t.Errorf("Resolved = %d, want %d", loaded.Resolved, summary.Resolved)
	}
	if loaded.ResolveRate != summary.ResolveRate {
		t.Errorf("ResolveRate = %f, want %f", loaded.ResolveRate, summary.ResolveRate)
	}
	if loaded.TotalCost != summary.TotalCost {
		t.Errorf("TotalCost = %f, want %f", loaded.TotalCost, summary.TotalCost)
	}
}

func TestLoadTaskResult_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadTaskResult(dir, "nonexistent")
	if err == nil {
		t.Error("expected error loading nonexistent task result")
	}
}

// --- Summarize tests ---

func TestSummarize_Basic(t *testing.T) {
	config := BenchmarkConfig{
		Name:   "test_bench",
		Mode:   ModeBaseline,
		Preset: "empty",
	}

	results := []TaskResult{
		{TaskID: "t1", Resolved: true, Evaluated: true, Duration: 2 * time.Minute, Cost: 0.10, TokensUsed: 10000},
		{TaskID: "t2", Resolved: false, Evaluated: true, Duration: 4 * time.Minute, Cost: 0.20, TokensUsed: 20000},
		{TaskID: "t3", Resolved: true, Evaluated: true, Duration: 6 * time.Minute, Cost: 0.30, TokensUsed: 30000},
	}

	s := Summarize(config, results)

	if s.TotalTasks != 3 {
		t.Errorf("TotalTasks = %d, want 3", s.TotalTasks)
	}
	if s.Resolved != 2 {
		t.Errorf("Resolved = %d, want 2", s.Resolved)
	}
	if s.Evaluated != 3 {
		t.Errorf("Evaluated = %d, want 3", s.Evaluated)
	}
	if s.Errored != 0 {
		t.Errorf("Errored = %d, want 0", s.Errored)
	}

	expectedRate := 2.0 / 3.0
	if s.ResolveRate < expectedRate-0.001 || s.ResolveRate > expectedRate+0.001 {
		t.Errorf("ResolveRate = %f, want ~%f", s.ResolveRate, expectedRate)
	}

	expectedAvg := 4 * time.Minute
	if s.AvgDuration != expectedAvg {
		t.Errorf("AvgDuration = %v, want %v", s.AvgDuration, expectedAvg)
	}

	if s.TotalCost < 0.59 || s.TotalCost > 0.61 {
		t.Errorf("TotalCost = %f, want ~0.60", s.TotalCost)
	}
	if s.TotalTokens != 60000 {
		t.Errorf("TotalTokens = %d, want 60000", s.TotalTokens)
	}
}

func TestSummarize_WithErrors(t *testing.T) {
	config := BenchmarkConfig{Name: "test", Mode: ModeBaseline, Preset: "empty"}

	results := []TaskResult{
		{TaskID: "t1", Resolved: true, Evaluated: true, Duration: time.Minute},
		{TaskID: "t2", Error: "sandbox reset failed", Duration: time.Second},
	}

	s := Summarize(config, results)

	if s.Errored != 1 {
		t.Errorf("Errored = %d, want 1", s.Errored)
	}
	if s.Evaluated != 1 {
		t.Errorf("Evaluated = %d, want 1", s.Evaluated)
	}
	if s.Resolved != 1 {
		t.Errorf("Resolved = %d, want 1", s.Resolved)
	}
	if s.ResolveRate != 1.0 {
		t.Errorf("ResolveRate = %f, want 1.0", s.ResolveRate)
	}
}

func TestSummarize_Empty(t *testing.T) {
	config := BenchmarkConfig{Name: "test", Mode: ModeBaseline, Preset: "empty"}
	s := Summarize(config, nil)

	if s.TotalTasks != 0 {
		t.Errorf("TotalTasks = %d, want 0", s.TotalTasks)
	}
	if s.ResolveRate != 0 {
		t.Errorf("ResolveRate = %f, want 0", s.ResolveRate)
	}
}

func TestSummarize_NoEvaluated(t *testing.T) {
	config := BenchmarkConfig{Name: "test", Mode: ModeBaseline, Preset: "empty"}
	results := []TaskResult{
		{TaskID: "t1", Evaluated: false, Duration: time.Minute},
	}

	s := Summarize(config, results)

	if s.Evaluated != 0 {
		t.Errorf("Evaluated = %d, want 0", s.Evaluated)
	}
	if s.ResolveRate != 0 {
		t.Errorf("ResolveRate = %f, want 0 (no evaluated tasks)", s.ResolveRate)
	}
}

func TestFormatSummary(t *testing.T) {
	s := RunSummary{
		Benchmark:   "swe_bench_pro",
		Mode:        ModeBaseline,
		Preset:      "opus",
		TotalTasks:  100,
		Resolved:    23,
		Evaluated:   100,
		Errored:     2,
		ResolveRate: 0.23,
		AvgDuration: 8*time.Minute + 30*time.Second,
		TotalCost:   45.00,
		TotalTokens: 15000000,
	}

	output := FormatSummary(s)

	// Check key parts are present.
	for _, want := range []string{
		"swe_bench_pro",
		"baseline",
		"opus",
		"100 total",
		"23 resolved",
		"2 errored",
		"23.0%",
		"$45.00",
	} {
		if !contains(output, want) {
			t.Errorf("FormatSummary missing %q in output:\n%s", want, output)
		}
	}
}

// --- Task filtering tests ---

func TestFilterTasks_NoFilter(t *testing.T) {
	r := &Runner{Config: BenchmarkConfig{}}
	tasks := []BenchmarkTask{
		{ID: "t1"}, {ID: "t2"}, {ID: "t3"},
	}

	filtered := r.filterTasks(tasks)
	if len(filtered) != 3 {
		t.Errorf("expected 3 tasks, got %d", len(filtered))
	}
}

func TestFilterTasks_WithFilter(t *testing.T) {
	r := &Runner{Config: BenchmarkConfig{
		TaskFilter: []string{"t1", "t3"},
	}}
	tasks := []BenchmarkTask{
		{ID: "t1"}, {ID: "t2"}, {ID: "t3"},
	}

	filtered := r.filterTasks(tasks)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(filtered))
	}
	if filtered[0].ID != "t1" || filtered[1].ID != "t3" {
		t.Errorf("filtered = [%s, %s], want [t1, t3]", filtered[0].ID, filtered[1].ID)
	}
}

func TestFilterTasks_NoMatch(t *testing.T) {
	r := &Runner{Config: BenchmarkConfig{
		TaskFilter: []string{"nonexistent"},
	}}
	tasks := []BenchmarkTask{{ID: "t1"}, {ID: "t2"}}

	filtered := r.filterTasks(tasks)
	if len(filtered) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(filtered))
	}
}

// --- Runner integration tests (with mocked sandbox/agent) ---

func TestRunTask_FullFlow(t *testing.T) {
	baseDir := t.TempDir()
	resultsDir := t.TempDir()

	// Create a real sandbox for the test.
	_, err := sandbox.Create("test-run", "empty", "", baseDir)
	if err != nil {
		t.Fatalf("sandbox create: %v", err)
	}

	// Create a fake work dir with a git repo.
	workDir := t.TempDir()
	initGitRepo(t, workDir)

	r := &Runner{
		Config: BenchmarkConfig{
			Name:   "test_bench",
			Mode:   ModeBaseline,
			Preset: "empty",
		},
		SandboxName: "test-run",
		SandboxBase: baseDir,
		ResultsDir:  resultsDir,
		// Mock: clone just returns the pre-made workdir.
		CloneRepo: func(ctx context.Context, repoURL, baseCommit string) (string, error) {
			return workDir, nil
		},
		// Mock: agent is a no-op.
		RunAgent: func(ctx context.Context, sb *sandbox.Sandbox, dir, issue string) error {
			// Simulate writing a file (agent did some work).
			return os.WriteFile(filepath.Join(dir, "solution.py"), []byte("print('hello')"), 0o644)
		},
		// Mock: patch generation.
		GeneratePatch: func(dir, baseCommit string) (string, error) {
			return "diff --git a/solution.py b/solution.py\n+print('hello')\n", nil
		},
	}

	task := BenchmarkTask{
		ID:         "task-1",
		RepoURL:    "https://example.com/repo.git",
		BaseCommit: "abc123",
		Issue:      "Fix the bug",
		EvalFunc: func(dir string) (bool, error) {
			// Check if solution.py exists.
			_, err := os.Stat(filepath.Join(dir, "solution.py"))
			return err == nil, nil
		},
	}

	result := r.RunTask(context.Background(), task, resultsDir)

	if result.TaskID != "task-1" {
		t.Errorf("TaskID = %q, want %q", result.TaskID, "task-1")
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
	if !result.Resolved {
		t.Error("expected task to be resolved")
	}
	if !result.Evaluated {
		t.Error("expected task to be evaluated")
	}
	if result.Duration == 0 {
		t.Error("expected non-zero duration")
	}

	// Verify patch was saved.
	if result.PatchPath == "" {
		t.Error("expected PatchPath to be set")
	} else {
		patchData, err := os.ReadFile(result.PatchPath)
		if err != nil {
			t.Errorf("reading patch: %v", err)
		} else if !contains(string(patchData), "solution.py") {
			t.Errorf("patch doesn't mention solution.py: %s", string(patchData))
		}
	}

	// Verify per-task result was saved.
	loaded, err := LoadTaskResult(resultsDir, "task-1")
	if err != nil {
		t.Fatalf("LoadTaskResult: %v", err)
	}
	if loaded.TaskID != "task-1" {
		t.Errorf("loaded TaskID = %q, want %q", loaded.TaskID, "task-1")
	}
	if !loaded.Resolved {
		t.Error("loaded task should be resolved")
	}
}

func TestRunTask_SandboxResetFails(t *testing.T) {
	resultsDir := t.TempDir()

	r := &Runner{
		Config: BenchmarkConfig{
			Name:   "test",
			Mode:   ModeBaseline,
			Preset: "empty",
		},
		SandboxName: "nonexistent-sandbox",
		SandboxBase: t.TempDir(), // No sandbox exists here.
	}

	task := BenchmarkTask{ID: "task-1", Issue: "Fix bug"}
	result := r.RunTask(context.Background(), task, resultsDir)

	if result.Error == "" {
		t.Error("expected error when sandbox doesn't exist")
	}
	if !contains(result.Error, "sandbox reset") {
		t.Errorf("error should mention sandbox reset, got: %s", result.Error)
	}
}

func TestRunTask_NoEvalFunc(t *testing.T) {
	baseDir := t.TempDir()
	resultsDir := t.TempDir()

	_, err := sandbox.Create("test-noeval", "empty", "", baseDir)
	if err != nil {
		t.Fatalf("sandbox create: %v", err)
	}

	workDir := t.TempDir()

	r := &Runner{
		Config: BenchmarkConfig{
			Name:   "test",
			Mode:   ModeBaseline,
			Preset: "empty",
		},
		SandboxName: "test-noeval",
		SandboxBase: baseDir,
		CloneRepo: func(ctx context.Context, repoURL, baseCommit string) (string, error) {
			return workDir, nil
		},
		RunAgent: func(ctx context.Context, sb *sandbox.Sandbox, dir, issue string) error {
			return nil
		},
		GeneratePatch: func(dir, baseCommit string) (string, error) {
			return "", nil
		},
	}

	task := BenchmarkTask{
		ID:    "task-noeval",
		Issue: "Do something",
		// No EvalFunc.
	}

	result := r.RunTask(context.Background(), task, resultsDir)

	if result.Evaluated {
		t.Error("expected Evaluated=false when no EvalFunc")
	}
	if result.Resolved {
		t.Error("expected Resolved=false when not evaluated")
	}
}

func TestRunTask_Timeout(t *testing.T) {
	baseDir := t.TempDir()
	resultsDir := t.TempDir()

	_, err := sandbox.Create("test-timeout", "empty", "", baseDir)
	if err != nil {
		t.Fatalf("sandbox create: %v", err)
	}

	workDir := t.TempDir()

	r := &Runner{
		Config: BenchmarkConfig{
			Name:    "test",
			Mode:    ModeBaseline,
			Preset:  "empty",
			Timeout: 50 * time.Millisecond,
		},
		SandboxName: "test-timeout",
		SandboxBase: baseDir,
		CloneRepo: func(ctx context.Context, repoURL, baseCommit string) (string, error) {
			return workDir, nil
		},
		RunAgent: func(ctx context.Context, sb *sandbox.Sandbox, dir, issue string) error {
			// Block until context is cancelled.
			<-ctx.Done()
			return ctx.Err()
		},
		GeneratePatch: func(dir, baseCommit string) (string, error) {
			return "", nil
		},
	}

	task := BenchmarkTask{ID: "task-timeout", Issue: "Slow task"}
	result := r.RunTask(context.Background(), task, resultsDir)

	if result.Error == "" {
		t.Error("expected error from timeout")
	}
}

func TestRunAll_FilterAndConcurrency(t *testing.T) {
	baseDir := t.TempDir()

	_, err := sandbox.Create("test-all", "empty", "", baseDir)
	if err != nil {
		t.Fatalf("sandbox create: %v", err)
	}

	r := &Runner{
		Config: BenchmarkConfig{
			Name:        "test",
			Mode:        ModeBaseline,
			Preset:      "empty",
			TaskFilter:  []string{"t1", "t3"},
			Concurrency: 2,
		},
		SandboxName: "test-all",
		SandboxBase: baseDir,
		ResultsDir:  t.TempDir(),
		CloneRepo: func(ctx context.Context, repoURL, baseCommit string) (string, error) {
			return t.TempDir(), nil
		},
		RunAgent: func(ctx context.Context, sb *sandbox.Sandbox, dir, issue string) error {
			return nil
		},
		GeneratePatch: func(dir, baseCommit string) (string, error) {
			return "", nil
		},
	}

	tasks := []BenchmarkTask{
		{ID: "t1", Issue: "Task 1", EvalFunc: func(string) (bool, error) { return true, nil }},
		{ID: "t2", Issue: "Task 2", EvalFunc: func(string) (bool, error) { return true, nil }},
		{ID: "t3", Issue: "Task 3", EvalFunc: func(string) (bool, error) { return false, nil }},
	}

	results, err := r.RunAll(context.Background(), tasks)
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results (filtered), got %d", len(results))
	}

	// Verify summary was saved.
	summary, err := LoadSummary(r.ResultsDir)
	if err != nil {
		t.Fatalf("LoadSummary: %v", err)
	}
	if summary.TotalTasks != 2 {
		t.Errorf("summary.TotalTasks = %d, want 2", summary.TotalTasks)
	}
}

func TestRunAll_EmptyFilter(t *testing.T) {
	r := &Runner{
		Config: BenchmarkConfig{
			TaskFilter: []string{"nonexistent"},
		},
	}

	_, err := r.RunAll(context.Background(), []BenchmarkTask{{ID: "t1"}})
	if err == nil {
		t.Error("expected error when filter matches no tasks")
	}
}

// --- Patch writing tests ---

func TestWritePatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patches", "test.patch")
	content := "diff --git a/foo.py b/foo.py\n+bar\n"

	if err := writePatch(path, content); err != nil {
		t.Fatalf("writePatch: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patch: %v", err)
	}
	if string(data) != content {
		t.Errorf("patch content = %q, want %q", string(data), content)
	}
}

// --- Baseline tests ---

func TestTruncateOutput(t *testing.T) {
	short := "hello"
	if got := truncateOutput([]byte(short), 100); got != short {
		t.Errorf("truncateOutput short = %q, want %q", got, short)
	}

	long := make([]byte, 500)
	for i := range long {
		long[i] = 'x'
	}
	got := truncateOutput(long, 100)
	if len(got) > 130 { // 100 chars + "... (truncated)" suffix
		t.Errorf("truncateOutput long result too long: %d chars", len(got))
	}
	if !contains(got, "truncated") {
		t.Error("truncateOutput long result should contain 'truncated'")
	}
}

// --- Helpers ---

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"git", "-C", dir, "init"},
		{"git", "-C", dir, "config", "user.email", "test@test.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s: %v", args, string(out), err)
		}
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
