package swe_bench_pro

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Instance parsing tests ---

func TestInstance_RepoURL(t *testing.T) {
	inst := Instance{Repo: "django/django"}
	want := "https://github.com/django/django.git"
	if got := inst.RepoURL(); got != want {
		t.Errorf("RepoURL() = %q, want %q", got, want)
	}
}

func TestInstance_ParseFailToPass(t *testing.T) {
	inst := Instance{
		FailToPass: `["tests/test_foo.py::TestFoo::test_bar", "tests/test_foo.py::TestFoo::test_baz"]`,
	}

	tests, err := inst.ParseFailToPass()
	if err != nil {
		t.Fatalf("ParseFailToPass: %v", err)
	}
	if len(tests) != 2 {
		t.Fatalf("expected 2 tests, got %d", len(tests))
	}
	if tests[0] != "tests/test_foo.py::TestFoo::test_bar" {
		t.Errorf("tests[0] = %q", tests[0])
	}
}

func TestInstance_ParsePassToPass(t *testing.T) {
	inst := Instance{
		PassToPass: `["tests/test_other.py::test_a"]`,
	}

	tests, err := inst.ParsePassToPass()
	if err != nil {
		t.Fatalf("ParsePassToPass: %v", err)
	}
	if len(tests) != 1 {
		t.Fatalf("expected 1 test, got %d", len(tests))
	}
}

func TestInstance_ParseEmptyTestList(t *testing.T) {
	inst := Instance{FailToPass: "[]"}
	tests, err := inst.ParseFailToPass()
	if err != nil {
		t.Fatalf("ParseFailToPass: %v", err)
	}
	if len(tests) != 0 {
		t.Errorf("expected 0 tests for empty list, got %d", len(tests))
	}
}

func TestInstance_ParseEmptyString(t *testing.T) {
	inst := Instance{FailToPass: ""}
	tests, err := inst.ParseFailToPass()
	if err != nil {
		t.Fatalf("ParseFailToPass: %v", err)
	}
	if tests != nil {
		t.Errorf("expected nil for empty string, got %v", tests)
	}
}

func TestInstance_ParsePythonSingleQuotes(t *testing.T) {
	inst := Instance{
		FailToPass: `['tests/unit/utils/test_qtlog.py::TestHideQtWarning::test_unfiltered', 'tests/unit/utils/test_qtlog.py::TestHideQtWarning::test_filtered']`,
	}
	tests, err := inst.ParseFailToPass()
	if err != nil {
		t.Fatalf("ParseFailToPass with single quotes: %v", err)
	}
	if len(tests) != 2 {
		t.Fatalf("expected 2 tests, got %d", len(tests))
	}
	if tests[0] != "tests/unit/utils/test_qtlog.py::TestHideQtWarning::test_unfiltered" {
		t.Errorf("tests[0] = %q", tests[0])
	}
}

func TestInstance_ParseSelectedTestFiles(t *testing.T) {
	inst := Instance{
		SelectedTestFilesToRun: `["tests/test_foo.py", "tests/test_bar.py"]`,
	}
	files, err := inst.ParseSelectedTestFiles()
	if err != nil {
		t.Fatalf("ParseSelectedTestFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
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
			"instance_id": "django__django-16408",
			"repo": "django/django",
			"base_commit": "abc123",
			"problem_statement": "Fix the migration bug",
			"patch": "diff --git a/foo.py",
			"test_patch": "diff --git a/test_foo.py",
			"fail_to_pass": "[\"tests/test_foo.py::test_bar\"]",
			"pass_to_pass": "[\"tests/test_other.py::test_a\"]",
			"selected_test_files_to_run": "[\"tests/test_foo.py\"]",
			"repo_language": "Python"
		},
		{
			"instance_id": "sympy__sympy-24152",
			"repo": "sympy/sympy",
			"base_commit": "def456",
			"problem_statement": "Fix integral evaluation",
			"patch": "diff --git a/bar.py",
			"test_patch": "diff --git a/test_bar.py",
			"fail_to_pass": "[\"tests/test_integrals.py::test_issue\"]",
			"pass_to_pass": "[]",
			"selected_test_files_to_run": "[\"tests/test_integrals.py\"]",
			"repo_language": "Python"
		}
	]`

	path := filepath.Join(t.TempDir(), "dataset.json")
	os.WriteFile(path, []byte(data), 0o644)

	dataset, err := LoadDataset(path)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}

	if len(dataset.Instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(dataset.Instances))
	}

	inst := dataset.Instances[0]
	if inst.InstanceID != "django__django-16408" {
		t.Errorf("InstanceID = %q", inst.InstanceID)
	}
	if inst.Repo != "django/django" {
		t.Errorf("Repo = %q", inst.Repo)
	}
	if inst.BaseCommit != "abc123" {
		t.Errorf("BaseCommit = %q", inst.BaseCommit)
	}
	if inst.RepoLanguage != "Python" {
		t.Errorf("RepoLanguage = %q", inst.RepoLanguage)
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
	instances := []Instance{
		{InstanceID: "test-1", Repo: "foo/bar", BaseCommit: "abc"},
	}

	path := filepath.Join(t.TempDir(), "data", "out.json")
	if err := SaveDataset(path, instances); err != nil {
		t.Fatalf("SaveDataset: %v", err)
	}

	// Verify round-trip.
	loaded, err := LoadDataset(path)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	if len(loaded.Instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(loaded.Instances))
	}
	if loaded.Instances[0].InstanceID != "test-1" {
		t.Errorf("InstanceID = %q", loaded.Instances[0].InstanceID)
	}
}

func TestDataset_Filter_All(t *testing.T) {
	dataset := &Dataset{Instances: []Instance{
		{InstanceID: "a"}, {InstanceID: "b"}, {InstanceID: "c"},
	}}

	filtered := dataset.Filter(nil)
	if len(filtered) != 3 {
		t.Errorf("expected 3 (no filter), got %d", len(filtered))
	}
}

func TestDataset_Filter_Subset(t *testing.T) {
	dataset := &Dataset{Instances: []Instance{
		{InstanceID: "a"}, {InstanceID: "b"}, {InstanceID: "c"},
	}}

	filtered := dataset.Filter([]string{"a", "c"})
	if len(filtered) != 2 {
		t.Fatalf("expected 2, got %d", len(filtered))
	}
	if filtered[0].InstanceID != "a" || filtered[1].InstanceID != "c" {
		t.Errorf("wrong instances: %v, %v", filtered[0].InstanceID, filtered[1].InstanceID)
	}
}

func TestDataset_Filter_NoMatch(t *testing.T) {
	dataset := &Dataset{Instances: []Instance{
		{InstanceID: "a"}, {InstanceID: "b"},
	}}

	filtered := dataset.Filter([]string{"nonexistent"})
	if len(filtered) != 0 {
		t.Errorf("expected 0, got %d", len(filtered))
	}
}

// --- ToBenchmarkTasks tests ---

func TestToBenchmarkTasks(t *testing.T) {
	instances := []Instance{
		{
			InstanceID:       "django__django-16408",
			Repo:             "django/django",
			BaseCommit:       "abc123",
			ProblemStatement: "Fix the bug",
			TestPatch:        "diff patch",
			FailToPass:       `["test_foo"]`,
			PassToPass:       `["test_bar"]`,
		},
	}

	tasks := ToBenchmarkTasks(instances)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}

	task := tasks[0]
	if task.ID != "django__django-16408" {
		t.Errorf("ID = %q", task.ID)
	}
	if task.RepoURL != "https://github.com/django/django.git" {
		t.Errorf("RepoURL = %q", task.RepoURL)
	}
	if task.BaseCommit != "abc123" {
		t.Errorf("BaseCommit = %q", task.BaseCommit)
	}
	if task.Issue != "Fix the bug" {
		t.Errorf("Issue = %q", task.Issue)
	}
	if task.TestPatch != "diff patch" {
		t.Errorf("TestPatch = %q", task.TestPatch)
	}
	if task.EvalFunc == nil {
		t.Error("EvalFunc should not be nil")
	}
}

// --- detectTestRunner tests ---

func TestDetectTestRunner_Pytest(t *testing.T) {
	workDir := t.TempDir()
	os.WriteFile(filepath.Join(workDir, "conftest.py"), []byte(""), 0o644)

	runner := detectTestRunner(workDir, "Python")
	if runner != RunnerPytest {
		t.Errorf("expected pytest, got %q", runner)
	}
}

func TestDetectTestRunner_PytestFromPyproject(t *testing.T) {
	workDir := t.TempDir()
	os.WriteFile(filepath.Join(workDir, "pyproject.toml"), []byte("[tool.pytest]"), 0o644)

	runner := detectTestRunner(workDir, "")
	if runner != RunnerPytest {
		t.Errorf("expected pytest, got %q", runner)
	}
}

func TestDetectTestRunner_Tox(t *testing.T) {
	workDir := t.TempDir()
	os.WriteFile(filepath.Join(workDir, "tox.ini"), []byte("[tox]"), 0o644)

	runner := detectTestRunner(workDir, "Python")
	if runner != RunnerTox {
		t.Errorf("expected tox, got %q", runner)
	}
}

func TestDetectTestRunner_DefaultPython(t *testing.T) {
	workDir := t.TempDir()

	runner := detectTestRunner(workDir, "Python")
	if runner != RunnerPytest {
		t.Errorf("expected pytest default for Python, got %q", runner)
	}
}

func TestDetectTestRunner_UnknownLanguage(t *testing.T) {
	workDir := t.TempDir()

	runner := detectTestRunner(workDir, "Rust")
	if runner != RunnerUnknown {
		t.Errorf("expected unknown for Rust, got %q", runner)
	}
}

// --- buildTestCommand tests ---

func TestBuildTestCommand_Pytest(t *testing.T) {
	cmd := buildTestCommand(RunnerPytest, "tests/test_foo.py")
	// python -m pytest -vs tests/test_foo.py = 5 args
	if len(cmd) != 5 {
		t.Fatalf("expected 5 args, got %d: %v", len(cmd), cmd)
	}
	if cmd[0] != "python" || cmd[2] != "pytest" {
		t.Errorf("unexpected command: %v", cmd)
	}
	if cmd[4] != "tests/test_foo.py" {
		t.Errorf("test file = %q", cmd[4])
	}
}

func TestBuildTestCommand_Pytest_NoStopOnFirstFailure(t *testing.T) {
	cmd := buildTestCommand(RunnerPytest, "tests/test_foo.py")
	joined := strings.Join(cmd, " ")
	// -x stops on first failure, which causes false negatives in evaluation.
	if strings.Contains(joined, "-x") {
		t.Errorf("pytest command should NOT contain -x flag (stops on first failure): %v", cmd)
	}
}

func TestBuildTestCommand_Tox(t *testing.T) {
	cmd := buildTestCommand(RunnerTox, "tests/test_foo.py")
	if cmd[0] != "tox" {
		t.Errorf("expected tox, got %q", cmd[0])
	}
}

// --- Evaluate integration test ---

func TestEvaluate_NoTestPatch(t *testing.T) {
	inst := Instance{
		InstanceID: "test-1",
		FailToPass: "[]",
		PassToPass: "[]",
	}

	result, err := Evaluate(t.TempDir(), inst)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !result.Resolved {
		t.Error("should be resolved when no tests to check")
	}
	if !result.FailToPassAll {
		t.Error("FailToPassAll should be true with empty list")
	}
	if !result.PassToPassAll {
		t.Error("PassToPassAll should be true with empty list")
	}
}

