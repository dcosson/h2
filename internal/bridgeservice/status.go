package bridgeservice

import (
	"fmt"
	"strings"
)

// conciergeRouting returns the routing explanation when a concierge is set.
func conciergeRouting(agentName string) string {
	return fmt.Sprintf("The concierge agent %s will reply to all messages.", agentName)
}

// noConciergeRouting returns the routing explanation when no concierge is set.
// firstAgent is the name of the first available agent, or empty if none.
func noConciergeRouting(firstAgent string) string {
	if firstAgent == "" {
		return "No agents are running to receive messages. Create agents with h2 run."
	}
	return fmt.Sprintf(
		"There is no concierge agent set, so messages will get routed to the last agent "+
			"that sent a message over this bridge. Your first message will go to %s, "+
			"the first agent in the list.", firstAgent)
}

// directMessagingHint returns the agent-prefix and reply instruction.
func directMessagingHint() string {
	return `You can message specific agents directly by prefixing your message with "<agent name>: " or by replying to their messages.`
}

// allowedCommandsHint returns the slash-command hint.
func allowedCommandsHint(commands []string) string {
	if len(commands) == 0 {
		return "The allowed commands that you can run directly with / are: (None are configured)."
	}
	return fmt.Sprintf("The allowed commands that you can run directly with / are: %s.",
		strings.Join(commands, ", "))
}
