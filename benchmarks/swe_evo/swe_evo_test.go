package swe_evo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Task parsing tests ---

func TestTask_RepoURL(t *testing.T) {
	task := Task{Repo: "scikit-learn/scikit-learn"}
	want := "https://github.com/scikit-learn/scikit-learn.git"
	if got := task.RepoURL(); got != want {
		t.Errorf("RepoURL() = %q, want %q", got, want)
	}
}

func TestTask_ParseTestSuiteFiles(t *testing.T) {
	task := Task{
		TestSuite: `["tests/test_model.py", "tests/test_utils.py", "tests/test_pipeline.py"]`,
	}
	files, err := task.ParseTestSuiteFiles()
	if err != nil {
		t.Fatalf("ParseTestSuiteFiles: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}
	if files[0] != "tests/test_model.py" {
		t.Errorf("files[0] = %q", files[0])
	}
}

func TestTask_ParseTestSuiteFiles_Empty(t *testing.T) {
	task := Task{TestSuite: "[]"}
	files, err := task.ParseTestSuiteFiles()
	if err != nil {
		t.Fatalf("ParseTestSuiteFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files for empty list, got %d", len(files))
	}
}

func TestTask_ParseTestSuiteFiles_EmptyString(t *testing.T) {
	task := Task{TestSuite: ""}
	files, err := task.ParseTestSuiteFiles()
	if err != nil {
		t.Fatalf("ParseTestSuiteFiles: %v", err)
	}
	if files != nil {
		t.Errorf("expected nil for empty string, got %v", files)
	}
}

func TestParseTestList_InvalidJSON(t *testing.T) {
	_, err := parseTestList("not valid json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// --- Dataset tests ---

func TestLoadDataset(t *testing.T) {
	data := `[
		{
			"task_id": "sklearn__sklearn-v1.2-v1.3-001",
			"repo": "scikit-learn/scikit-learn",
			"base_version": "v1.2.0",
			"target_version": "v1.3.0",
			"evolution_description": "Add HDBSCAN clustering algorithm",
			"test_suite": "[\"tests/test_hdbscan.py\", \"tests/test_cluster.py\"]",
			"test_cmd": "python -m pytest tests/ --tb=short -q",
			"test_count": 874,
			"files_changed": 21,
			"repo_language": "Python"
		},
		{
			"task_id": "flask__flask-v2.3-v3.0-001",
			"repo": "pallets/flask",
			"base_version": "v2.3.0",
			"target_version": "v3.0.0",
			"evolution_description": "Migrate to async support",
			"test_suite": "[\"tests/test_async.py\"]",
			"test_cmd": "python -m pytest tests/ -q",
			"test_count": 450,
			"files_changed": 15,
			"repo_language": "Python"
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
	if task.TaskID != "sklearn__sklearn-v1.2-v1.3-001" {
		t.Errorf("TaskID = %q", task.TaskID)
	}
	if task.Repo != "scikit-learn/scikit-learn" {
		t.Errorf("Repo = %q", task.Repo)
	}
	if task.BaseVersion != "v1.2.0" {
		t.Errorf("BaseVersion = %q", task.BaseVersion)
	}
	if task.TargetVersion != "v1.3.0" {
		t.Errorf("TargetVersion = %q", task.TargetVersion)
	}
	if task.TestCount != 874 {
		t.Errorf("TestCount = %d", task.TestCount)
	}
	if task.FilesChanged != 21 {
		t.Errorf("FilesChanged = %d", task.FilesChanged)
	}
	if task.RepoLanguage != "Python" {
		t.Errorf("RepoLanguage = %q", task.RepoLanguage)
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
		{TaskID: "test-1", Repo: "foo/bar", BaseVersion: "v1.0"},
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
	if filtered[0].TaskID != "a" || filtered[1].TaskID != "c" {
		t.Errorf("wrong tasks: %v, %v", filtered[0].TaskID, filtered[1].TaskID)
	}
}

func TestDataset_Filter_NoMatch(t *testing.T) {
	dataset := &Dataset{Tasks: []Task{
		{TaskID: "a"}, {TaskID: "b"},
	}}

	filtered := dataset.Filter([]string{"nonexistent"})
	if len(filtered) != 0 {
		t.Errorf("expected 0, got %d", len(filtered))
	}
}

// --- ToBenchmarkTasks tests ---

func TestToBenchmarkTasks(t *testing.T) {
	tasks := []Task{
		{
			TaskID:               "sklearn__sklearn-v1.2-v1.3-001",
			Repo:                 "scikit-learn/scikit-learn",
			BaseVersion:          "v1.2.0",
			TargetVersion:        "v1.3.0",
			EvolutionDescription: "Add HDBSCAN clustering",
			TestCount:            874,
			FilesChanged:         21,
		},
	}

	benchTasks := ToBenchmarkTasks(tasks)
	if len(benchTasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(benchTasks))
	}

	bt := benchTasks[0]
	if bt.ID != "sklearn__sklearn-v1.2-v1.3-001" {
		t.Errorf("ID = %q", bt.ID)
	}
	if bt.RepoURL != "https://github.com/scikit-learn/scikit-learn.git" {
		t.Errorf("RepoURL = %q", bt.RepoURL)
	}
	if bt.BaseCommit != "v1.2.0" {
		t.Errorf("BaseCommit = %q", bt.BaseCommit)
	}
	if !strings.Contains(bt.Issue, "v1.2.0") {
		t.Errorf("Issue should mention base version: %q", bt.Issue)
	}
	if !strings.Contains(bt.Issue, "v1.3.0") {
		t.Errorf("Issue should mention target version: %q", bt.Issue)
	}
	if !strings.Contains(bt.Issue, "HDBSCAN") {
		t.Errorf("Issue should contain evolution description: %q", bt.Issue)
	}
	if bt.EvalFunc == nil {
		t.Error("EvalFunc should not be nil")
	}
}

func TestFormatPrompt(t *testing.T) {
	task := Task{
		Repo:                 "foo/bar",
		BaseVersion:          "v1.0",
		TargetVersion:        "v2.0",
		EvolutionDescription: "Major refactor",
		FilesChanged:         10,
		TestCount:            500,
	}

	prompt := formatPrompt(task)
	if !strings.Contains(prompt, "v1.0") {
		t.Error("prompt should contain base version")
	}
	if !strings.Contains(prompt, "v2.0") {
		t.Error("prompt should contain target version")
	}
	if !strings.Contains(prompt, "foo/bar") {
		t.Error("prompt should contain repo")
	}
	if !strings.Contains(prompt, "Major refactor") {
		t.Error("prompt should contain evolution description")
	}
	if !strings.Contains(prompt, "10 files") {
		t.Error("prompt should mention file count")
	}
	if !strings.Contains(prompt, "500 tests") {
		t.Error("prompt should mention test count")
	}
}

// --- Eval tests ---

func TestParsePytestSummary_AllPassed(t *testing.T) {
	output := "10 passed in 5.23s"
	passed, total := parsePytestSummary(output)
	if passed != 10 {
		t.Errorf("passed = %d, want 10", passed)
	}
	if total != 10 {
		t.Errorf("total = %d, want 10", total)
	}
}

func TestParsePytestSummary_Mixed(t *testing.T) {
	output := "8 passed, 2 failed in 12.34s"
	passed, total := parsePytestSummary(output)
	if passed != 8 {
		t.Errorf("passed = %d, want 8", passed)
	}
	if total != 10 {
		t.Errorf("total = %d, want 10", total)
	}
}

func TestParsePytestSummary_WithErrors(t *testing.T) {
	output := "5 passed, 3 failed, 2 error in 8.00s"
	passed, total := parsePytestSummary(output)
	if passed != 5 {
		t.Errorf("passed = %d, want 5", passed)
	}
	if total != 10 {
		t.Errorf("total = %d, want 10", total)
	}
}

func TestParsePytestSummary_Empty(t *testing.T) {
	passed, total := parsePytestSummary("")
	if passed != 0 {
		t.Errorf("passed = %d, want 0", passed)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
}

func TestParsePytestSummary_NoTests(t *testing.T) {
	output := "no tests ran in 0.01s"
	passed, total := parsePytestSummary(output)
	if passed != 0 {
		t.Errorf("passed = %d, want 0", passed)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
}

func TestParsePytestSummary_FullOutput(t *testing.T) {
	output := `============================= test session starts =============================
platform linux -- Python 3.11.0, pytest-7.4.0
collected 874 items

tests/test_model.py ..............................
tests/test_utils.py .............................F
tests/test_pipeline.py ...........................

======================== 873 passed, 1 failed in 45.67s ========================`
	passed, total := parsePytestSummary(output)
	if passed != 873 {
		t.Errorf("passed = %d, want 873", passed)
	}
	if total != 874 {
		t.Errorf("total = %d, want 874", total)
	}
}

func TestEvaluate_ResolvedWhenAllPass(t *testing.T) {
	// We can't actually run pytest in tests, but we can test the
	// result logic by checking that Evaluate handles the test_cmd error
	// gracefully and parses whatever output it gets.
	task := Task{
		TaskID:  "test-1",
		TestCmd: "echo '10 passed in 1.00s'",
	}

	result, err := Evaluate(t.TempDir(), task)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if result.TestsPassed != 10 {
		t.Errorf("TestsPassed = %d, want 10", result.TestsPassed)
	}
	if result.TestsTotal != 10 {
		t.Errorf("TestsTotal = %d, want 10", result.TestsTotal)
	}
	if !result.Resolved {
		t.Error("should be resolved when all tests pass")
	}
}

func TestEvaluate_NotResolvedWhenSomeFail(t *testing.T) {
	task := Task{
		TaskID:  "test-2",
		TestCmd: "echo '8 passed, 2 failed in 1.00s'",
	}

	result, err := Evaluate(t.TempDir(), task)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if result.TestsPassed != 8 {
		t.Errorf("TestsPassed = %d, want 8", result.TestsPassed)
	}
	if result.TestsTotal != 10 {
		t.Errorf("TestsTotal = %d, want 10", result.TestsTotal)
	}
	if result.Resolved {
		t.Error("should not be resolved when some tests fail")
	}
}

func TestEvaluate_NotResolvedWhenNoTests(t *testing.T) {
	task := Task{
		TaskID:  "test-3",
		TestCmd: "echo 'no tests ran'",
	}

	result, err := Evaluate(t.TempDir(), task)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if result.Resolved {
		t.Error("should not be resolved when no tests ran")
	}
}

// --- Report and utility tests ---

func TestRepeatStr(t *testing.T) {
	if got := repeatStr("-", 5); got != "-----" {
		t.Errorf("repeatStr('-', 5) = %q", got)
	}
	if got := repeatStr("", 3); got != "" {
		t.Errorf("repeatStr('', 3) = %q", got)
	}
}

// --- DatasetDir / DefaultDatasetPath tests ---

func TestDatasetDir(t *testing.T) {
	dir := DatasetDir()
	if !strings.Contains(dir, "swe_evo") {
		t.Errorf("DatasetDir() = %q, should contain swe_evo", dir)
	}
}

func TestDefaultDatasetPath(t *testing.T) {
	path := DefaultDatasetPath()
	if !strings.HasSuffix(path, "swe_evo.json") {
		t.Errorf("DefaultDatasetPath() = %q, should end with swe_evo.json", path)
	}
}
