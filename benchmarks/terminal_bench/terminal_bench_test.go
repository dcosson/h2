package terminal_bench

import (
	"os"
	"path/filepath"
	"testing"
)

// --- Dataset tests ---

func TestLoadDataset(t *testing.T) {
	data := `[
		{
			"task_id": "task-01",
			"name": "Create directory structure",
			"description": "Create the directory structure /tmp/a/b/c and a file in it.",
			"category": "file-ops",
			"difficulty": "easy",
			"checks": [
				{"type": "file_exists", "target": "a/b/c/file.txt", "expected": ""}
			],
			"expected_files": ["a/b/c/"]
		},
		{
			"task_id": "task-02",
			"name": "Process management",
			"description": "Find and list all running python processes.",
			"category": "process",
			"difficulty": "medium",
			"checks": [
				{"type": "command_output", "target": "cat output.txt", "expected": "python"}
			]
		}
	]`

	path := filepath.Join(t.TempDir(), "dataset.json")
	os.WriteFile(path, []byte(data), 0o644)

	dataset, err := LoadDataset(path)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}

	if len(dataset.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(dataset.Tasks))
	}

	task := dataset.Tasks[0]
	if task.TaskID != "task-01" {
		t.Errorf("TaskID = %q", task.TaskID)
	}
	if task.Category != "file-ops" {
		t.Errorf("Category = %q", task.Category)
	}
	if len(task.Checks) != 1 {
		t.Errorf("expected 1 check, got %d", len(task.Checks))
	}
	if len(task.ExpectedFiles) != 1 {
		t.Errorf("expected 1 expected file, got %d", len(task.ExpectedFiles))
	}
}

func TestLoadDataset_NotFound(t *testing.T) {
	_, err := LoadDataset("/nonexistent/path.json")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadDataset_InvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(path, []byte("not json"), 0o644)

	_, err := LoadDataset(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestSaveDataset(t *testing.T) {
	tasks := []Task{
		{TaskID: "test-1", Name: "Test Task", Category: "test"},
	}

	path := filepath.Join(t.TempDir(), "data", "out.json")
	if err := SaveDataset(path, tasks); err != nil {
		t.Fatalf("SaveDataset: %v", err)
	}

	loaded, err := LoadDataset(path)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	if len(loaded.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(loaded.Tasks))
	}
	if loaded.Tasks[0].TaskID != "test-1" {
		t.Errorf("TaskID = %q", loaded.Tasks[0].TaskID)
	}
}

// --- Filter tests ---

func TestDataset_Filter_All(t *testing.T) {
	dataset := &Dataset{Tasks: []Task{
		{TaskID: "a"}, {TaskID: "b"}, {TaskID: "c"},
	}}

	filtered := dataset.Filter(nil)
	if len(filtered) != 3 {
		t.Errorf("expected 3 (no filter), got %d", len(filtered))
	}
}

func TestDataset_Filter_Subset(t *testing.T) {
	dataset := &Dataset{Tasks: []Task{
		{TaskID: "a"}, {TaskID: "b"}, {TaskID: "c"},
	}}

	filtered := dataset.Filter([]string{"a", "c"})
	if len(filtered) != 2 {
		t.Fatalf("expected 2, got %d", len(filtered))
	}
}

func TestDataset_Filter_NoMatch(t *testing.T) {
	dataset := &Dataset{Tasks: []Task{{TaskID: "a"}}}

	filtered := dataset.Filter([]string{"nonexistent"})
	if len(filtered) != 0 {
		t.Errorf("expected 0, got %d", len(filtered))
	}
}

func TestDataset_FilterByCategory(t *testing.T) {
	dataset := &Dataset{Tasks: []Task{
		{TaskID: "a", Category: "file-ops"},
		{TaskID: "b", Category: "process"},
		{TaskID: "c", Category: "file-ops"},
	}}

	fileOps := dataset.FilterByCategory("file-ops")
	if len(fileOps) != 2 {
		t.Errorf("expected 2 file-ops, got %d", len(fileOps))
	}

	process := dataset.FilterByCategory("process")
	if len(process) != 1 {
		t.Errorf("expected 1 process, got %d", len(process))
	}
}

// --- ToBenchmarkTasks tests ---

func TestToBenchmarkTasks(t *testing.T) {
	tasks := []Task{
		{
			TaskID:      "task-01",
			Name:        "Create dir",
			Description: "Create /tmp/test",
			Category:    "file-ops",
			Difficulty:  "easy",
		},
	}

	benchTasks := ToBenchmarkTasks(tasks)
	if len(benchTasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(benchTasks))
	}

	bt := benchTasks[0]
	if bt.ID != "task-01" {
		t.Errorf("ID = %q", bt.ID)
	}
	if bt.EvalFunc == nil {
		t.Error("EvalFunc should not be nil")
	}
}

// --- Eval tests ---

func TestEvaluate_AllChecksPassed(t *testing.T) {
	workDir := t.TempDir()

	// Create expected files.
	os.MkdirAll(filepath.Join(workDir, "output"), 0o755)
	os.WriteFile(filepath.Join(workDir, "output", "result.txt"), []byte("success"), 0o644)

	task := Task{
		TaskID:        "test-eval",
		ExpectedFiles: []string{"output/result.txt"},
		Checks: []Check{
			{Type: "file_exists", Target: "output/result.txt"},
			{Type: "file_contains", Target: "output/result.txt", Expected: "success"},
		},
	}

	result, err := Evaluate(workDir, task)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if !result.Resolved {
		t.Error("expected resolved")
	}
	if result.ChecksPassed != 3 { // 1 expected file + 2 checks
		t.Errorf("ChecksPassed = %d, want 3", result.ChecksPassed)
	}
	if result.ChecksTotal != 3 {
		t.Errorf("ChecksTotal = %d, want 3", result.ChecksTotal)
	}
}

func TestEvaluate_SomeFailed(t *testing.T) {
	workDir := t.TempDir()

	// Create a file but with wrong content.
	os.WriteFile(filepath.Join(workDir, "result.txt"), []byte("wrong"), 0o644)

	task := Task{
		TaskID: "test-partial",
		Checks: []Check{
			{Type: "file_exists", Target: "result.txt"},
			{Type: "file_contains", Target: "result.txt", Expected: "correct"},
		},
	}

	result, err := Evaluate(workDir, task)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if result.Resolved {
		t.Error("should not be resolved when checks fail")
	}
	if result.ChecksPassed != 1 {
		t.Errorf("ChecksPassed = %d, want 1", result.ChecksPassed)
	}
}

func TestEvaluate_NoChecks(t *testing.T) {
	task := Task{TaskID: "no-checks"}

	result, err := Evaluate(t.TempDir(), task)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if !result.Resolved {
		t.Error("no checks should mean resolved")
	}
}

func TestCheckFileExists(t *testing.T) {
	workDir := t.TempDir()
	os.WriteFile(filepath.Join(workDir, "exists.txt"), []byte(""), 0o644)

	cr := checkFileExists(workDir, "exists.txt")
	if !cr.Passed {
		t.Error("file exists but check failed")
	}

	cr = checkFileExists(workDir, "missing.txt")
	if cr.Passed {
		t.Error("file doesn't exist but check passed")
	}
}

func TestCheckFileContains(t *testing.T) {
	workDir := t.TempDir()
	os.WriteFile(filepath.Join(workDir, "data.txt"), []byte("hello world"), 0o644)

	cr := checkFileContains(workDir, "data.txt", "hello")
	if !cr.Passed {
		t.Error("file contains 'hello' but check failed")
	}

	cr = checkFileContains(workDir, "data.txt", "missing")
	if cr.Passed {
		t.Error("file doesn't contain 'missing' but check passed")
	}

	cr = checkFileContains(workDir, "nonexistent.txt", "anything")
	if cr.Passed {
		t.Error("file doesn't exist but check passed")
	}
}

func TestCheckExitCode(t *testing.T) {
	workDir := t.TempDir()

	cr := checkExitCode(workDir, "true", "0")
	if !cr.Passed {
		t.Error("'true' should exit 0")
	}

	cr = checkExitCode(workDir, "false", "1")
	if !cr.Passed {
		t.Error("'false' should exit 1")
	}

	cr = checkExitCode(workDir, "false", "0")
	if cr.Passed {
		t.Error("'false' exits 1, not 0")
	}
}

func TestRunCheck_UnknownType(t *testing.T) {
	cr := runCheck(t.TempDir(), Check{Type: "unknown_type", Target: "foo"})
	if cr.Passed {
		t.Error("unknown check type should fail")
	}
	if cr.Details == "" {
		t.Error("should have details about unknown type")
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("truncate short = %q", got)
	}
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("truncate long = %q, want %q", got, "hello...")
	}
}

func TestFormatPrompt(t *testing.T) {
	task := Task{
		Name:        "Create Files",
		Category:    "file-ops",
		Difficulty:  "easy",
		Description: "Create the following directory structure",
	}

	prompt := formatPrompt(task)
	if prompt == "" {
		t.Error("prompt should not be empty")
	}
	for _, want := range []string{"Create Files", "file-ops", "easy"} {
		if !contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
