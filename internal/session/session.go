package session

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"h2/internal/message"
)

const idleThreshold = 2 * time.Second

// State represents the current state of the session's child process.
type State int

const (
	StateActive State = iota // child running, recent output
	StateIdle                // child running, no output for 2+ seconds
	StateExited              // child process exited or hung
)

// String returns a human-readable name for the state.
func (s State) String() string {
	switch s {
	case StateActive:
		return "active"
	case StateIdle:
		return "idle"
	case StateExited:
		return "exited"
	default:
		return "unknown"
	}
}

// Session manages the message queue, delivery loop, and observable state
// for an h2 session. It coordinates between the overlay (UI), the virtual
// terminal (child process), and incoming messages.
type Session struct {
	Name      string
	Queue     *message.MessageQueue
	AgentName string
	PtyWriter io.Writer // writes to PTY under VT.Mu

	mu             sync.Mutex
	state          State
	stateChangedAt time.Time
	stateCh        chan struct{}

	outputNotify chan struct{} // buffered(1), signaled on child output
	otelNotify   chan struct{} // buffered(1), signaled on OTEL event
	exitNotify   chan struct{} // buffered(1), signaled on child exit

	// OTEL collector
	otelListener net.Listener
	otelServer   *http.Server
	otelPort     int

	stopCh chan struct{}

	// OnDeliver is called after each message delivery (e.g. to re-render UI).
	OnDeliver func()
}

// New creates a new Session with the given name and PTY writer.
func New(name string, ptyWriter io.Writer) *Session {
	return &Session{
		Name:           name,
		AgentName:      name,
		Queue:          message.NewMessageQueue(),
		PtyWriter:      ptyWriter,
		state:          StateActive,
		stateChangedAt: time.Now(),
		stateCh:        make(chan struct{}),
		outputNotify:   make(chan struct{}, 1),
		otelNotify:     make(chan struct{}, 1),
		exitNotify:     make(chan struct{}, 1),
		stopCh:         make(chan struct{}),
	}
}

// State returns the current session state.
func (s *Session) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// StateChanged returns a channel that is closed when the session state changes.
// Callers should re-check State() after receiving from this channel.
func (s *Session) StateChanged() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateCh
}

// WaitForState blocks until the session reaches the target state or ctx is cancelled.
// Returns true if the target state was reached, false if ctx was cancelled.
func (s *Session) WaitForState(ctx context.Context, target State) bool {
	for {
		s.mu.Lock()
		if s.state == target {
			s.mu.Unlock()
			return true
		}
		ch := s.stateCh
		s.mu.Unlock()

		select {
		case <-ch:
			continue
		case <-ctx.Done():
			return false
		}
	}
}

// setState updates the session state and notifies waiters.
func (s *Session) setState(newState State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == newState {
		return
	}
	s.state = newState
	s.stateChangedAt = time.Now()
	close(s.stateCh)
	s.stateCh = make(chan struct{})
}

// StateDuration returns how long the session has been in its current state.
func (s *Session) StateDuration() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Since(s.stateChangedAt)
}

// NoteOutput signals that the child process has produced output.
// Safe to call while holding VT.Mu — does only a non-blocking channel send.
func (s *Session) NoteOutput() {
	select {
	case s.outputNotify <- struct{}{}:
	default:
	}
}

// NoteExit signals that the child process has exited or hung.
// Safe to call while holding VT.Mu — does only a non-blocking channel send.
func (s *Session) NoteExit() {
	select {
	case s.exitNotify <- struct{}{}:
	default:
	}
}

// SubmitInput enqueues user-typed input for priority-aware delivery.
// The message has no FilePath (raw input), so deliver() will write Body directly.
func (s *Session) SubmitInput(text string, priority message.Priority) {
	msg := &message.Message{
		ID:        uuid.New().String(),
		From:      "user",
		Priority:  priority,
		Body:      text,
		Status:    message.StatusQueued,
		CreatedAt: time.Now(),
	}
	s.Queue.Enqueue(msg)
}

// Start launches the watchState and delivery goroutines. Blocks until Stop is called.
func (s *Session) Start() {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		s.watchState(s.stopCh)
	}()

	go func() {
		defer wg.Done()
		message.RunDelivery(message.DeliveryConfig{
			Queue:     s.Queue,
			AgentName: s.AgentName,
			PtyWriter: s.PtyWriter,
			IsIdle: func() bool {
				return s.State() == StateIdle
			},
			WaitForIdle: func(ctx context.Context) bool {
				return s.WaitForState(ctx, StateIdle)
			},
			OnDeliver: s.OnDeliver,
			Stop:      s.stopCh,
		})
	}()

	wg.Wait()
}

// Stop signals all goroutines to stop and cleans up resources.
func (s *Session) Stop() {
	select {
	case <-s.stopCh:
		// already stopped
	default:
		close(s.stopCh)
	}
	s.StopOtelCollector()
}

// noteActivity resets the idle timer and sets state to Active.
func (s *Session) noteActivity(idleTimer *time.Timer) {
	s.setState(StateActive)
	if !idleTimer.Stop() {
		select {
		case <-idleTimer.C:
		default:
		}
	}
	idleTimer.Reset(idleThreshold)
}

// watchState manages state transitions based on output, OTEL, and exit notifications.
func (s *Session) watchState(stop <-chan struct{}) {
	idleTimer := time.NewTimer(idleThreshold)
	defer idleTimer.Stop()

	for {
		select {
		case <-s.outputNotify:
			s.noteActivity(idleTimer)

		case <-s.otelNotify:
			s.noteActivity(idleTimer)

		case <-idleTimer.C:
			s.mu.Lock()
			if s.state != StateExited {
				s.mu.Unlock()
				s.setState(StateIdle)
			} else {
				s.mu.Unlock()
			}

		case <-s.exitNotify:
			s.setState(StateExited)

		case <-stop:
			return
		}
	}
}
