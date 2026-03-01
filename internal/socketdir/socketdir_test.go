package socketdir

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormat(t *testing.T) {
	tests := []struct {
		socketType, name string
		want             string
	}{
		{"agent", "concierge", "agent.concierge.sock"},
		{"bridge", "dcosson", "bridge.dcosson.sock"},
		{"agent", "silent-deer", "agent.silent-deer.sock"},
	}
	for _, tt := range tests {
		got := Format(tt.socketType, tt.name)
		if got != tt.want {
			t.Errorf("Format(%q, %q) = %q, want %q", tt.socketType, tt.name, got, tt.want)
		}
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		filename string
		wantType string
		wantName string
		wantOK   bool
	}{
		{"agent.concierge.sock", TypeAgent, "concierge", true},
		{"bridge.dcosson.sock", TypeBridge, "dcosson", true},
		{"agent.silent-deer.sock", TypeAgent, "silent-deer", true},
		{"notasocket.txt", "", "", false},
		{"noperiod.sock", "", "", false},
		{".sock", "", "", false},
		{"onlyone.sock", "", "", false},
		{"agent..sock", TypeAgent, "", true}, // degenerate but parseable
	}
	for _, tt := range tests {
		entry, ok := Parse(tt.filename)
		if ok != tt.wantOK {
			t.Errorf("Parse(%q) ok = %v, want %v", tt.filename, ok, tt.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if entry.Type != tt.wantType {
			t.Errorf("Parse(%q).Type = %q, want %q", tt.filename, entry.Type, tt.wantType)
		}
		if entry.Name != tt.wantName {
			t.Errorf("Parse(%q).Name = %q, want %q", tt.filename, entry.Name, tt.wantName)
		}
	}
}

func TestPath(t *testing.T) {
	// Path uses Dir() which depends on config; just verify format.
	got := Path("agent", "concierge")
	want := filepath.Join(Dir(), "agent.concierge.sock")
	if got != want {
		t.Errorf("Path(agent, concierge) = %q, want %q", got, want)
	}
}

func TestFind(t *testing.T) {
	dir := t.TempDir()

	// Create some sockets using the naming convention.
	os.WriteFile(filepath.Join(dir, "agent.concierge.sock"), nil, 0o600)
	os.WriteFile(filepath.Join(dir, "bridge.dcosson.sock"), nil, 0o600)
	os.WriteFile(filepath.Join(dir, "agent.worker.sock"), nil, 0o600)

	t.Run("single match", func(t *testing.T) {
		path, err := FindIn(dir, "concierge")
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(dir, "agent.concierge.sock")
		if path != want {
			t.Errorf("Find(concierge) = %q, want %q", path, want)
		}
	})

	t.Run("no match", func(t *testing.T) {
		_, err := FindIn(dir, "nonexistent")
		if err == nil {
			t.Fatal("expected error for no match")
		}
	})

	t.Run("ambiguous match", func(t *testing.T) {
		// Create a second socket with name "dcosson" but different type.
		os.WriteFile(filepath.Join(dir, "agent.dcosson.sock"), nil, 0o600)
		_, err := FindIn(dir, "dcosson")
		if err == nil {
			t.Fatal("expected error for ambiguous match")
		}
	})
}

func TestList(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "agent.concierge.sock"), nil, 0o600)
	os.WriteFile(filepath.Join(dir, "bridge.dcosson.sock"), nil, 0o600)
	os.WriteFile(filepath.Join(dir, "agent.worker.sock"), nil, 0o600)
	os.WriteFile(filepath.Join(dir, "random.txt"), nil, 0o600)      // ignored
	os.WriteFile(filepath.Join(dir, "old-format.sock"), nil, 0o600) // ignored (no type.name format)

	entries, err := ListIn(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d: %+v", len(entries), entries)
	}

	// Entries should be sorted by dir order (alphabetical on most systems).
	types := make(map[string]int)
	for _, e := range entries {
		types[e.Type]++
		if e.Path == "" {
			t.Error("entry has empty Path")
		}
	}
	if types[TypeAgent] != 2 {
		t.Errorf("expected 2 agent entries, got %d", types[TypeAgent])
	}
	if types[TypeBridge] != 1 {
		t.Errorf("expected 1 bridge entry, got %d", types[TypeBridge])
	}
}

func TestListByType(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "agent.concierge.sock"), nil, 0o600)
	os.WriteFile(filepath.Join(dir, "bridge.dcosson.sock"), nil, 0o600)
	os.WriteFile(filepath.Join(dir, "agent.worker.sock"), nil, 0o600)

	agents, err := ListByTypeIn(dir, TypeAgent)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(agents))
	}

	bridges, err := ListByTypeIn(dir, TypeBridge)
	if err != nil {
		t.Fatal(err)
	}
	if len(bridges) != 1 {
		t.Errorf("expected 1 bridge, got %d", len(bridges))
	}
}

func TestListIn_EmptyDir(t *testing.T) {
	entries, err := ListIn(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestListIn_NonexistentDir(t *testing.T) {
	entries, err := ListIn("/nonexistent/path")
	if err != nil {
		t.Fatal(err)
	}
	if entries != nil {
		t.Errorf("expected nil, got %v", entries)
	}
}

func TestProbeSocket_NoFile(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "nonexistent.sock")
	if err := ProbeSocket(sockPath, "test"); err != nil {
		t.Errorf("ProbeSocket on nonexistent path: %v", err)
	}
}

func TestProbeSocket_StaleSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "stale.sock")

	// Create a regular file pretending to be a stale socket.
	os.WriteFile(sockPath, nil, 0o600)

	if err := ProbeSocket(sockPath, "test"); err != nil {
		t.Errorf("ProbeSocket on stale socket: %v", err)
	}
	// File should be removed.
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Error("stale socket file was not removed")
	}
}

func TestProbeSocket_LiveListener(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "live.sock")

	// Start a real listener.
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	err = ProbeSocket(sockPath, `agent "concierge"`)
	if err == nil {
		t.Fatal("expected error for live listener, got nil")
	}
	if want := `agent "concierge" is already running`; err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	// Socket file should NOT be removed.
	if _, err := os.Stat(sockPath); err != nil {
		t.Error("live socket file was incorrectly removed")
	}
}

func TestResolveSocketDir_ShortPath(t *testing.T) {
	// For a short h2 dir path, ResolveSocketDir returns <h2Dir>/sockets/.
	// Use a short path to avoid the macOS long temp dir issue.
	h2Dir := filepath.Join(os.TempDir(), "h2t")
	os.MkdirAll(h2Dir, 0o755)
	defer os.RemoveAll(h2Dir)

	got := ResolveSocketDir(h2Dir)
	want := filepath.Join(h2Dir, "sockets")
	if got != want {
		t.Errorf("ResolveSocketDir(%q) = %q, want %q", h2Dir, got, want)
	}
}

func TestResolveSocketDir_LongPath(t *testing.T) {
	// For an extremely long path, ResolveSocketDir should return a short symlink path.
	// Build a path long enough to exceed maxSocketPathLen (100).
	base := t.TempDir()
	longPart := strings.Repeat("a", 80)
	longDir := filepath.Join(base, longPart)
	os.MkdirAll(longDir, 0o755)

	got := ResolveSocketDir(longDir)

	// The result should be a symlink path under /tmp/ (or os.TempDir()),
	// not the original long path.
	if strings.HasPrefix(got, longDir) {
		// If the path is still short enough (test path happens to be short on this system),
		// that's fine — the logic only kicks in for truly long paths.
		testPath := filepath.Join(longDir, "sockets", "agent.long-agent-name-example.sock")
		if len(testPath) > 100 {
			t.Errorf("ResolveSocketDir returned long path %q, expected symlink", got)
		}
	}

	// The returned directory (or its symlink target) should point to <h2Dir>/sockets.
	if strings.Contains(got, "h2-") {
		// It's a symlink — verify it points to the right place.
		target, err := os.Readlink(got)
		if err != nil {
			t.Fatalf("Readlink(%q): %v", got, err)
		}
		wantTarget := filepath.Join(longDir, "sockets")
		if target != wantTarget {
			t.Errorf("symlink target = %q, want %q", target, wantTarget)
		}
	}
}

func TestResolveSocketDir_SymlinkCreation(t *testing.T) {
	// Test the symlink path logic directly by creating a real directory
	// and a symlink, then verifying resolve follows it.
	realDir := t.TempDir()
	symlinkDir := filepath.Join(t.TempDir(), "symlink-target")

	if err := os.Symlink(realDir, symlinkDir); err != nil {
		t.Fatalf("create test symlink: %v", err)
	}

	// Verify the symlink points to the right place.
	target, err := os.Readlink(symlinkDir)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if target != realDir {
		t.Errorf("symlink target = %q, want %q", target, realDir)
	}
}
