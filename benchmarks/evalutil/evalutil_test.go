package evalutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- ApplyPatch tests ---

func TestApplyPatch_EmptyPatch(t *testing.T) {
	if err := ApplyPatch(t.TempDir(), ""); err != nil {
		t.Fatalf("expected nil error for empty patch, got: %v", err)
	}
}

func TestApplyPatch_ValidPatch(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Create a file and commit it.
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAdd(t, dir, "hello.txt")
	gitCommit(t, dir, "init")

	// Apply a patch that modifies the file.
	patch := `diff --git a/hello.txt b/hello.txt
--- a/hello.txt
+++ b/hello.txt
@@ -1 +1 @@
-hello
+world
`
	if err := ApplyPatch(dir, patch); err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "world\n" {
		t.Errorf("file content = %q, want %q", string(data), "world\n")
	}
}

func TestApplyPatch_InvalidPatch(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	err := ApplyPatch(dir, "this is not a valid patch")
	if err == nil {
		t.Error("expected error for invalid patch")
	}
}

// --- ParsePytestOutput tests ---

func TestParsePytestOutput(t *testing.T) {
	output := `collecting ... collected 3 items

test_module.py::TestFoo::test_one PASSED
test_module.py::TestFoo::test_two FAILED
test_module.py::TestFoo::test_three ERROR

===== 1 passed, 1 failed, 1 error in 0.5s =====`

	results := ParsePytestOutput(output)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if !results["test_module.py::TestFoo::test_one"] {
		t.Error("test_one should be passed")
	}
	if results["test_module.py::TestFoo::test_two"] {
		t.Error("test_two should be failed")
	}
	if results["test_module.py::TestFoo::test_three"] {
		t.Error("test_three should be failed (ERROR)")
	}
}

func TestParsePytestOutput_Empty(t *testing.T) {
	results := ParsePytestOutput("")
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty output, got %d", len(results))
	}
}

func TestParsePytestOutput_MixedContent(t *testing.T) {
	output := `platform linux -- Python 3.10.0
test_a.py::test_func PASSED
some random output
test_b.py::test_func FAILED`

	results := ParsePytestOutput(output)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if !results["test_a.py::test_func"] {
		t.Error("test_a should be passed")
	}
}

// --- CheckTestsPassed tests ---

func TestCheckTestsPassed_AllPass(t *testing.T) {
	results := map[string]bool{
		"test_a": true,
		"test_b": true,
	}
	if !CheckTestsPassed([]string{"test_a", "test_b"}, results) {
		t.Error("expected true when all tests pass")
	}
}

func TestCheckTestsPassed_OneFails(t *testing.T) {
	results := map[string]bool{
		"test_a": true,
		"test_b": false,
	}
	if CheckTestsPassed([]string{"test_a", "test_b"}, results) {
		t.Error("expected false when one test fails")
	}
}

func TestCheckTestsPassed_MissingTest(t *testing.T) {
	results := map[string]bool{
		"test_a": true,
	}
	if CheckTestsPassed([]string{"test_a", "test_missing"}, results) {
		t.Error("expected false when test is missing from results")
	}
}

func TestCheckTestsPassed_EmptyList(t *testing.T) {
	if !CheckTestsPassed(nil, map[string]bool{"test_a": true}) {
		t.Error("expected true for empty test list")
	}
}

func TestCheckTestsPassed_EmptyResults(t *testing.T) {
	if CheckTestsPassed([]string{"test_a"}, nil) {
		t.Error("expected false when results map is nil")
	}
}

// --- ParsePytestSummary tests ---

func TestParsePytestSummary_AllPassed(t *testing.T) {
	output := "===== 10 passed in 1.23s ====="
	passed, total := ParsePytestSummary(output)
	if passed != 10 || total != 10 {
		t.Errorf("got passed=%d total=%d, want 10/10", passed, total)
	}
}

func TestParsePytestSummary_Mixed(t *testing.T) {
	output := "===== 5 passed, 2 failed, 1 error in 3.45s ====="
	passed, total := ParsePytestSummary(output)
	if passed != 5 {
		t.Errorf("passed = %d, want 5", passed)
	}
	if total != 8 {
		t.Errorf("total = %d, want 8 (5+2+1)", total)
	}
}

func TestParsePytestSummary_NoPassed(t *testing.T) {
	output := "===== 3 failed in 0.5s ====="
	passed, total := ParsePytestSummary(output)
	if passed != 0 {
		t.Errorf("passed = %d, want 0", passed)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
}

func TestParsePytestSummary_Empty(t *testing.T) {
	passed, total := ParsePytestSummary("")
	if passed != 0 || total != 0 {
		t.Errorf("got passed=%d total=%d, want 0/0", passed, total)
	}
}

// --- RunShellCmd tests ---

func TestRunShellCmd_Echo(t *testing.T) {
	out, err := RunShellCmd(t.TempDir(), "echo hello")
	if err != nil {
		t.Fatalf("RunShellCmd: %v", err)
	}
	if out != "hello\n" {
		t.Errorf("output = %q, want %q", out, "hello\n")
	}
}

func TestRunShellCmd_Empty(t *testing.T) {
	_, err := RunShellCmd(t.TempDir(), "")
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestRunShellCmd_CWD(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "testfile.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := RunShellCmd(dir, "cat testfile.txt")
	if err != nil {
		t.Fatalf("RunShellCmd: %v", err)
	}
	if out != "hi" {
		t.Errorf("output = %q, want %q", out, "hi")
	}
}

// --- Truncate tests ---

func TestTruncate_Short(t *testing.T) {
	if got := Truncate("hello", 10); got != "hello" {
		t.Errorf("Truncate short = %q", got)
	}
}

func TestTruncate_Exact(t *testing.T) {
	if got := Truncate("hello", 5); got != "hello" {
		t.Errorf("Truncate exact = %q", got)
	}
}

func TestTruncate_Long(t *testing.T) {
	if got := Truncate("hello world", 5); got != "hello..." {
		t.Errorf("Truncate long = %q, want %q", got, "hello...")
	}
}

// --- Git helpers ---

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

func gitAdd(t *testing.T, dir, file string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", dir, "add", file).CombinedOutput(); err != nil {
		t.Fatalf("git add: %s: %v", string(out), err)
	}
}

func gitCommit(t *testing.T, dir, msg string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", dir, "commit", "-m", msg).CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s: %v", string(out), err)
	}
}
