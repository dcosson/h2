package cmd

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"h2/internal/config"
	"h2/internal/session/message"
	"h2/internal/socketdir"
)

// mockHookAgent listens on a Unix socket and records received requests.
type mockHookAgent struct {
	listener net.Listener
	received []message.Request
	mu       sync.Mutex
	wg       sync.WaitGroup
}

func newMockHookAgent(t *testing.T, sockPath string) *mockHookAgent {
	t.Helper()
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	a := &mockHookAgent{listener: ln}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			a.wg.Add(1)
			go func() {
				defer a.wg.Done()
				defer conn.Close()
				req, err := message.ReadRequest(conn)
				if err != nil {
					return
				}
				a.mu.Lock()
				a.received = append(a.received, *req)
				a.mu.Unlock()
				message.SendResponse(conn, &message.Response{OK: true})
			}()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		a.wg.Wait()
	})
	return a
}

func (a *mockHookAgent) Received() []message.Request {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]message.Request(nil), a.received...)
}

// shortHookTempDir creates a temp directory with a short path for Unix sockets.
func shortHookTempDir(t *testing.T) string {
	t.Helper()
	name := t.Name()
	if len(name) > 20 {
		name = name[:20]
	}
	dir, err := os.MkdirTemp("/tmp", "h2h-"+name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// setupMockAgent creates the ~/.h2/sockets/ structure inside tmpDir and
// starts a mock agent socket. Returns the mock agent. Sets HOME to tmpDir.
func setupMockAgent(t *testing.T, tmpDir, agentName string) *mockHookAgent {
	t.Helper()

	// Reset caches so socketdir.Dir() and config.ConfigDir() pick up the new HOME.
	config.ResetResolveCache()
	socketdir.ResetDirCache()
	t.Cleanup(func() {
		config.ResetResolveCache()
		socketdir.ResetDirCache()
	})

	h2Root := filepath.Join(tmpDir, ".h2")
	sockDir := filepath.Join(h2Root, "sockets")
	os.MkdirAll(sockDir, 0o755)
	// Write marker file so ResolveDir finds this as a valid h2 dir.
	config.WriteMarker(h2Root)

	sockPath := filepath.Join(sockDir, socketdir.Format(socketdir.TypeAgent, agentName))
	t.Setenv("HOME", tmpDir)
	t.Setenv("H2_DIR", "")
	return newMockHookAgent(t, sockPath)
}

func TestHookCollect_SendsEventToAgent(t *testing.T) {
	tmpDir := shortHookTempDir(t)
	agent := setupMockAgent(t, tmpDir, "myagent")

	payload := `{"hook_event_name": "PreToolUse", "tool_name": "Bash", "session_id": "abc123"}`

	cmd := newHookCollectCmd()
	cmd.SetArgs([]string{"--agent", "myagent"})
	cmd.SetIn(bytes.NewBufferString(payload))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reqs := agent.Received()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	if reqs[0].Type != "hook_event" {
		t.Errorf("expected type=hook_event, got %q", reqs[0].Type)
	}
	if reqs[0].EventName != "PreToolUse" {
		t.Errorf("expected event_name=PreToolUse, got %q", reqs[0].EventName)
	}

	// Verify the full payload was forwarded.
	var payloadMap map[string]interface{}
	if err := json.Unmarshal(reqs[0].Payload, &payloadMap); err != nil {
		t.Fatalf("failed to parse forwarded payload: %v", err)
	}
	if payloadMap["tool_name"] != "Bash" {
		t.Errorf("expected tool_name=Bash in payload, got %v", payloadMap["tool_name"])
	}

	// Verify stdout output.
	if got := stdout.String(); got != "{}\n" {
		t.Errorf("expected stdout={}, got %q", got)
	}
}

func TestHookCollect_DefaultsAgentFromH2Actor(t *testing.T) {
	tmpDir := shortHookTempDir(t)
	agent := setupMockAgent(t, tmpDir, "concierge")
	t.Setenv("H2_ACTOR", "concierge")

	payload := `{"hook_event_name": "SessionStart"}`

	cmd := newHookCollectCmd()
	cmd.SetArgs([]string{}) // no --agent flag
	cmd.SetIn(bytes.NewBufferString(payload))
	cmd.SetOut(&bytes.Buffer{})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reqs := agent.Received()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	if reqs[0].EventName != "SessionStart" {
		t.Errorf("expected event_name=SessionStart, got %q", reqs[0].EventName)
	}
}

func TestHookCollect_ErrorNoAgent(t *testing.T) {
	t.Setenv("H2_ACTOR", "")

	cmd := newHookCollectCmd()
	cmd.SetArgs([]string{})
	cmd.SetIn(bytes.NewBufferString(`{"hook_event_name": "PreToolUse"}`))

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no agent specified")
	}
	if err.Error() != "--agent is required (or set H2_ACTOR)" {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestHookCollect_ErrorNoEventName(t *testing.T) {
	cmd := newHookCollectCmd()
	cmd.SetArgs([]string{"--agent", "test"})
	cmd.SetIn(bytes.NewBufferString(`{"some_field": "value"}`))

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when hook_event_name missing")
	}
	if err.Error() != "hook_event_name not found in payload" {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestHookCollect_ErrorInvalidJSON(t *testing.T) {
	cmd := newHookCollectCmd()
	cmd.SetArgs([]string{"--agent", "test"})
	cmd.SetIn(bytes.NewBufferString(`not json`))

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
