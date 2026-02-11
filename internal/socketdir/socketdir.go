package socketdir

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"h2/internal/config"
)

const (
	TypeAgent  = "agent"
	TypeBridge = "bridge"

	// maxSocketPathLen is the conservative limit for Unix domain socket paths.
	// macOS has sizeof(sockaddr_un.sun_path) = 104.
	// We use 100 to leave room for the socket filename.
	maxSocketPathLen = 100
)

// Entry represents a parsed socket file in the socket directory.
type Entry struct {
	Type string // "agent", "bridge"
	Name string // "concierge", "dcosson"
	Path string // full path to .sock file
}

// Format returns the socket filename for a given type and name: "agent.concierge.sock".
func Format(socketType, name string) string {
	return socketType + "." + name + ".sock"
}

// Parse extracts type and name from a socket filename like "agent.concierge.sock".
// Returns false if the filename doesn't match the expected format.
func Parse(filename string) (Entry, bool) {
	if !strings.HasSuffix(filename, ".sock") {
		return Entry{}, false
	}
	base := strings.TrimSuffix(filename, ".sock")
	dot := strings.IndexByte(base, '.')
	if dot < 1 {
		return Entry{}, false
	}
	return Entry{
		Type: base[:dot],
		Name: base[dot+1:],
	}, true
}

var (
	socketDir     string
	socketDirOnce sync.Once
)

// Dir returns the socket directory, derived from the resolved h2 dir.
// If the resulting path would be too long for Unix domain sockets,
// a symlink from /tmp/h2-<hash>/ is created and returned instead.
func Dir() string {
	socketDirOnce.Do(func() {
		socketDir = resolveSocketDir()
	})
	return socketDir
}

// ResetDirCache resets the cached Dir result. For testing only.
func ResetDirCache() {
	socketDirOnce = sync.Once{}
	socketDir = ""
}

func resolveSocketDir() string {
	realDir := filepath.Join(config.ConfigDir(), "sockets")

	// Check if a typical socket path would exceed the limit.
	// Use a representative long socket name to test.
	testPath := filepath.Join(realDir, "agent.long-agent-name-example.sock")
	if len(testPath) <= maxSocketPathLen {
		return realDir
	}

	// Path too long — create a symlink from /tmp/h2-<hash>/
	hash := sha256.Sum256([]byte(realDir))
	shortDir := filepath.Join(os.TempDir(), fmt.Sprintf("h2-%x", hash[:8]))

	// Check if the symlink already exists and points to the right place.
	if target, err := os.Readlink(shortDir); err == nil && target == realDir {
		return shortDir
	}

	// Ensure the real socket directory exists.
	os.MkdirAll(realDir, 0o755)

	// Remove any stale entry and create the symlink.
	os.Remove(shortDir)
	if err := os.Symlink(realDir, shortDir); err != nil {
		// If symlink creation fails, fall back to the real dir.
		return realDir
	}
	return shortDir
}

// Path returns the full socket path for a given type and name.
func Path(socketType, name string) string {
	return filepath.Join(Dir(), Format(socketType, name))
}

// Find globs for *.{name}.sock in the default socket directory
// and returns the full path. Returns an error if zero or more than one match.
func Find(name string) (string, error) {
	return FindIn(Dir(), name)
}

// FindIn globs for *.{name}.sock in the given directory.
func FindIn(dir, name string) (string, error) {
	pattern := filepath.Join(dir, "*."+name+".sock")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no socket found for %q", name)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous name %q: %d sockets match", name, len(matches))
	}
}

// List returns all parsed socket entries from the default directory.
func List() ([]Entry, error) {
	return ListIn(Dir())
}

// ListIn returns all parsed socket entries from the given directory.
func ListIn(dir string) ([]Entry, error) {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var entries []Entry
	for _, de := range dirEntries {
		entry, ok := Parse(de.Name())
		if !ok {
			continue
		}
		entry.Path = filepath.Join(dir, de.Name())
		entries = append(entries, entry)
	}
	return entries, nil
}

// ListByType returns entries matching a specific type from the default directory.
func ListByType(socketType string) ([]Entry, error) {
	return ListByTypeIn(Dir(), socketType)
}

// ListByTypeIn returns entries matching a specific type from the given directory.
func ListByTypeIn(dir, socketType string) ([]Entry, error) {
	all, err := ListIn(dir)
	if err != nil {
		return nil, err
	}
	var filtered []Entry
	for _, e := range all {
		if e.Type == socketType {
			filtered = append(filtered, e)
		}
	}
	return filtered, nil
}
