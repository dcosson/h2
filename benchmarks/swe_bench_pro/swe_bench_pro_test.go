package swe_bench_pro

import (
	"os"
	"os/exec"
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

// --- Eval tests ---

func TestParsePytestOutput(t *testing.T) {
	output := `
tests/test_foo.py::TestFoo::test_bar PASSED
tests/test_foo.py::TestFoo::test_baz FAILED
tests/test_foo.py::TestFoo::test_err ERROR
`
	results := parsePytestOutput(output)

	if !results["tests/test_foo.py::TestFoo::test_bar"] {
		t.Error("test_bar should be PASSED")
	}
	if results["tests/test_foo.py::TestFoo::test_baz"] {
		t.Error("test_baz should be FAILED")
	}
	if results["tests/test_foo.py::TestFoo::test_err"] {
		t.Error("test_err should be ERROR (treated as failed)")
	}
}

func TestParsePytestOutput_Empty(t *testing.T) {
	results := parsePytestOutput("")
	if len(results) != 0 {
		t.Errorf("expected 0 results from empty output, got %d", len(results))
	}
}

func TestParsePytestOutput_MixedContent(t *testing.T) {
	output := `
============================= test session starts =============================
collecting ... collected 3 items

tests/test_foo.py::test_one PASSED
tests/test_foo.py::test_two PASSED
tests/test_foo.py::test_three FAILED

=========================== short test summary info ===========================
FAILED tests/test_foo.py::test_three - AssertionError
============================== 1 failed, 2 passed =============================
`
	results := parsePytestOutput(output)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if !results["tests/test_foo.py::test_one"] {
		t.Error("test_one should pass")
	}
	if !results["tests/test_foo.py::test_two"] {
		t.Error("test_two should pass")
	}
	if results["tests/test_foo.py::test_three"] {
		t.Error("test_three should fail")
	}
}

func TestCheckTestsPassed_AllPass(t *testing.T) {
	results := map[string]bool{
		"test_a": true,
		"test_b": true,
	}
	if !checkTestsPassed([]string{"test_a", "test_b"}, results) {
		t.Error("expected all tests to pass")
	}
}

func TestCheckTestsPassed_OneFails(t *testing.T) {
	results := map[string]bool{
		"test_a": true,
		"test_b": false,
	}
	if checkTestsPassed([]string{"test_a", "test_b"}, results) {
		t.Error("expected failure when one test fails")
	}
}

func TestCheckTestsPassed_MissingTest(t *testing.T) {
	results := map[string]bool{
		"test_a": true,
	}
	if checkTestsPassed([]string{"test_a", "test_missing"}, results) {
		t.Error("expected failure when test is missing from results")
	}
}

func TestCheckTestsPassed_EmptyList(t *testing.T) {
	results := map[string]bool{"test_a": false}
	if !checkTestsPassed(nil, results) {
		t.Error("empty test list should pass")
	}
}

func TestCheckTestsPassed_EmptyResults(t *testing.T) {
	if checkTestsPassed([]string{"test_a"}, nil) {
		t.Error("should fail when results is nil but tests are expected")
	}
}

// --- ApplyPatch tests ---

func TestApplyPatch_EmptyPatch(t *testing.T) {
	err := ApplyPatch(t.TempDir(), "")
	if err != nil {
		t.Fatalf("ApplyPatch with empty patch: %v", err)
	}
}

func TestApplyPatch_ValidPatch(t *testing.T) {
	workDir := t.TempDir()
	initTestGitRepo(t, workDir)

	// Create a file to patch.
	os.WriteFile(filepath.Join(workDir, "hello.py"), []byte("print('hello')\n"), 0o644)
	gitAdd(t, workDir, "hello.py")
	gitCommit(t, workDir, "add hello.py")

	// Create a patch that modifies the file.
	patch := `diff --git a/hello.py b/hello.py
index 1234567..abcdefg 100644
--- a/hello.py
+++ b/hello.py
@@ -1 +1,2 @@
 print('hello')
+print('world')
`
	err := ApplyPatch(workDir, patch)
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	// Verify the file was patched.
	data, _ := os.ReadFile(filepath.Join(workDir, "hello.py"))
	if !strings.Contains(string(data), "world") {
		t.Error("patch should have added 'world' to hello.py")
	}
}

func TestApplyPatch_InvalidPatch(t *testing.T) {
	workDir := t.TempDir()
	initTestGitRepo(t, workDir)

	// Patch that doesn't match anything.
	err := ApplyPatch(workDir, "garbage not a patch at all")
	if err == nil {
		t.Error("expected error for invalid patch")
	}
	if !strings.Contains(err.Error(), "all patch strategies failed") {
		t.Errorf("error should mention strategies failed, got: %v", err)
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

// --- Utility tests ---

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("truncate short = %q", got)
	}
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("truncate long = %q, want %q", got, "hello...")
	}
}

// --- Helpers ---

func initTestGitRepo(t *testing.T, dir string) {
	t.Helper()
	runCmd(t, dir, "git", "init")
	runCmd(t, dir, "git", "config", "user.email", "test@test.com")
	runCmd(t, dir, "git", "config", "user.name", "Test")
}

func gitAdd(t *testing.T, dir, file string) {
	t.Helper()
	runCmd(t, dir, "git", "add", file)
}

func gitCommit(t *testing.T, dir, msg string) {
	t.Helper()
	runCmd(t, dir, "git", "commit", "-m", msg)
}

func runCmd(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %s: %v", name, args, string(out), err)
	}
}
