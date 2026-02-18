package swe_bench_verified

import (
	"os"
	"path/filepath"
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
		FailToPass: `["tests/test_foo.py::TestFoo::test_bar"]`,
	}

	tests, err := inst.ParseFailToPass()
	if err != nil {
		t.Fatalf("ParseFailToPass: %v", err)
	}
	if len(tests) != 1 {
		t.Fatalf("expected 1 test, got %d", len(tests))
	}
	if tests[0] != "tests/test_foo.py::TestFoo::test_bar" {
		t.Errorf("tests[0] = %q", tests[0])
	}
}

func TestInstance_ParsePassToPass(t *testing.T) {
	inst := Instance{PassToPass: `["test_a", "test_b"]`}

	tests, err := inst.ParsePassToPass()
	if err != nil {
		t.Fatalf("ParsePassToPass: %v", err)
	}
	if len(tests) != 2 {
		t.Fatalf("expected 2 tests, got %d", len(tests))
	}
}

func TestInstance_ParseEmpty(t *testing.T) {
	inst := Instance{FailToPass: "[]"}
	tests, err := inst.ParseFailToPass()
	if err != nil {
		t.Fatalf("ParseFailToPass: %v", err)
	}
	if len(tests) != 0 {
		t.Errorf("expected 0 tests, got %d", len(tests))
	}
}

func TestInstance_ParseEmptyString(t *testing.T) {
	inst := Instance{FailToPass: ""}
	tests, err := inst.ParseFailToPass()
	if err != nil {
		t.Fatalf("ParseFailToPass: %v", err)
	}
	if tests != nil {
		t.Errorf("expected nil, got %v", tests)
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
			"instance_id": "django__django-16379",
			"repo": "django/django",
			"base_commit": "abc123",
			"problem_statement": "Fix the caching bug",
			"patch": "diff --git a/foo.py",
			"test_patch": "diff --git a/test_foo.py",
			"fail_to_pass": "[\"tests/test_cache.py::test_fix\"]",
			"pass_to_pass": "[]",
			"selected_test_files_to_run": "[\"tests/test_cache.py\"]",
			"repo_language": "Python",
			"version": "5.0"
		}
	]`

	path := filepath.Join(t.TempDir(), "dataset.json")
	os.WriteFile(path, []byte(data), 0o644)

	dataset, err := LoadDataset(path)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}

	if len(dataset.Instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(dataset.Instances))
	}

	inst := dataset.Instances[0]
	if inst.InstanceID != "django__django-16379" {
		t.Errorf("InstanceID = %q", inst.InstanceID)
	}
	if inst.Version != "5.0" {
		t.Errorf("Version = %q", inst.Version)
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

// --- Filter tests ---

func TestDataset_Filter_All(t *testing.T) {
	dataset := &Dataset{Instances: []Instance{
		{InstanceID: "a"}, {InstanceID: "b"},
	}}

	filtered := dataset.Filter(nil)
	if len(filtered) != 2 {
		t.Errorf("expected 2, got %d", len(filtered))
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
}

func TestDataset_Filter_NoMatch(t *testing.T) {
	dataset := &Dataset{Instances: []Instance{{InstanceID: "a"}}}

	filtered := dataset.Filter([]string{"nonexistent"})
	if len(filtered) != 0 {
		t.Errorf("expected 0, got %d", len(filtered))
	}
}

// --- ToBenchmarkTasks tests ---

func TestToBenchmarkTasks(t *testing.T) {
	instances := []Instance{
		{
			InstanceID:       "django__django-16379",
			Repo:             "django/django",
			BaseCommit:       "abc123",
			ProblemStatement: "Fix the bug",
			TestPatch:        "diff patch",
			FailToPass:       `["test_foo"]`,
		},
	}

	tasks := ToBenchmarkTasks(instances)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}

	task := tasks[0]
	if task.ID != "django__django-16379" {
		t.Errorf("ID = %q", task.ID)
	}
	if task.RepoURL != "https://github.com/django/django.git" {
		t.Errorf("RepoURL = %q", task.RepoURL)
	}
	if task.TestPatch != "diff patch" {
		t.Errorf("TestPatch = %q", task.TestPatch)
	}
	if task.EvalFunc == nil {
		t.Error("EvalFunc should not be nil")
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
		t.Error("should be resolved with no tests")
	}
}

