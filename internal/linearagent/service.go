package linearagent

import (
	"context"
	"log"
	"sync"
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
	// Name is the h2 agent name backing this session.
	Name() string
	// Done is closed when the agent finishes its turn.
	Done() <-chan struct{}
	// Activities streams activities (thought/action/elicitation) produced while
	// the agent works. The service forwards each to Linear. Closed when the
	// agent's turn ends; ranging over it is safe.
	Activities() <-chan linear.AgentActivity
	// Result returns a best-effort final summary; valid once Done is closed.
	Result() string
	// Deliver routes a follow-up prompt to the running agent.
	Deliver(ctx context.Context, text string) error
}

// AgentRunner spawns and locates h2 agents for delegated issues. The cmd layer
// wires the real implementation (session launch); tests provide a fake. Keeping
// it an interface here keeps this service free of the session/cmd packages.
type AgentRunner interface {
	// Spawn launches an agent to work a delegated issue.
	Spawn(ctx context.Context, req SpawnRequest) (AgentHandle, error)
	// DeliverTo routes a follow-up prompt to an already-running agent by name.
	// Used to resume sessions whose in-memory handle was lost (e.g. after a
	// restart of the linear service). Returns an error if the agent is gone.
	DeliverTo(ctx context.Context, agentName, text string) error
}

// ackTimeout bounds how long we allow the acknowledgement activity to take.
// Linear expects the first activity within ~10s of the created event.
const ackTimeout = 8 * time.Second

// SessionStore persists the sessionID -> agent-name mapping so follow-up
// prompts can be routed to a still-running agent even after the linear service
// restarts. A nil store disables persistence (in-memory only).
type SessionStore interface {
	Put(sessionID, agentName string) error
	Get(sessionID string) (agentName string, ok bool)
	Delete(sessionID string) error
}

// Service consumes inbound agent-session events, acknowledges them, spawns an
// agent per delegated issue, streams activities, and reports the result back to
// Linear.
type Service struct {
	src      Source
	reporter Reporter
	runner   AgentRunner
	store    SessionStore

	mu     sync.Mutex
	active map[string]AgentHandle // sessionID -> handle (in-memory, this process)
}

// New builds a Service. store may be nil to disable cross-restart persistence.
func New(src Source, reporter Reporter, runner AgentRunner, store SessionStore) *Service {
	return &Service{
		src:      src,
		reporter: reporter,
		runner:   runner,
		store:    store,
		active:   make(map[string]AgentHandle),
	}
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
		s.handlePrompted(ctx, ev)
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

	// Register the session so follow-ups can be routed to this agent.
	s.register(sessID, h)
	defer s.unregister(sessID)

	// 3. Stream activities until the agent's turn ends, then post the response.
	for {
		select {
		case <-ctx.Done():
			return
		case a, ok := <-h.Activities():
			if !ok {
				// Activity channel closed; fall through to await Done below.
				s.awaitAndRespond(ctx, sessID, h)
				return
			}
			s.report(ctx, sessID, a)
		case <-h.Done():
			s.drain(ctx, sessID, h)
			s.respond(ctx, sessID, h)
			return
		}
	}
}

// handlePrompted routes a human follow-up to the running agent. It resolves the
// agent via the in-memory handle first, then the persistent store (to survive a
// linear-service restart).
func (s *Service) handlePrompted(ctx context.Context, ev AgentSessionEvent) {
	sessID := ev.AgentSession.ID
	prompt := ev.PromptText()

	if h := s.lookup(sessID); h != nil {
		if err := h.Deliver(ctx, prompt); err != nil {
			log.Printf("linear: deliver follow-up to %s failed: %v", sessID, err)
		}
		s.report(ctx, sessID, linear.AgentActivity{Type: linear.ActivityThought, Body: "Got it — continuing."})
		return
	}
	if s.store != nil {
		if name, ok := s.store.Get(sessID); ok {
			if err := s.runner.DeliverTo(ctx, name, prompt); err != nil {
				log.Printf("linear: deliver follow-up to agent %q failed: %v", name, err)
				s.report(ctx, sessID, linear.AgentActivity{Type: linear.ActivityError, Body: "That session's agent is no longer available."})
				s.store.Delete(sessID)
				return
			}
			s.report(ctx, sessID, linear.AgentActivity{Type: linear.ActivityThought, Body: "Got it — continuing."})
			return
		}
	}
	s.report(ctx, sessID, linear.AgentActivity{Type: linear.ActivityError, Body: "No active agent for this session."})
}

// drain flushes any activities already queued before Done fired.
func (s *Service) drain(ctx context.Context, sessID string, h AgentHandle) {
	for {
		select {
		case a, ok := <-h.Activities():
			if !ok {
				return
			}
			s.report(ctx, sessID, a)
		default:
			return
		}
	}
}

// awaitAndRespond waits for Done after the activity channel closed, then posts.
func (s *Service) awaitAndRespond(ctx context.Context, sessID string, h AgentHandle) {
	select {
	case <-ctx.Done():
		return
	case <-h.Done():
		s.respond(ctx, sessID, h)
	}
}

func (s *Service) respond(ctx context.Context, sessID string, h AgentHandle) {
	body := h.Result()
	if body == "" {
		body = "Done."
	}
	s.report(ctx, sessID, linear.AgentActivity{Type: linear.ActivityResponse, Body: body})
}

func (s *Service) register(sessID string, h AgentHandle) {
	s.mu.Lock()
	s.active[sessID] = h
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.Put(sessID, h.Name()); err != nil {
			log.Printf("linear: persist session %s failed: %v", sessID, err)
		}
	}
}

func (s *Service) unregister(sessID string) {
	s.mu.Lock()
	delete(s.active, sessID)
	s.mu.Unlock()
	if s.store != nil {
		s.store.Delete(sessID)
	}
}

func (s *Service) lookup(sessID string) AgentHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[sessID]
}

// report posts an activity, logging and dropping any error. Linear problems
// must never crash the service.
func (s *Service) report(ctx context.Context, sessionID string, a linear.AgentActivity) {
	if err := s.reporter.CreateAgentActivity(ctx, sessionID, a); err != nil {
		log.Printf("linear: post %s activity to %s failed: %v", a.Type, sessionID, err)
	}
}
