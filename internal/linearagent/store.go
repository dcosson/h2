package linearagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// FileStore is a JSON-file-backed SessionStore mapping Linear agent-session IDs
// to h2 agent names, so follow-up prompts can be routed to a still-running
// agent after the linear service restarts. It is safe for concurrent use.
type FileStore struct {
	path string
	mu   sync.Mutex
	m    map[string]string
}

// NewFileStore loads (or initializes) a store at path. A missing/corrupt file
// starts empty rather than erroring — the mapping is a best-effort cache.
func NewFileStore(path string) *FileStore {
	s := &FileStore{path: path, m: map[string]string{}}
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &s.m)
	}
	return s
}

func (s *FileStore) Put(sessionID, agentName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[sessionID] = agentName
	return s.flushLocked()
}

func (s *FileStore) Get(sessionID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.m[sessionID]
	return n, ok
}

func (s *FileStore) Delete(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, sessionID)
	return s.flushLocked()
}

// flushLocked atomically writes the map. Caller holds s.mu.
func (s *FileStore) flushLocked() error {
	data, err := json.MarshalIndent(s.m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
