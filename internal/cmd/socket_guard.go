package cmd

import (
	"fmt"

	"h2/internal/socketdir"
)

// ensureAgentSocketAvailable verifies that the agent's socket path is usable.
// It removes stale socket files and errors if a live agent is already running.
func ensureAgentSocketAvailable(name string) error {
	if name == "" {
		return nil
	}
	sockPath := socketdir.Path(socketdir.TypeAgent, name)
	if err := socketdir.ProbeSocket(sockPath, fmt.Sprintf("agent %q", name)); err != nil {
		return err
	}
	return nil
}
