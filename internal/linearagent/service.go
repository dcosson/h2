package linearagent

import (
	"context"
	"log"
	"time"

	"h2/internal/linear"
)

// Reporter posts activities back to a Linear agent session. *linear.Client
// satisfies it.
type Reporter interface {
	CreateAgentActivity(ctx context.Context, sessionID string, a linear.AgentActivity) error
}

// SpawnRequest describes an agent to launch for a delegated issue.
type SpawnRequest struct {
	SessionID string // Linear agent session id (for correlation)
	Issue     Issue
	Prompt    string // natural-language context to seed the agent
}

// AgentHandle is a running agent working a delegated issue.
type AgentHandle interface {
	// Done is closed when the agent finishes its turn.
	Done() <-chan struct{}
	// Result returns a best-effort final summary; valid once Done is closed.
	Result() string
}

// AgentRunner spawns an h2 agent to work a delegated issue. The cmd layer wires
// the real implementation (session launch); tests provide a fake. Keeping it an
// interface here keeps this service free of the session/cmd packages.
type AgentRunner interface {
	Spawn(ctx context.Context, req SpawnRequest) (AgentHandle, error)
}

// ackTimeout bounds how long we allow the acknowledgement activity to take.
// Linear expects the first activity within ~10s of the created event.
const ackTimeout = 8 * time.Second

// Service consumes inbound agent-session events, acknowledges them, spawns an
// agent per delegated issue, and reports the result back to Linear.
type Service struct {
	src      Source
	reporter Reporter
	runner   AgentRunner
}

// New builds a Service.
func New(src Source, reporter Reporter, runner AgentRunner) *Service {
	return &Service{src: src, reporter: reporter, runner: runner}
}

// Run processes events until ctx is cancelled. Each event is handled in its own
// goroutine so a slow agent never blocks acknowledgement of the next event.
func (s *Service) Run(ctx context.Context) error {
	log.Printf("linear: agent service started")
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-s.src.Events():
			go s.handle(ctx, ev)
		}
	}
}

func (s *Service) handle(ctx context.Context, ev AgentSessionEvent) {
	switch ev.Action {
	case ActionCreated:
		s.handleCreated(ctx, ev)
	case ActionPrompted:
		// Follow-up prompts on an existing session are Phase 3 (interactivity).
		// Acknowledge so the human sees we received it.
		s.report(ctx, ev.AgentSession.ID, linear.AgentActivity{
			Type: linear.ActivityThought,
			Body: "Follow-up received (interactive replies coming soon).",
		})
	default:
		log.Printf("linear: ignoring agent-session action %q", ev.Action)
	}
}

func (s *Service) handleCreated(ctx context.Context, ev AgentSessionEvent) {
	sessID := ev.AgentSession.ID
	issueRef := ev.AgentSession.Issue.Identifier

	// 1. Acknowledge immediately (must be the first activity, within ~10s).
	ackCtx, cancel := context.WithTimeout(ctx, ackTimeout)
	s.report(ackCtx, sessID, linear.AgentActivity{
		Type: linear.ActivityThought,
		Body: "On it — starting work on " + issueRef + ".",
	})
	cancel()

	// 2. Spawn an agent to work the issue.
	h, err := s.runner.Spawn(ctx, SpawnRequest{
		SessionID: sessID,
		Issue:     ev.AgentSession.Issue,
		Prompt:    ev.PromptText(),
	})
	if err != nil {
		log.Printf("linear: spawn failed for %s: %v", issueRef, err)
		s.report(ctx, sessID, linear.AgentActivity{
			Type: linear.ActivityError,
			Body: "Failed to start an agent for this issue.",
		})
		return
	}

	// 3. Wait for the agent to finish, then post its result as the response.
	select {
	case <-ctx.Done():
		return
	case <-h.Done():
		body := h.Result()
		if body == "" {
			body = "Done."
		}
		s.report(ctx, sessID, linear.AgentActivity{
			Type: linear.ActivityResponse,
			Body: body,
		})
	}
}

// report posts an activity, logging and dropping any error. Linear problems
// must never crash the service.
func (s *Service) report(ctx context.Context, sessionID string, a linear.AgentActivity) {
	if err := s.reporter.CreateAgentActivity(ctx, sessionID, a); err != nil {
		log.Printf("linear: post %s activity to %s failed: %v", a.Type, sessionID, err)
	}
}
