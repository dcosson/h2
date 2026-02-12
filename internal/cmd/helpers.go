package cmd

import (
	"fmt"
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

// parseVarFlags parses --var key=value flags into a map.
// Each flag value is split on the first "=". Missing "=" is an error.
// Empty values (key=) are allowed.
func parseVarFlags(flags []string) (map[string]string, error) {
	vars := make(map[string]string, len(flags))
	for _, f := range flags {
		idx := strings.Index(f, "=")
		if idx < 0 {
			return nil, fmt.Errorf("invalid --var %q: must be key=value", f)
		}
		key := f[:idx]
		if key == "" {
			return nil, fmt.Errorf("invalid --var %q: key cannot be empty", f)
		}
		vars[key] = f[idx+1:]
	}
	return vars, nil
}
