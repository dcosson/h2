package e2etests

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// h2Binary is the path to the compiled h2 binary, set by TestMain.
var h2Binary string

func TestMain(m *testing.M) {
	// Build h2 binary into a temp directory.
	tmp, err := os.MkdirTemp("", "h2-e2e-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)

	h2Binary = filepath.Join(tmp, "h2")
	cmd := exec.Command("go", "build", "-o", h2Binary, "./cmd/h2")
	cmd.Dir = filepath.Join(mustGetwd(), "..")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: build h2: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func mustGetwd() string {
	dir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return dir
}

// H2Result holds the output of an h2 command execution.
type H2Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// h2Opts configures a single h2 command invocation.
type h2Opts struct {
	h2Dir    string   // H2_DIR env var (empty = unset)
	workDir  string   // working directory for the process
	extraEnv []string // additional KEY=VALUE env vars
}

// createTestH2Dir creates a temp h2 directory by running h2 init.
// Returns the absolute path. Cleanup is automatic via t.Cleanup.
func createTestH2Dir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	h2Dir := filepath.Join(dir, "h2root")

	result := runH2(t, "", "init", h2Dir)
	if result.ExitCode != 0 {
		t.Fatalf("h2 init failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}

	return h2Dir
}

// runH2 executes the h2 binary with the given args. If h2Dir is non-empty,
// H2_DIR is set in the environment. Returns stdout, stderr, and exit code.
func runH2(t *testing.T, h2Dir string, args ...string) H2Result {
	t.Helper()
	return runH2Opts(t, h2Opts{h2Dir: h2Dir}, args...)
}

// runH2WithEnv executes h2 with additional environment variables.
// extraEnv entries are in "KEY=VALUE" format and override os.Environ().
func runH2WithEnv(t *testing.T, h2Dir string, extraEnv []string, args ...string) H2Result {
	t.Helper()
	return runH2Opts(t, h2Opts{h2Dir: h2Dir, extraEnv: extraEnv}, args...)
}

// runH2InDir executes h2 in the given working directory.
func runH2InDir(t *testing.T, workDir string, extraEnv []string, args ...string) H2Result {
	t.Helper()
	return runH2Opts(t, h2Opts{workDir: workDir, extraEnv: extraEnv}, args...)
}

// runH2Opts is the core helper that all runH2* functions delegate to.
func runH2Opts(t *testing.T, opts h2Opts, args ...string) H2Result {
	t.Helper()

	cmd := exec.Command(h2Binary, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if opts.workDir != "" {
		cmd.Dir = opts.workDir
	}

	// Start with a clean env to avoid inheriting the test runner's H2_DIR,
	// and isolate route registration from real ~/.h2/routes.jsonl.
	fakeRootDir := t.TempDir()
	cmd.Env = append(os.Environ(), "H2_DIR=", "H2_ROOT_DIR="+fakeRootDir)
	if opts.h2Dir != "" {
		cmd.Env = append(cmd.Env, "H2_DIR="+opts.h2Dir)
	}
	cmd.Env = append(cmd.Env, opts.extraEnv...)

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("h2 command failed to execute: %v", err)
		}
	}

	return H2Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}
}

// createRole writes a role YAML file into the h2 dir's roles/ directory.
func createRole(t *testing.T, h2Dir, name, content string) {
	t.Helper()
	path := filepath.Join(h2Dir, "roles", name+".yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("create role %s: %v", name, err)
	}
}

// createPodTemplate writes a pod template YAML file into pods/templates/.
func createPodTemplate(t *testing.T, h2Dir, name, content string) {
	t.Helper()
	path := filepath.Join(h2Dir, "pods", "templates", name+".yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("create pod template %s: %v", name, err)
	}
}

// waitForSocket polls for a socket file to appear under h2Dir/sockets/.
// socketType is "agent" or "bridge", name is the agent/bridge name.
// Socket naming convention: sockets/<type>.<name>.sock
func waitForSocket(t *testing.T, h2Dir, socketType, name string) {
	t.Helper()
	sockPath := filepath.Join(h2Dir, "sockets", fmt.Sprintf("%s.%s.sock", socketType, name))

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for socket %s", sockPath)
}

// stopAgent runs h2 stop for the given agent name.
func stopAgent(t *testing.T, h2Dir, name string) {
	t.Helper()
	result := runH2(t, h2Dir, "stop", name)
	if result.ExitCode != 0 {
		t.Logf("warning: h2 stop %s: exit=%d stderr=%s", name, result.ExitCode, result.Stderr)
	}
}
