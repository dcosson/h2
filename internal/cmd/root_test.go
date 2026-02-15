package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2/internal/config"
)

func TestRootCmd_H2DIRValidation_InvalidDir(t *testing.T) {
	config.ResetResolveCache()
	t.Cleanup(config.ResetResolveCache)

	dir := t.TempDir() // no marker file
	t.Setenv("H2_DIR", dir)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"list"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid H2_DIR")
	}
	if !strings.Contains(err.Error(), "not an h2 directory") {
		t.Errorf("error = %q, want it to contain 'not an h2 directory'", err.Error())
	}
}

func TestRootCmd_H2DIRValidation_InitExempt(t *testing.T) {
	config.ResetResolveCache()
	t.Cleanup(config.ResetResolveCache)

	dir := t.TempDir() // no marker file
	t.Setenv("H2_DIR", dir)

	newDir := filepath.Join(t.TempDir(), "newh2")

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"init", newDir})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("init should be exempt from H2_DIR validation, got: %v", err)
	}

	// Verify the init actually worked.
	if _, err := os.Stat(filepath.Join(newDir, ".h2-dir.txt")); err != nil {
		t.Errorf("expected .h2-dir.txt to exist after init: %v", err)
	}
}

func TestRootCmd_H2DIRValidation_VersionExempt(t *testing.T) {
	config.ResetResolveCache()
	t.Cleanup(config.ResetResolveCache)

	dir := t.TempDir() // no marker file
	t.Setenv("H2_DIR", dir)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"version"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("version should be exempt from H2_DIR validation, got: %v", err)
	}
}
