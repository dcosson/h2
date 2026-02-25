package cmd

import (
	"fmt"
	"strings"

	"h2/internal/socketdir"
)

// agentConnError returns an error for a failed agent connection that includes
// the list of available agents.
func agentConnError(name string, err error) error {
	agents, listErr := socketdir.ListByType(socketdir.TypeAgent)
	if listErr != nil || len(agents) == 0 {
		return fmt.Errorf("cannot connect to agent %q (no running agents)\n\nStart one with: h2 run --name <name> <command>", name)
	}
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name
	}
	return fmt.Errorf("cannot connect to agent %q\n\nAvailable agents: %s", name, strings.Join(names, ", "))
}
