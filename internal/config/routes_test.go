package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRootDir_Default(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("H2_ROOT_DIR", "")

	got, err := RootDir()
	if err != nil {
		t.Fatalf("RootDir: %v", err)
	}
	want := filepath.Join(fakeHome, ".h2")
	if got != want {
		t.Errorf("RootDir = %q, want %q", got, want)
	}
}

func TestRootDir_EnvOverride(t *testing.T) {
	custom := t.TempDir()
	t.Setenv("H2_ROOT_DIR", custom)

	got, err := RootDir()
	if err != nil {
		t.Fatalf("RootDir: %v", err)
	}
	// Resolve symlinks for comparison (macOS /var -> /private/var).
	wantAbs, _ := filepath.Abs(custom)
	if got != wantAbs {
		t.Errorf("RootDir = %q, want %q", got, wantAbs)
	}
}

func TestReadRoutes_Empty(t *testing.T) {
	rootDir := t.TempDir()

	routes, err := ReadRoutes(rootDir)
	if err != nil {
		t.Fatalf("ReadRoutes: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("expected empty routes, got %d", len(routes))
	}
}

func TestReadRoutes_ParsesEntries(t *testing.T) {
	rootDir := t.TempDir()

	lines := []string{
		`{"prefix":"root","path":"/home/user/.h2"}`,
		`{"prefix":"project-a","path":"/home/user/work/project-a"}`,
		`{"prefix":"h2home","path":"/home/user/h2home"}`,
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(rootDir, "routes.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	routes, err := ReadRoutes(rootDir)
	if err != nil {
		t.Fatalf("ReadRoutes: %v", err)
	}
	if len(routes) != 3 {
		t.Fatalf("expected 3 routes, got %d", len(routes))
	}

	if routes[0].Prefix != "root" || routes[0].Path != "/home/user/.h2" {
		t.Errorf("routes[0] = %+v", routes[0])
	}
	if routes[1].Prefix != "project-a" || routes[1].Path != "/home/user/work/project-a" {
		t.Errorf("routes[1] = %+v", routes[1])
	}
	if routes[2].Prefix != "h2home" || routes[2].Path != "/home/user/h2home" {
		t.Errorf("routes[2] = %+v", routes[2])
	}
}

func TestReadRoutes_SkipsBlankLines(t *testing.T) {
	rootDir := t.TempDir()

	content := `{"prefix":"a","path":"/a"}

{"prefix":"b","path":"/b"}
`
	if err := os.WriteFile(filepath.Join(rootDir, "routes.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	routes, err := ReadRoutes(rootDir)
	if err != nil {
		t.Fatalf("ReadRoutes: %v", err)
	}
	if len(routes) != 2 {
		t.Errorf("expected 2 routes, got %d", len(routes))
	}
}

func TestReadRoutes_InvalidJSON(t *testing.T) {
	rootDir := t.TempDir()

	content := `{"prefix":"a","path":"/a"}
not valid json
`
	if err := os.WriteFile(filepath.Join(rootDir, "routes.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadRoutes(rootDir)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error = %q, want it to mention line 2", err.Error())
	}
}

func TestRegisterRoute_AppendsToFile(t *testing.T) {
	rootDir := t.TempDir()

	// Register first route.
	if err := RegisterRoute(rootDir, Route{Prefix: "alpha", Path: "/alpha"}); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	// Register second route.
	if err := RegisterRoute(rootDir, Route{Prefix: "beta", Path: "/beta"}); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	// Read back.
	routes, err := ReadRoutes(rootDir)
	if err != nil {
		t.Fatalf("ReadRoutes: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}
	if routes[0].Prefix != "alpha" {
		t.Errorf("routes[0].Prefix = %q, want %q", routes[0].Prefix, "alpha")
	}
	if routes[1].Prefix != "beta" {
		t.Errorf("routes[1].Prefix = %q, want %q", routes[1].Prefix, "beta")
	}

	// Verify the file is valid JSONL.
	data, err := os.ReadFile(filepath.Join(rootDir, "routes.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines in file, got %d", len(lines))
	}
	for i, line := range lines {
		var r Route
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Errorf("line %d not valid JSON: %v", i+1, err)
		}
	}
}

func TestRegisterRoute_RejectsExistingPrefix(t *testing.T) {
	rootDir := t.TempDir()

	if err := RegisterRoute(rootDir, Route{Prefix: "myapp", Path: "/a"}); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	err := RegisterRoute(rootDir, Route{Prefix: "myapp", Path: "/b"})
	if err == nil {
		t.Fatal("expected error for duplicate prefix")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("error = %q, want it to contain 'already registered'", err.Error())
	}
}

func TestRegisterRoute_RejectsDuplicatePath(t *testing.T) {
	rootDir := t.TempDir()
	dir := t.TempDir() // a real path so Abs works

	if err := RegisterRoute(rootDir, Route{Prefix: "first", Path: dir}); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	err := RegisterRoute(rootDir, Route{Prefix: "second", Path: dir})
	if err == nil {
		t.Fatal("expected error for duplicate path")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("error = %q, want it to contain 'already registered'", err.Error())
	}
}

func TestRegisterRoute_RejectsEmptyFields(t *testing.T) {
	rootDir := t.TempDir()

	if err := RegisterRoute(rootDir, Route{Prefix: "", Path: "/a"}); err == nil {
		t.Error("expected error for empty prefix")
	}
	if err := RegisterRoute(rootDir, Route{Prefix: "a", Path: ""}); err == nil {
		t.Error("expected error for empty path")
	}
}

func TestResolvePrefix_Default(t *testing.T) {
	rootDir := t.TempDir()

	// No routes file — no conflicts.
	prefix, err := ResolvePrefix(rootDir, "", "/home/user/myproject")
	if err != nil {
		t.Fatalf("ResolvePrefix: %v", err)
	}
	if prefix != "myproject" {
		t.Errorf("prefix = %q, want %q", prefix, "myproject")
	}
}

func TestResolvePrefix_UsesDesired(t *testing.T) {
	rootDir := t.TempDir()

	prefix, err := ResolvePrefix(rootDir, "custom", "/home/user/myproject")
	if err != nil {
		t.Fatalf("ResolvePrefix: %v", err)
	}
	if prefix != "custom" {
		t.Errorf("prefix = %q, want %q", prefix, "custom")
	}
}

func TestResolvePrefix_AutoIncrement(t *testing.T) {
	rootDir := t.TempDir()

	// Register "myproject" prefix.
	if err := RegisterRoute(rootDir, Route{Prefix: "myproject", Path: "/a"}); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	prefix, err := ResolvePrefix(rootDir, "", "/home/user/myproject")
	if err != nil {
		t.Fatalf("ResolvePrefix: %v", err)
	}
	if prefix != "myproject-2" {
		t.Errorf("prefix = %q, want %q", prefix, "myproject-2")
	}
}

func TestResolvePrefix_AutoIncrementChain(t *testing.T) {
	rootDir := t.TempDir()

	// Register "foo", "foo-2", "foo-3".
	for _, p := range []string{"foo", "foo-2", "foo-3"} {
		if err := RegisterRoute(rootDir, Route{Prefix: p, Path: "/" + p}); err != nil {
			t.Fatalf("RegisterRoute(%s): %v", p, err)
		}
	}

	prefix, err := ResolvePrefix(rootDir, "foo", "/new")
	if err != nil {
		t.Fatalf("ResolvePrefix: %v", err)
	}
	if prefix != "foo-4" {
		t.Errorf("prefix = %q, want %q", prefix, "foo-4")
	}
}

func TestResolvePrefix_RootDir(t *testing.T) {
	rootDir := t.TempDir()

	prefix, err := ResolvePrefix(rootDir, "anything", rootDir)
	if err != nil {
		t.Fatalf("ResolvePrefix: %v", err)
	}
	if prefix != "root" {
		t.Errorf("prefix = %q, want %q", prefix, "root")
	}
}

func TestResolvePrefix_RootDirIgnoresDesired(t *testing.T) {
	rootDir := t.TempDir()

	// Even if desired is something else, root dir always gets "root".
	prefix, err := ResolvePrefix(rootDir, "customname", rootDir)
	if err != nil {
		t.Fatalf("ResolvePrefix: %v", err)
	}
	if prefix != "root" {
		t.Errorf("prefix = %q, want %q", prefix, "root")
	}
}

func TestConcurrentRegistration(t *testing.T) {
	rootDir := t.TempDir()

	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			prefix := "agent"
			path := filepath.Join(t.TempDir(), "dir")
			// Use ResolvePrefix to get unique prefix, then register.
			// We need to do this under the write lock to avoid races,
			// but for this test we just register with unique prefixes.
			errs[i] = RegisterRoute(rootDir, Route{
				Prefix: prefix + "-" + string(rune('a'+i)),
				Path:   path,
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}

	// Verify all routes were written.
	routes, err := ReadRoutes(rootDir)
	if err != nil {
		t.Fatalf("ReadRoutes: %v", err)
	}
	if len(routes) != n {
		t.Errorf("expected %d routes, got %d", n, len(routes))
	}

	// Verify no duplicate prefixes.
	seen := make(map[string]bool)
	for _, r := range routes {
		if seen[r.Prefix] {
			t.Errorf("duplicate prefix %q", r.Prefix)
		}
		seen[r.Prefix] = true
	}
}

func TestConcurrentRegistration_SamePrefix(t *testing.T) {
	rootDir := t.TempDir()

	// Try to register the same prefix concurrently — only one should succeed.
	const n = 5
	var wg sync.WaitGroup
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = RegisterRoute(rootDir, Route{
				Prefix: "conflicting",
				Path:   filepath.Join(t.TempDir(), "dir"),
			})
		}(i)
	}
	wg.Wait()

	successes := 0
	failures := 0
	for _, err := range errs {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}

	if successes != 1 {
		t.Errorf("expected exactly 1 success, got %d", successes)
	}
	if failures != n-1 {
		t.Errorf("expected %d failures, got %d", n-1, failures)
	}
}

func TestReadRoutes_CreatesRootDirIfMissing(t *testing.T) {
	rootDir := filepath.Join(t.TempDir(), "nonexistent", "root")

	routes, err := ReadRoutes(rootDir)
	if err != nil {
		t.Fatalf("ReadRoutes: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("expected empty routes, got %d", len(routes))
	}

	// Root dir should have been created.
	info, err := os.Stat(rootDir)
	if err != nil {
		t.Fatalf("expected root dir to exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected root dir to be a directory")
	}
}

func TestRegisterRoute_CreatesRootDirIfMissing(t *testing.T) {
	rootDir := filepath.Join(t.TempDir(), "nonexistent", "root")

	if err := RegisterRoute(rootDir, Route{Prefix: "test", Path: "/test"}); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	routes, err := ReadRoutes(rootDir)
	if err != nil {
		t.Fatalf("ReadRoutes: %v", err)
	}
	if len(routes) != 1 {
		t.Errorf("expected 1 route, got %d", len(routes))
	}
}
