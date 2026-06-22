package linearagent

import (
	"path/filepath"
	"testing"
)

func TestFileStore_RoundTripAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "linear-sessions.json")

	s := NewFileStore(path)
	if err := s.Put("sess-1", "lin-1"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if n, ok := s.Get("sess-1"); !ok || n != "lin-1" {
		t.Fatalf("Get = %q %v", n, ok)
	}

	// A fresh store loads the persisted mapping from disk.
	s2 := NewFileStore(path)
	if n, ok := s2.Get("sess-1"); !ok || n != "lin-1" {
		t.Fatalf("reloaded Get = %q %v", n, ok)
	}

	if err := s2.Delete("sess-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := s2.Get("sess-1"); ok {
		t.Error("expected mapping deleted")
	}

	s3 := NewFileStore(path)
	if _, ok := s3.Get("sess-1"); ok {
		t.Error("delete did not persist")
	}
}

func TestFileStore_MissingFileStartsEmpty(t *testing.T) {
	s := NewFileStore(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if _, ok := s.Get("x"); ok {
		t.Error("expected empty store")
	}
}
