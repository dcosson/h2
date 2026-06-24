package linearrelay

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

// persistedState is the on-disk shape of the relay's install state. It contains
// workspace OAuth tokens, so the file is written 0600.
type persistedState struct {
	Tokens   map[string]string `json:"tokens"`   // orgID -> oauth token
	Pairings map[string]string `json:"pairings"` // pairingToken -> orgID
}

// loadState populates tokens/pairings from statePath if present. A missing or
// corrupt file starts empty (best-effort).
func (s *Server) loadState() {
	if s.statePath == "" {
		return
	}
	data, err := os.ReadFile(s.statePath)
	if err != nil {
		return
	}
	var ps persistedState
	if err := json.Unmarshal(data, &ps); err != nil {
		log.Printf("relay: ignoring corrupt state file %s: %v", s.statePath, err)
		return
	}
	if ps.Tokens != nil {
		s.tokens = ps.Tokens
	}
	if ps.Pairings != nil {
		s.pairings = ps.Pairings
	}
	log.Printf("relay: loaded %d workspace(s) from %s", len(s.tokens), s.statePath)
}

// persistLocked atomically writes the current install state. The caller must
// hold s.mu. Errors are logged and dropped (the in-memory state remains valid).
func (s *Server) persistLocked() {
	if s.statePath == "" {
		return
	}
	data, err := json.MarshalIndent(persistedState{Tokens: s.tokens, Pairings: s.pairings}, "", "  ")
	if err != nil {
		log.Printf("relay: marshal state failed: %v", err)
		return
	}
	if dir := filepath.Dir(s.statePath); dir != "" {
		os.MkdirAll(dir, 0o700)
	}
	tmp := s.statePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		log.Printf("relay: write state failed: %v", err)
		return
	}
	if err := os.Rename(tmp, s.statePath); err != nil {
		log.Printf("relay: rename state failed: %v", err)
	}
}
