package cmd

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2/internal/config"
	"h2/internal/session"
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
	t.Setenv("H2_ROOT_DIR", filepath.Join(tmpDir, ".h2"))
	t.Setenv("H2_GATEWAY", "0")

	h2Root := filepath.Join(tmpDir, ".h2")
	os.MkdirAll(filepath.Join(h2Root, "sockets"), 0o700)
	os.MkdirAll(filepath.Join(h2Root, "roles"), 0o755)
	os.MkdirAll(filepath.Join(h2Root, "pods"), 0o755)
	os.MkdirAll(filepath.Join(h2Root, "sessions"), 0o755)
	os.MkdirAll(filepath.Join(h2Root, "claude-config", "default"), 0o755)
	config.WriteMarker(h2Root)

	t.Setenv("H2_DIR", h2Root)

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
	os.WriteFile(filepath.Join(h2Root, "pods", "backend.yaml"), []byte(tmplContent), 0o644)

	var buf bytes.Buffer
	cmd := newPodListCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "backend-team") {
		t.Errorf("expected 'backend-team' in output, got: %s", out)
	}
	if !strings.Contains(out, "builder") {
		t.Errorf("expected 'builder' in output, got: %s", out)
	}
	if !strings.Contains(out, "tester") {
		t.Errorf("expected 'tester' in output, got: %s", out)
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
	os.WriteFile(filepath.Join(h2Root, "pods", "needsvar.yaml"), []byte(tmplContent), 0o644)

	cmd := newPodLaunchCmd()
	cmd.SetArgs([]string{"needsvar"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing required var")
	}
}

func TestPodLaunchCmd_TemplateWithVarRendering(t *testing.T) {
	h2Root := setupPodTestEnv(t)

	// Mock ForkDaemon to prevent spawning real processes.
	var forkSessionDirs []string
	origFork := forkDaemonFunc
	forkDaemonFunc = func(sessionDir string, hints session.TerminalHints, resume bool) error {
		forkSessionDirs = append(forkSessionDirs, sessionDir)
		return nil
	}
	t.Cleanup(func() { forkDaemonFunc = origFork })

	// Template that uses a variable in an agent name.
	tmplContent := `variables:
  prefix:
    default: dev

pod_name: test
agents:
  - name: "{{ .Var.prefix }}-worker"
    role: default
`
	os.WriteFile(filepath.Join(h2Root, "pods", "vartest.yaml"), []byte(tmplContent), 0o644)

	// Create the default role so it can be loaded.
	roleContent := "role_name: default\ninstructions: |\n  test\n"
	os.WriteFile(filepath.Join(h2Root, "roles", "default.yaml"), []byte(roleContent), 0o644)

	cmd := newPodLaunchCmd()
	cmd.SetArgs([]string{"vartest"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(forkSessionDirs) != 1 {
		t.Fatalf("expected 1 fork call, got %d", len(forkSessionDirs))
	}
	rc, err := config.ReadRuntimeConfig(forkSessionDirs[0])
	if err != nil {
		t.Fatalf("read runtime config: %v", err)
	}
	if rc.AgentName != "dev-worker" {
		t.Errorf("expected agent name 'dev-worker', got %q", rc.AgentName)
	}
}

func TestPodLaunchCmd_CLIVarOverridesDefault(t *testing.T) {
	h2Root := setupPodTestEnv(t)

	// Mock ForkDaemon to prevent spawning real processes.
	var forkSessionDirs []string
	origFork := forkDaemonFunc
	forkDaemonFunc = func(sessionDir string, hints session.TerminalHints, resume bool) error {
		forkSessionDirs = append(forkSessionDirs, sessionDir)
		return nil
	}
	t.Cleanup(func() { forkDaemonFunc = origFork })

	tmplContent := `variables:
  prefix:
    default: dev

pod_name: test
agents:
  - name: "{{ .Var.prefix }}-worker"
    role: default
`
	os.WriteFile(filepath.Join(h2Root, "pods", "override.yaml"), []byte(tmplContent), 0o644)

	roleContent := "role_name: default\ninstructions: |\n  test\n"
	os.WriteFile(filepath.Join(h2Root, "roles", "default.yaml"), []byte(roleContent), 0o644)

	cmd := newPodLaunchCmd()
	cmd.SetArgs([]string{"--var", "prefix=staging", "override"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(forkSessionDirs) != 1 {
		t.Fatalf("expected 1 fork call, got %d", len(forkSessionDirs))
	}
	rc, err := config.ReadRuntimeConfig(forkSessionDirs[0])
	if err != nil {
		t.Fatalf("read runtime config: %v", err)
	}
	if rc.AgentName != "staging-worker" {
		t.Errorf("expected agent name 'staging-worker', got %q", rc.AgentName)
	}
}

func TestPodLaunchBridges_SkipsBridgeAlreadyRunningForSamePod(t *testing.T) {
	h2Root := setupPodTestEnv(t)
	sockDir := filepath.Join(h2Root, "sockets")

	configYAML := `bridges:
  personal:
    macos_notify:
      enabled: true
`
	if err := os.WriteFile(filepath.Join(h2Root, "config.yaml"), []byte(configYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	agentSock := filepath.Join(sockDir, socketdir.Format(socketdir.TypeAgent, "sage"))
	agentListener, err := net.Listen("unix", agentSock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { agentListener.Close() })
	go func() {
		for {
			conn, err := agentListener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				req, err := message.ReadRequest(conn)
				if err != nil {
					return
				}
				if req.Type == "status" {
					_ = message.SendResponse(conn, &message.Response{
						OK: true,
						Agent: &message.AgentInfo{
							Name: "sage",
							Pod:  "dev-pod",
						},
					})
				}
			}()
		}
	}()

	bridgeSock := filepath.Join(sockDir, socketdir.Format(socketdir.TypeBridge, "personal"))
	bridgeListener, err := net.Listen("unix", bridgeSock)
	if err != nil {
		t.Fatal(err)
	}
	var stopRequests int
	t.Cleanup(func() { bridgeListener.Close() })
	go func() {
		for {
			conn, err := bridgeListener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				req, err := message.ReadRequest(conn)
				if err != nil {
					return
				}
				switch req.Type {
				case "status":
					_ = message.SendResponse(conn, &message.Response{
						OK: true,
						Bridge: &message.BridgeInfo{
							Name: "personal",
							Pod:  "dev-pod",
						},
					})
				case "stop":
					stopRequests++
					_ = message.SendResponse(conn, &message.Response{OK: true})
				}
			}()
		}
	}()

	origForkBridge := forkBridgeFunc
	forkCalls := 0
	forkBridgeFunc = func(bridgeName, concierge, pod string) error {
		forkCalls++
		return nil
	}
	t.Cleanup(func() { forkBridgeFunc = origForkBridge })

	if err := podLaunchBridges([]config.PodBridge{{Bridge: "personal", Concierge: "sage"}}, "dev-pod"); err != nil {
		t.Fatalf("podLaunchBridges returned error: %v", err)
	}

	if forkCalls != 0 {
		t.Fatalf("expected no bridge relaunch, got %d fork call(s)", forkCalls)
	}
	if stopRequests != 0 {
		t.Fatalf("expected no bridge stop request, got %d", stopRequests)
	}
}

func TestPodLaunchBridges_FailsWhenBridgeAlreadyRunningStandalone(t *testing.T) {
	h2Root := setupPodTestEnv(t)
	sockDir := filepath.Join(h2Root, "sockets")

	configYAML := `bridges:
  personal:
    macos_notify:
      enabled: true
`
	if err := os.WriteFile(filepath.Join(h2Root, "config.yaml"), []byte(configYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	agentSock := filepath.Join(sockDir, socketdir.Format(socketdir.TypeAgent, "sage"))
	agentListener, err := net.Listen("unix", agentSock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { agentListener.Close() })
	go func() {
		for {
			conn, err := agentListener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				req, err := message.ReadRequest(conn)
				if err != nil {
					return
				}
				if req.Type == "status" {
					_ = message.SendResponse(conn, &message.Response{
						OK: true,
						Agent: &message.AgentInfo{
							Name: "sage",
							Pod:  "dev-pod",
						},
					})
				}
			}()
		}
	}()

	bridgeSock := filepath.Join(sockDir, socketdir.Format(socketdir.TypeBridge, "personal"))
	bridgeListener, err := net.Listen("unix", bridgeSock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bridgeListener.Close() })
	go func() {
		for {
			conn, err := bridgeListener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				req, err := message.ReadRequest(conn)
				if err != nil {
					return
				}
				if req.Type == "status" {
					_ = message.SendResponse(conn, &message.Response{
						OK: true,
						Bridge: &message.BridgeInfo{
							Name: "personal",
							Pod:  "",
						},
					})
				}
			}()
		}
	}()

	err = podLaunchBridges([]config.PodBridge{{Bridge: "personal", Concierge: "sage"}}, "dev-pod")
	if err == nil {
		t.Fatal("expected error for standalone bridge already running")
	}
	if !strings.Contains(err.Error(), `bridge "personal" is already running`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPodLaunchCmd_AttachesInSupportedTerminal(t *testing.T) {
	h2Root := setupPodTestEnv(t)

	tmplContent := `pod_name: dev-pod
agents:
  - name: worker
    role: default
`
	if err := os.WriteFile(filepath.Join(h2Root, "pods", "devpod.yaml"), []byte(tmplContent), 0o644); err != nil {
		t.Fatal(err)
	}
	roleContent := "role_name: default\ninstructions: |\n  test\n"
	if err := os.WriteFile(filepath.Join(h2Root, "roles", "default.yaml"), []byte(roleContent), 0o644); err != nil {
		t.Fatal(err)
	}

	origFork := forkDaemonFunc
	forkDaemonFunc = func(sessionDir string, hints session.TerminalHints, resume bool) error { return nil }
	t.Cleanup(func() { forkDaemonFunc = origFork })

	origIsTerminal := stdinIsTerminalFunc
	stdinIsTerminalFunc = func(fd int) bool { return true }
	t.Cleanup(func() { stdinIsTerminalFunc = origIsTerminal })

	t.Setenv("TERM_PROGRAM", "ghostty")

	origTileAttach := tileAttachFunc
	attached := ""
	tileAttachFunc = func(name string, dryRun bool) error {
		attached = name
		if dryRun {
			t.Fatal("unexpected dry-run tile attach")
		}
		return nil
	}
	t.Cleanup(func() { tileAttachFunc = origTileAttach })

	cmd := newPodLaunchCmd()
	cmd.SetArgs([]string{"devpod"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if attached != "dev-pod" {
		t.Fatalf("expected tile attach for pod dev-pod, got %q", attached)
	}
}

func TestPodLaunchCmd_AutoDetachesWithoutPTY(t *testing.T) {
	h2Root := setupPodTestEnv(t)

	tmplContent := `pod_name: dev-pod
agents:
  - name: worker
    role: default
`
	if err := os.WriteFile(filepath.Join(h2Root, "pods", "devpod.yaml"), []byte(tmplContent), 0o644); err != nil {
		t.Fatal(err)
	}
	roleContent := "role_name: default\ninstructions: |\n  test\n"
	if err := os.WriteFile(filepath.Join(h2Root, "roles", "default.yaml"), []byte(roleContent), 0o644); err != nil {
		t.Fatal(err)
	}

	origFork := forkDaemonFunc
	forkDaemonFunc = func(sessionDir string, hints session.TerminalHints, resume bool) error { return nil }
	t.Cleanup(func() { forkDaemonFunc = origFork })

	origIsTerminal := stdinIsTerminalFunc
	stdinIsTerminalFunc = func(fd int) bool { return false }
	t.Cleanup(func() { stdinIsTerminalFunc = origIsTerminal })

	origTileAttach := tileAttachFunc
	called := false
	tileAttachFunc = func(name string, dryRun bool) error {
		called = true
		return nil
	}
	t.Cleanup(func() { tileAttachFunc = origTileAttach })

	cmd := newPodLaunchCmd()
	cmd.SetArgs([]string{"devpod"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if called {
		t.Fatal("expected auto-detached pod launch to skip tile attach")
	}
}
