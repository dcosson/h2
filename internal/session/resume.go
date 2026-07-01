package session

import (
	"fmt"
	"os"
	"time"

	"h2/internal/config"
	"h2/internal/session/agent/harness"
	"h2/internal/socketdir"
)

// ForkDaemonFunc matches ForkDaemon's signature so callers (and tests) can
// inject how the daemon process is launched.
type ForkDaemonFunc func(sessionDir string, hints TerminalHints, resume bool) error

// ResumeSession relaunches a stopped agent's daemon, resuming its previous
// conversation. It validates that the stored session supports resume, errors
// if the agent is already running (pruning a stale socket if one is left
// over), and starts a new daemon with the resume flag. forkDaemon may be nil
// to use ForkDaemon; tests inject a stub.
func ResumeSession(name string, hints TerminalHints, forkDaemon ForkDaemonFunc) error {
	if forkDaemon == nil {
		forkDaemon = ForkDaemon
	}

	sessionDir := config.SessionDir(name)
	rc, err := config.ReadRuntimeConfig(sessionDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no session found for agent %q", name)
		}
		return fmt.Errorf("session config for agent %q is invalid: %w", name, err)
	}

	sockPath := socketdir.Path(socketdir.TypeAgent, name)
	if err := socketdir.ProbeSocket(sockPath, fmt.Sprintf("agent %q", name)); err != nil {
		return err
	}

	h, err := harness.Resolve(rc, nil)
	if err != nil {
		return fmt.Errorf("resolve harness for resume: %w", err)
	}
	if !h.SupportsResume() {
		return fmt.Errorf("agent %q uses harness %q which does not support --resume", name, rc.HarnessType)
	}
	if rc.HarnessSessionID == "" {
		return fmt.Errorf("session config for agent %q has no harness_session_id; cannot resume", name)
	}
	if err := h.EnsureConfigDir(config.ConfigDir()); err != nil {
		return fmt.Errorf("ensure config dir: %w", err)
	}

	// Update started_at for the new daemon instance. If the fork fails,
	// restore the original so the metadata isn't left in a corrupted state.
	origStartedAt := rc.StartedAt
	rc.StartedAt = time.Now().UTC().Format(time.RFC3339)
	if err := config.WriteRuntimeConfig(sessionDir, rc); err != nil {
		return fmt.Errorf("write runtime config for resume: %w", err)
	}

	if err := forkDaemon(sessionDir, hints, true); err != nil {
		rc.StartedAt = origStartedAt
		_ = config.WriteRuntimeConfig(sessionDir, rc) // best-effort restore
		return err
	}
	return nil
}
