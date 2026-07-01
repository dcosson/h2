package session

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/google/uuid"

	"h2/internal/config"
	"h2/internal/session/agent/harness/claude"
	"h2/internal/socketdir"
)

// forkSuffixRe matches a trailing fork suffix like "-fork3" so that forking a
// fork produces siblings (fond-birch-fork2) instead of nested names
// (fond-birch-fork1-fork1).
var forkSuffixRe = regexp.MustCompile(`-fork\d+$`)

// GenerateForkName derives a fork name from a parent agent name by appending
// "-forkN" to the parent's base name (any existing fork suffix is stripped
// first). N starts at 1 and increments until taken(name) reports the name as
// available.
func GenerateForkName(parentName string, taken func(string) bool) string {
	base := forkSuffixRe.ReplaceAllString(parentName, "")
	for i := 1; ; i++ {
		name := fmt.Sprintf("%s-fork%d", base, i)
		if !taken(name) {
			return name
		}
	}
}

// forkNameTaken reports whether an agent name is already in use, either by an
// existing session directory or by a live/leftover agent socket.
func forkNameTaken(name string) bool {
	if _, err := os.Stat(config.SessionDir(name)); err == nil {
		return true
	}
	if _, err := os.Stat(socketdir.Path(socketdir.TypeAgent, name)); err == nil {
		return true
	}
	return false
}

// ForkSessionFiles clones a session's on-disk state into a new, independent
// session: the harness's native session log is copied under a new session id
// (with ids rewritten), and a new RuntimeConfig is written to a fresh session
// directory. The parent session is not modified and may be running.
//
// The forked session gets a new agent name derived from the parent
// (e.g. fond-birch -> fond-birch-fork1) and does NOT inherit the parent's pod
// membership. The returned RuntimeConfig has HarnessSessionID set to the new
// id, so launching its daemon with resume=true continues the copied
// conversation.
func ForkSessionFiles(parent *config.RuntimeConfig) (*config.RuntimeConfig, string, error) {
	if parent.HarnessType != "claude_code" {
		return nil, "", fmt.Errorf("fork is not supported for harness %q (only claude_code)", parent.HarnessType)
	}
	if parent.HarnessSessionID == "" {
		return nil, "", fmt.Errorf("session for agent %q has no harness_session_id; cannot fork", parent.AgentName)
	}

	// Locate the parent's native session log. Older sessions may not have
	// NativeLogPathSuffix persisted; recompute it (same as rotate does).
	oldSuffix := parent.NativeLogPathSuffix
	if oldSuffix == "" {
		oldSuffix = claude.NativeLogPathSuffix(parent.CWD, parent.HarnessSessionID)
	}
	configDir := parent.HarnessConfigDir()
	if configDir == "" || oldSuffix == "" {
		return nil, "", fmt.Errorf("session for agent %q has no harness config dir or log path; cannot fork", parent.AgentName)
	}
	oldLogPath := filepath.Join(configDir, oldSuffix)
	logData, err := os.ReadFile(oldLogPath)
	if err != nil {
		return nil, "", fmt.Errorf("read session log %s: %w", oldLogPath, err)
	}

	newName := GenerateForkName(parent.AgentName, forkNameTaken)
	newID := uuid.New().String()

	// Clone the config under the new identity. Pod membership is deliberately
	// not inherited — a fork is a standalone agent.
	rc := *parent
	rc.AgentName = newName
	rc.SessionID = newID
	rc.HarnessSessionID = newID
	rc.NativeLogPathSuffix = claude.NativeLogPathSuffix(rc.CWD, newID)
	rc.Pod = ""
	rc.PodIndex = 0
	rc.StartedAt = time.Now().UTC().Format(time.RFC3339)
	rc.ResumeSessionID = ""

	// Copy the session log under the new id, rewriting the session id inside.
	// Claude Code requires the sessionId fields in the log to match the id
	// being resumed; a global replace of the uuid is safe because uuids don't
	// collide with other content.
	newLogPath := filepath.Join(configDir, rc.NativeLogPathSuffix)
	if err := os.MkdirAll(filepath.Dir(newLogPath), 0o755); err != nil {
		return nil, "", fmt.Errorf("create log dir: %w", err)
	}
	newLogData := bytes.ReplaceAll(logData, []byte(parent.HarnessSessionID), []byte(newID))
	if err := os.WriteFile(newLogPath, newLogData, 0o644); err != nil {
		return nil, "", fmt.Errorf("write forked session log: %w", err)
	}

	// os.Mkdir (not MkdirAll) so a concurrent fork racing to the same name
	// fails loudly instead of silently sharing a session directory.
	sessionDir := config.SessionDir(newName)
	if err := os.Mkdir(sessionDir, 0o755); err != nil {
		os.Remove(newLogPath)
		return nil, "", fmt.Errorf("create session dir: %w", err)
	}
	if err := config.WriteRuntimeConfig(sessionDir, &rc); err != nil {
		os.Remove(newLogPath)
		return nil, "", fmt.Errorf("write forked runtime config: %w", err)
	}

	return &rc, sessionDir, nil
}

// ForkAndLaunch forks a session's files and starts a new daemon that resumes
// the copied conversation. Returns the new agent name.
func ForkAndLaunch(parent *config.RuntimeConfig, hints TerminalHints) (string, error) {
	rc, sessionDir, err := ForkSessionFiles(parent)
	if err != nil {
		return "", err
	}
	if err := ForkDaemon(sessionDir, hints, true); err != nil {
		return "", fmt.Errorf("launch forked daemon: %w", err)
	}
	return rc.AgentName, nil
}
