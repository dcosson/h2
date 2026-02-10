package session

import (
	"os/exec"
	"strconv"
	"strings"
)

// parseGitDiffStats runs git diff --numstat (staged + unstaged) and returns
// the combined file count and line changes. Returns nil if git is not available
// or the CWD is not a git repo.
func parseGitDiffStats() *gitDiffStats {
	// Get unstaged changes.
	unstaged, err := exec.Command("git", "diff", "--numstat").Output()
	if err != nil {
		return nil
	}
	// Get staged changes.
	staged, err := exec.Command("git", "diff", "--cached", "--numstat").Output()
	if err != nil {
		return nil
	}

	files := make(map[string]bool)
	var added, removed int64

	for _, output := range [][]byte{unstaged, staged} {
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			if line == "" {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) < 3 {
				continue
			}
			// Binary files show "-" for add/remove counts.
			if parts[0] == "-" || parts[1] == "-" {
				files[parts[2]] = true
				continue
			}
			a, _ := strconv.ParseInt(parts[0], 10, 64)
			r, _ := strconv.ParseInt(parts[1], 10, 64)
			added += a
			removed += r
			files[parts[2]] = true
		}
	}

	if len(files) == 0 && added == 0 && removed == 0 {
		return nil
	}

	return &gitDiffStats{
		FilesChanged: len(files),
		LinesAdded:   added,
		LinesRemoved: removed,
	}
}
