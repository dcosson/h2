package cmd

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2/internal/config"
	"h2/internal/session/message"
	"h2/internal/socketdir"
)

func setupPodTestEnv(t *testing.T) string {
	t.Helper()
	config.ResetResolveCache()
	socketdir.ResetDirCache()
	t.Cleanup(func() {
		config.ResetResolveCache()
		socketdir.ResetDirCache()
	})

	// Use /tmp for short socket paths (macOS limit).
	tmpDir, err := os.MkdirTemp("/tmp", "h2t-pod")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	t.Setenv("HOME", tmpDir)
	t.Setenv("H2_DIR", "")

	h2Root := filepath.Join(tmpDir, ".h2")
	os.MkdirAll(filepath.Join(h2Root, "sockets"), 0o700)
	os.MkdirAll(filepath.Join(h2Root, "roles"), 0o755)
	os.MkdirAll(filepath.Join(h2Root, "pods", "roles"), 0o755)
	os.MkdirAll(filepath.Join(h2Root, "pods", "templates"), 0o755)
	os.MkdirAll(filepath.Join(h2Root, "sessions"), 0o755)
	os.MkdirAll(filepath.Join(h2Root, "claude-config", "default"), 0o755)
	config.WriteMarker(h2Root)

	return h2Root
}

func TestPodListCmd_NoTemplates(t *testing.T) {
	setupPodTestEnv(t)

	cmd := newPodListCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPodListCmd_ShowsTemplates(t *testing.T) {
	h2Root := setupPodTestEnv(t)

	// Create a template.
	tmplContent := `pod_name: backend-team
agents:
  - name: builder
    role: default
  - name: tester
    role: default
`
	os.WriteFile(filepath.Join(h2Root, "pods", "templates", "backend.yaml"), []byte(tmplContent), 0o644)

	cmd := newPodListCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPodStopCmd_RequiresArg(t *testing.T) {
	cmd := newPodStopCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no args provided")
	}
}

func TestPodStopCmd_StopsOnlyPodAgents(t *testing.T) {
	h2Root := setupPodTestEnv(t)
	sockDir := filepath.Join(h2Root, "sockets")

	// Create two mock agents: one in the target pod, one not.
	type mockAgent struct {
		name    string
		pod     string
		stopped bool
	}
	agents := []mockAgent{
		{name: "in-pod", pod: "my-pod"},
		{name: "not-in-pod", pod: "other"},
	}

	var listeners []net.Listener
	for i := range agents {
		a := &agents[i]
		sockPath := filepath.Join(sockDir, socketdir.Format(socketdir.TypeAgent, a.name))
		ln, err := net.Listen("unix", sockPath)
		if err != nil {
			t.Fatalf("listen %s: %v", a.name, err)
		}
		listeners = append(listeners, ln)

		go func(agent *mockAgent, listener net.Listener) {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				req, err := message.ReadRequest(conn)
				if err != nil {
					conn.Close()
					return
				}
				switch req.Type {
				case "status":
					message.SendResponse(conn, &message.Response{
						OK: true,
						Agent: &message.AgentInfo{
							Name:    agent.name,
							Command: "claude",
							State:   "idle",
							Pod:     agent.pod,
						},
					})
				case "stop":
					agent.stopped = true
					message.SendResponse(conn, &message.Response{OK: true})
				}
				conn.Close()
			}
		}(a, ln)
	}
	t.Cleanup(func() {
		for _, ln := range listeners {
			ln.Close()
		}
	})

	cmd := newPodStopCmd()
	cmd.SetArgs([]string{"my-pod"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !agents[0].stopped {
		t.Error("expected in-pod agent to be stopped")
	}
	if agents[1].stopped {
		t.Error("expected not-in-pod agent to NOT be stopped")
	}
}

func TestPodStopCmd_NoPodAgents(t *testing.T) {
	setupPodTestEnv(t)

	cmd := newPodStopCmd()
	cmd.SetArgs([]string{"nonexistent-pod"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPodRunningAgents(t *testing.T) {
	h2Root := setupPodTestEnv(t)
	sockDir := filepath.Join(h2Root, "sockets")

	// Create two mock agents: one in "my-pod", one in "other-pod".
	type mockAgent struct {
		name string
		pod  string
	}
	agents := []mockAgent{
		{name: "coder", pod: "my-pod"},
		{name: "reviewer", pod: "my-pod"},
		{name: "unrelated", pod: "other-pod"},
	}

	var listeners []net.Listener
	for _, a := range agents {
		sockPath := filepath.Join(sockDir, socketdir.Format(socketdir.TypeAgent, a.name))
		ln, err := net.Listen("unix", sockPath)
		if err != nil {
			t.Fatalf("listen %s: %v", a.name, err)
		}
		listeners = append(listeners, ln)

		go func(agent mockAgent, listener net.Listener) {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				req, err := message.ReadRequest(conn)
				if err != nil {
					conn.Close()
					return
				}
				if req.Type == "status" {
					message.SendResponse(conn, &message.Response{
						OK: true,
						Agent: &message.AgentInfo{
							Name: agent.name,
							Pod:  agent.pod,
						},
					})
				}
				conn.Close()
			}
		}(a, ln)
	}
	t.Cleanup(func() {
		for _, ln := range listeners {
			ln.Close()
		}
	})

	running := podRunningAgents("my-pod")

	if !running["coder"] {
		t.Error("expected coder to be running in my-pod")
	}
	if !running["reviewer"] {
		t.Error("expected reviewer to be running in my-pod")
	}
	if running["unrelated"] {
		t.Error("expected unrelated to NOT be in my-pod")
	}
}

func TestPodLaunchCmd_RequiresArg(t *testing.T) {
	cmd := newPodLaunchCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no args provided")
	}
}

func TestPodLaunchCmd_InvalidTemplate(t *testing.T) {
	setupPodTestEnv(t)

	cmd := newPodLaunchCmd()
	cmd.SetArgs([]string{"nonexistent"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent template")
	}
}

func TestPodLaunchCmd_InvalidVarFlag(t *testing.T) {
	setupPodTestEnv(t)

	cmd := newPodLaunchCmd()
	cmd.SetArgs([]string{"--var", "noequals", "mytemplate"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid --var flag")
	}
}

func TestPodLaunchCmd_MissingRequiredVar(t *testing.T) {
	h2Root := setupPodTestEnv(t)

	// Template with a required variable.
	tmplContent := `variables:
  team:
    description: Team name

pod_name: test
agents:
  - name: worker
    role: default
`
	os.WriteFile(filepath.Join(h2Root, "pods", "templates", "needsvar.yaml"), []byte(tmplContent), 0o644)

	cmd := newPodLaunchCmd()
	cmd.SetArgs([]string{"needsvar"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing required var")
	}
}

func TestPodLaunchCmd_TemplateWithVarRendering(t *testing.T) {
	h2Root := setupPodTestEnv(t)

	// Template that uses a variable in an agent name.
	tmplContent := `variables:
  prefix:
    default: dev

pod_name: test
agents:
  - name: "{{ .Var.prefix }}-worker"
    role: default
`
	os.WriteFile(filepath.Join(h2Root, "pods", "templates", "vartest.yaml"), []byte(tmplContent), 0o644)

	// Create the default role so it can be loaded.
	roleContent := "name: default\ninstructions: |\n  test\n"
	os.WriteFile(filepath.Join(h2Root, "roles", "default.yaml"), []byte(roleContent), 0o644)

	// This will fail at setupAndForkAgentQuiet (can't actually fork in tests),
	// but we verify it gets past template rendering and var parsing.
	cmd := newPodLaunchCmd()
	cmd.SetArgs([]string{"vartest"})
	err := cmd.Execute()
	// We expect it to either succeed or fail at the fork step, not at template rendering.
	if err != nil {
		// If it fails, it should be at the fork/setup stage, not at template rendering.
		errStr := err.Error()
		if strings.Contains(errStr, "required variables") || strings.Contains(errStr, "template") {
			t.Fatalf("should not fail at template rendering: %v", err)
		}
	}
}

func TestPodLaunchCmd_CLIVarOverridesDefault(t *testing.T) {
	h2Root := setupPodTestEnv(t)

	tmplContent := `variables:
  prefix:
    default: dev

pod_name: test
agents:
  - name: "{{ .Var.prefix }}-worker"
    role: default
`
	os.WriteFile(filepath.Join(h2Root, "pods", "templates", "override.yaml"), []byte(tmplContent), 0o644)

	roleContent := "name: default\ninstructions: |\n  test\n"
	os.WriteFile(filepath.Join(h2Root, "roles", "default.yaml"), []byte(roleContent), 0o644)

	cmd := newPodLaunchCmd()
	cmd.SetArgs([]string{"--var", "prefix=staging", "override"})
	err := cmd.Execute()
	// Will fail at fork step, but should not fail at template rendering.
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "required variables") || strings.Contains(errStr, "template") {
			t.Fatalf("should not fail at template rendering: %v", err)
		}
	}
}
