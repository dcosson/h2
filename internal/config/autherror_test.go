package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteReadAuthError(t *testing.T) {
	dir := t.TempDir()
	info := &AuthErrorInfo{
		Message:    "OAuth token has expired",
		RecordedAt: time.Now().Truncate(time.Millisecond),
		AgentName:  "test-agent",
	}

	if err := WriteAuthError(dir, info); err != nil {
		t.Fatalf("WriteAuthError: %v", err)
	}

	got, err := ReadAuthError(dir)
	if err != nil {
		t.Fatalf("ReadAuthError: %v", err)
	}
	if got == nil {
		t.Fatal("ReadAuthError returned nil")
	}
	if got.Message != info.Message {
		t.Errorf("Message = %q, want %q", got.Message, info.Message)
	}
	if got.AgentName != info.AgentName {
		t.Errorf("AgentName = %q, want %q", got.AgentName, info.AgentName)
	}
}

func TestReadAuthError_NotExist(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadAuthError(dir)
	if err != nil {
		t.Fatalf("ReadAuthError: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing file, got %+v", got)
	}
}

func TestIsProfileAuthError(t *testing.T) {
	dir := t.TempDir()

	// No file — not an auth error.
	if got := IsProfileAuthError(dir); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}

	// Write an auth error.
	info := &AuthErrorInfo{
		Message:    "authentication_error",
		RecordedAt: time.Now(),
		AgentName:  "test",
	}
	if err := WriteAuthError(dir, info); err != nil {
		t.Fatalf("WriteAuthError: %v", err)
	}

	// Should return the info.
	got := IsProfileAuthError(dir)
	if got == nil {
		t.Fatal("expected auth error info, got nil")
	}
	if got.Message != info.Message {
		t.Errorf("Message = %q, want %q", got.Message, info.Message)
	}
}

func TestClearAuthError(t *testing.T) {
	dir := t.TempDir()

	// Clear when no file — should not error.
	if err := ClearAuthError(dir); err != nil {
		t.Fatalf("ClearAuthError (no file): %v", err)
	}

	// Write and clear.
	info := &AuthErrorInfo{Message: "test", RecordedAt: time.Now()}
	if err := WriteAuthError(dir, info); err != nil {
		t.Fatalf("WriteAuthError: %v", err)
	}
	if err := ClearAuthError(dir); err != nil {
		t.Fatalf("ClearAuthError: %v", err)
	}

	// File should be gone.
	if _, err := os.Stat(filepath.Join(dir, AuthErrorFileName)); !os.IsNotExist(err) {
		t.Errorf("expected file to be removed, stat error: %v", err)
	}
}
