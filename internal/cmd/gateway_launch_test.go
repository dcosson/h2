package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"h2/internal/config"
	"h2/internal/gateway"
	"h2/internal/session"
	"h2/internal/socketdir"
)

func TestLaunchAgentSessionDetachedUsesGateway(t *testing.T) {
	setupGatewayLaunchTestH2Dir(t)
	t.Setenv("H2_GATEWAY", "1")
	t.Setenv("OPENAI_API_KEY", "launch-secret")
	t.Setenv("CLAUDECODE", "parent-agent")

	sessionDir := filepath.Join(config.ConfigDir(), "sessions", "gateway-launch")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	rc := gatewayLaunchTestRC("gateway-launch")
	if err := config.WriteRuntimeConfig(sessionDir, rc); err != nil {
		t.Fatal(err)
	}

	var ensureCalled bool
	var startReq gateway.StartSessionRequest
	restore := stubGatewayLaunch(t)
	defer restore()
	gatewayEnsureRunningFunc = func(context.Context, gateway.EnsureOpts) (*gateway.Health, error) {
		ensureCalled = true
		return &gateway.Health{}, nil
	}
	gatewayStartSessionFunc = func(_ context.Context, _ string, req gateway.StartSessionRequest) (*gateway.SessionStatus, error) {
		startReq = req
		return &gateway.SessionStatus{}, nil
	}

	if err := launchAgentSession(sessionDir, rc, session.TerminalHints{}, false, true, map[string]string{"ROLE_ONLY": "role-value"}); err != nil {
		t.Fatalf("launchAgentSession: %v", err)
	}
	if !ensureCalled {
		t.Fatal("gateway ensure was not called")
	}
	if startReq.SessionDir != sessionDir {
		t.Fatalf("SessionDir = %q, want %q", startReq.SessionDir, sessionDir)
	}
	if startReq.LaunchEnv["OPENAI_API_KEY"] != "launch-secret" {
		t.Fatalf("OPENAI_API_KEY passthrough = %q", startReq.LaunchEnv["OPENAI_API_KEY"])
	}
	if _, ok := startReq.LaunchEnv["CLAUDECODE"]; ok {
		t.Fatal("CLAUDECODE should not be passed through")
	}
	if startReq.RoleEnv["ROLE_ONLY"] != "role-value" {
		t.Fatalf("role env = %+v", startReq.RoleEnv)
	}

	got, err := config.ReadRuntimeConfig(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.PassthroughEnvKeys) != 1 || got.PassthroughEnvKeys[0] != "OPENAI_API_KEY" {
		t.Fatalf("PassthroughEnvKeys = %v, want OPENAI_API_KEY", got.PassthroughEnvKeys)
	}
	if got.ResumeEnvWarning == "" {
		t.Fatal("expected resume env warning for launch-scoped OPENAI_API_KEY")
	}
}

func TestLaunchAgentSessionInteractiveUsesLegacyDaemon(t *testing.T) {
	setupGatewayLaunchTestH2Dir(t)

	sessionDir := filepath.Join(config.ConfigDir(), "sessions", "legacy-launch")
	rc := gatewayLaunchTestRC("legacy-launch")
	var forkCalled bool
	restore := stubGatewayLaunch(t)
	defer restore()
	forkDaemonFunc = func(sd string, hints session.TerminalHints, resume bool) error {
		forkCalled = true
		if sd != sessionDir {
			t.Fatalf("session dir = %q, want %q", sd, sessionDir)
		}
		return nil
	}

	if err := launchAgentSession(sessionDir, rc, session.TerminalHints{}, false, false, nil); err != nil {
		t.Fatalf("launchAgentSession: %v", err)
	}
	if !forkCalled {
		t.Fatal("forkDaemonFunc was not called")
	}
}

func TestLaunchAgentSessionGatewayDisabledUsesLegacyDaemon(t *testing.T) {
	setupGatewayLaunchTestH2Dir(t)

	sessionDir := filepath.Join(config.ConfigDir(), "sessions", "disabled-launch")
	rc := gatewayLaunchTestRC("disabled-launch")
	var forkCalled bool
	restore := stubGatewayLaunch(t)
	defer restore()
	forkDaemonFunc = func(string, session.TerminalHints, bool) error {
		forkCalled = true
		return nil
	}

	if err := launchAgentSession(sessionDir, rc, session.TerminalHints{}, false, true, nil); err != nil {
		t.Fatalf("launchAgentSession: %v", err)
	}
	if !forkCalled {
		t.Fatal("forkDaemonFunc was not called")
	}
}

func setupGatewayLaunchTestH2Dir(t *testing.T) {
	t.Helper()
	config.ResetResolveCache()
	t.Cleanup(config.ResetResolveCache)
	socketdir.ResetDirCache()
	t.Cleanup(socketdir.ResetDirCache)

	root := filepath.Join(t.TempDir(), ".h2")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteMarker(root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("H2_DIR", root)
}

func gatewayLaunchTestRC(name string) *config.RuntimeConfig {
	return &config.RuntimeConfig{
		AgentName:   name,
		SessionID:   name + "-session",
		HarnessType: "generic",
		Command:     "sh",
		CWD:         os.TempDir(),
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
	}
}

func stubGatewayLaunch(t *testing.T) func() {
	t.Helper()
	origFork := forkDaemonFunc
	origEnsure := gatewayEnsureRunningFunc
	origStart := gatewayStartSessionFunc
	origResume := gatewayResumeSessionFunc
	forkDaemonFunc = func(string, session.TerminalHints, bool) error {
		t.Fatal("unexpected forkDaemonFunc call")
		return nil
	}
	gatewayEnsureRunningFunc = func(context.Context, gateway.EnsureOpts) (*gateway.Health, error) {
		t.Fatal("unexpected gatewayEnsureRunningFunc call")
		return nil, nil
	}
	gatewayStartSessionFunc = func(context.Context, string, gateway.StartSessionRequest) (*gateway.SessionStatus, error) {
		t.Fatal("unexpected gatewayStartSessionFunc call")
		return nil, nil
	}
	gatewayResumeSessionFunc = func(context.Context, string, gateway.StartSessionRequest) (*gateway.SessionStatus, error) {
		t.Fatal("unexpected gatewayResumeSessionFunc call")
		return nil, nil
	}
	return func() {
		forkDaemonFunc = origFork
		gatewayEnsureRunningFunc = origEnsure
		gatewayStartSessionFunc = origStart
		gatewayResumeSessionFunc = origResume
	}
}
