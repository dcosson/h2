package session

import (
	"os/exec"
	"time"

	"h2/internal/session/agent"
	"h2/internal/session/agent/monitor"
	"h2/internal/session/message"
)

// HeartbeatConfig holds the parameters for the heartbeat nudge goroutine.
type HeartbeatConfig struct {
	IdleTimeout time.Duration
	Message     string
	Condition   string // optional shell command; nudge only if exit code 0

	Agent     *agent.Agent
	Queue     *message.MessageQueue
	AgentName string
	Stop      <-chan struct{}
}

// RunHeartbeat monitors agent state and sends a nudge message when the agent
// has been idle for the configured duration. If a condition command is set,
// the nudge is only sent when the command exits 0.
func RunHeartbeat(cfg HeartbeatConfig) {
	for {
		// Wait for agent to become idle.
		if !waitForIdle(cfg.Agent, cfg.Stop) {
			return
		}

		// Start idle timer.
		timer := time.NewTimer(cfg.IdleTimeout)
		fired := waitForTimer(timer, cfg.Agent, cfg.Stop)
		if !fired {
			timer.Stop()
			continue // agent went active or stop signaled
		}

		// Timer fired and agent is still idle. Check condition if set.
		if cfg.Condition != "" {
			cmd := exec.Command("sh", "-c", cfg.Condition)
			if err := cmd.Run(); err != nil {
				// Condition not met — wait for next state change before retrying.
				select {
				case <-cfg.Agent.StateChanged():
					continue
				case <-cfg.Stop:
					return
				}
			}
		}

		// Send the nudge.
		message.PrepareMessage(cfg.Queue, cfg.AgentName, "h2-heartbeat", cfg.Message, message.PriorityIdle)
	}
}

// waitForIdle blocks until the agent is idle. Returns false if stop is signaled.
func waitForIdle(a *agent.Agent, stop <-chan struct{}) bool {
	for {
		if st, _ := a.State(); st == monitor.StateIdle {
			return true
		}
		select {
		case <-a.StateChanged():
			continue
		case <-stop:
			return false
		}
	}
}

// waitForTimer waits for the timer to fire while the agent remains idle.
// Returns true if the timer fired (and agent is still idle), false otherwise.
func waitForTimer(timer *time.Timer, a *agent.Agent, stop <-chan struct{}) bool {
	for {
		select {
		case <-timer.C:
			// Timer fired — verify agent is still idle.
			if st, _ := a.State(); st == monitor.StateIdle {
				return true
			}
			return false
		case <-a.StateChanged():
			// State changed — if no longer idle, cancel.
			if st, _ := a.State(); st != monitor.StateIdle {
				return false
			}
			// Still idle (e.g. active→idle transition); keep waiting.
		case <-stop:
			return false
		}
	}
}
