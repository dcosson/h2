package socketdir

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	TypeAgent  = "agent"
	TypeBridge = "bridge"
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

// Dir returns the socket directory: ~/.h2/sockets/
func Dir() string {
	return filepath.Join(os.Getenv("HOME"), ".h2", "sockets")
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
