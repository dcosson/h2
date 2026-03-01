package cmd

import (
	"bytes"
	"strings"
	"testing"

	"h2/internal/version"
)

func TestVersionCmd(t *testing.T) {
	cmd := newVersionCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	got := strings.TrimSpace(buf.String())
	if got != version.DisplayVersion() {
		t.Errorf("version command output = %q, want %q", got, version.DisplayVersion())
	}
}
