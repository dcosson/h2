package cmd

import (
	"os"
	"os/exec"
	"strings"
)

// resolveActor determines the current actor identity.
// Resolution priority:
//  1. H2_ACTOR env var (set automatically by h2 for child processes)
//  2. git config user.name
//  3. $USER env var
//  4. "unknown"
func resolveActor() string {
	if actor := os.Getenv("H2_ACTOR"); actor != "" {
		return actor
	}

	if out, err := exec.Command("git", "config", "user.name").Output(); err == nil {
		if name := strings.TrimSpace(string(out)); name != "" {
			return name
		}
	}

	if user := os.Getenv("USER"); user != "" {
		return user
	}

	return "unknown"
}
