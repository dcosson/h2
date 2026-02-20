package session

import (
	"context"
	"testing"
	"time"

	"h2/internal/session/agent"
	"h2/internal/session/agent/monitor"
	"h2/internal/session/message"
)

// newTestAgent creates a minimal Agent with the output collector bridge
// started, ready for heartbeat testing.
func newTestAgent() *agent.Agent {
	a := agent.New(agent.ResolveAgentType("generic"))
	a.PrepareForLaunch("test", "")
	a.Start(context.Background())
	return a
}

func setFastIdleHeartbeat(t *testing.T) {
	t.Helper()
	old := monitor.IdleThreshold
	monitor.IdleThreshold = 10 * time.Millisecond
	t.Cleanup(func() { monitor.IdleThreshold = old })
}

func TestHeartbeat_NudgeAfterIdleTimeout(t *testing.T) {
	setFastIdleHeartbeat(t)
	a := newTestAgent()
	defer a.Stop()
	// Start the watchState goroutine so agent can transition to idle.
	// Agent already initialized in newTestAgent().
	q := message.NewMessageQueue()
	stop := make(chan struct{})

	go RunHeartbeat(HeartbeatConfig{
		IdleTimeout: 100 * time.Millisecond,
		Message:     "wake up",
		Agent:       a,
		Queue:       q,
		AgentName:   "test-agent",
		Stop:        stop,
	})

	// Wait for agent to become idle (10ms IdleThreshold) + heartbeat timeout (100ms) + buffer.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for heartbeat nudge")
		default:
		}
		if q.PendingCount() > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	msg := q.Dequeue(true, false)
	if msg == nil {
		t.Fatal("expected a message in the queue")
	}
	if msg.From != "h2-heartbeat" {
		t.Errorf("From = %q, want %q", msg.From, "h2-heartbeat")
	}
	if msg.Priority != message.PriorityIdle {
		t.Errorf("Priority = %v, want PriorityIdle", msg.Priority)
	}
	if msg.Body != "wake up" {
		t.Errorf("Body = %q, want %q", msg.Body, "wake up")
	}

	close(stop)
}

func TestHeartbeat_CancelledWhenAgentGoesActive(t *testing.T) {
	setFastIdleHeartbeat(t)
	a := newTestAgent()
	defer a.Stop()
	// Agent already initialized in newTestAgent().
	q := message.NewMessageQueue()
	stop := make(chan struct{})

	go RunHeartbeat(HeartbeatConfig{
		IdleTimeout: 500 * time.Millisecond,
		Message:     "should not arrive",
		Agent:       a,
		Queue:       q,
		AgentName:   "test-agent",
		Stop:        stop,
	})

	// Wait for agent to go idle.
	deadline := time.After(2 * time.Second)
	for st, _ := a.State(); st != monitor.StateIdle; st, _ = a.State() {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for idle")
		case <-a.StateChanged():
		}
	}

	// While the 500ms timer is running, make the agent active again and
	// keep it active by pumping output faster than IdleThreshold.
	time.Sleep(100 * time.Millisecond)
	stopOutput := make(chan struct{})
	go func() {
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				a.NoteOutput()
			case <-stopOutput:
				return
			}
		}
	}()

	// Wait past the original heartbeat timeout.
	time.Sleep(600 * time.Millisecond)
	close(stopOutput)

	if q.PendingCount() != 0 {
		t.Error("expected no messages; agent went active before timeout")
	}

	close(stop)
}

func TestHeartbeat_ConditionGates(t *testing.T) {
	setFastIdleHeartbeat(t)
	a := newTestAgent()
	defer a.Stop()
	// Agent already initialized in newTestAgent().
	q := message.NewMessageQueue()
	stop := make(chan struct{})

	// Use "false" as condition — should prevent nudge.
	go RunHeartbeat(HeartbeatConfig{
		IdleTimeout: 100 * time.Millisecond,
		Message:     "gated message",
		Condition:   "false",
		Agent:       a,
		Queue:       q,
		AgentName:   "test-agent",
		Stop:        stop,
	})

	// Wait for idle + timeout + buffer.
	deadline := time.After(2 * time.Second)
	for st, _ := a.State(); st != monitor.StateIdle; st, _ = a.State() {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for idle")
		case <-a.StateChanged():
		}
	}

	// Wait for the idle timeout to fire and condition to be checked.
	time.Sleep(200 * time.Millisecond)

	if q.PendingCount() != 0 {
		t.Error("expected no messages; condition 'false' should gate the nudge")
	}

	close(stop)
}

func TestHeartbeat_ConditionTrue(t *testing.T) {
	setFastIdleHeartbeat(t)
	a := newTestAgent()
	defer a.Stop()
	// Agent already initialized in newTestAgent().
	q := message.NewMessageQueue()
	stop := make(chan struct{})

	// Use "true" as condition — should allow nudge.
	go RunHeartbeat(HeartbeatConfig{
		IdleTimeout: 100 * time.Millisecond,
		Message:     "conditional nudge",
		Condition:   "true",
		Agent:       a,
		Queue:       q,
		AgentName:   "test-agent",
		Stop:        stop,
	})

	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for heartbeat nudge")
		default:
		}
		if q.PendingCount() > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	msg := q.Dequeue(true, false)
	if msg == nil {
		t.Fatal("expected a message")
	}
	if msg.Body != "conditional nudge" {
		t.Errorf("Body = %q, want %q", msg.Body, "conditional nudge")
	}

	close(stop)
}

func TestHeartbeat_StopTerminatesLoop(t *testing.T) {
	setFastIdleHeartbeat(t)
	a := newTestAgent()
	defer a.Stop()
	// Agent already initialized in newTestAgent().
	q := message.NewMessageQueue()
	stop := make(chan struct{})

	done := make(chan struct{})
	go func() {
		RunHeartbeat(HeartbeatConfig{
			IdleTimeout: 10 * time.Second, // long timeout
			Message:     "should not arrive",
			Agent:       a,
			Queue:       q,
			AgentName:   "test-agent",
			Stop:        stop,
		})
		close(done)
	}()

	// Close stop immediately.
	close(stop)

	select {
	case <-done:
		// Good — goroutine exited.
	case <-time.After(2 * time.Second):
		t.Fatal("RunHeartbeat did not exit after stop was closed")
	}

	if q.PendingCount() != 0 {
		t.Error("expected no messages after stop")
	}
}
