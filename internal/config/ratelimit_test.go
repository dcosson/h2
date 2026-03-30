package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteAndReadRateLimit(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)
	resetsAt := now.Add(1 * time.Hour)

	info := &RateLimitInfo{
		ResetsAt:   resetsAt,
		Message:    "You've hit your limit · resets 12pm",
		RecordedAt: now,
		AgentName:  "coder",
	}

	if err := WriteRateLimit(dir, info); err != nil {
		t.Fatal(err)
	}

	got, err := ReadRateLimit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected non-nil RateLimitInfo")
	}
	if !got.ResetsAt.Equal(resetsAt) {
		t.Errorf("ResetsAt = %v, want %v", got.ResetsAt, resetsAt)
	}
	if got.Message != info.Message {
		t.Errorf("Message = %q, want %q", got.Message, info.Message)
	}
	if got.AgentName != "coder" {
		t.Errorf("AgentName = %q, want %q", got.AgentName, "coder")
	}
}

func TestReadRateLimit_NotFound(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadRateLimit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for missing file, got %+v", got)
	}
}

func TestIsProfileRateLimited_Active(t *testing.T) {
	dir := t.TempDir()
	info := &RateLimitInfo{
		ResetsAt:   time.Now().Add(1 * time.Hour),
		RecordedAt: time.Now(),
	}
	if err := WriteRateLimit(dir, info); err != nil {
		t.Fatal(err)
	}

	got := IsProfileRateLimited(dir)
	if got == nil {
		t.Fatal("expected rate limit to be active")
	}
}

func TestIsProfileRateLimited_Expired(t *testing.T) {
	dir := t.TempDir()
	info := &RateLimitInfo{
		ResetsAt:   time.Now().Add(-1 * time.Hour),
		RecordedAt: time.Now().Add(-2 * time.Hour),
	}
	if err := WriteRateLimit(dir, info); err != nil {
		t.Fatal(err)
	}

	got := IsProfileRateLimited(dir)
	if got != nil {
		t.Errorf("expected nil for expired rate limit, got %+v", got)
	}

	// The expired file should have been cleaned up.
	if _, err := os.Stat(filepath.Join(dir, RateLimitFileName)); !os.IsNotExist(err) {
		t.Fatal("expired ratelimit.json should be removed after IsProfileRateLimited")
	}
}

func TestIsProfileRateLimited_NoFile(t *testing.T) {
	dir := t.TempDir()
	got := IsProfileRateLimited(dir)
	if got != nil {
		t.Errorf("expected nil for missing file, got %+v", got)
	}
}

func TestClearRateLimit(t *testing.T) {
	dir := t.TempDir()
	info := &RateLimitInfo{
		ResetsAt:   time.Now().Add(1 * time.Hour),
		RecordedAt: time.Now(),
	}
	if err := WriteRateLimit(dir, info); err != nil {
		t.Fatal(err)
	}

	// Verify file exists.
	if _, err := os.Stat(filepath.Join(dir, RateLimitFileName)); err != nil {
		t.Fatal("ratelimit.json should exist")
	}

	if err := ClearRateLimit(dir); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, RateLimitFileName)); !os.IsNotExist(err) {
		t.Fatal("ratelimit.json should be removed")
	}

	// Clearing again should not error.
	if err := ClearRateLimit(dir); err != nil {
		t.Fatal(err)
	}
}
