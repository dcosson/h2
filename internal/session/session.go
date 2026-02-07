package session

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/vito/midterm"
	"golang.org/x/term"

	"h2/internal/session/agent"
	"h2/internal/session/client"
	"h2/internal/session/message"
	"h2/internal/session/virtualterminal"
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

// Session manages the message queue, delivery loop, observable state,
// child process lifecycle, and client connections for an h2 session.
type Session struct {
	Name      string
	Command   string
	Args      []string
	Queue     *message.MessageQueue
	AgentName string
	Agent     *agent.Agent
	VT        *virtualterminal.VT
	Client    *client.Client // primary/interactive client (nil in daemon-only)
	Clients   []*client.Client
	clientsMu sync.Mutex

	// ExtraEnv holds additional environment variables to pass to the child process.
	ExtraEnv map[string]string

	// Daemon holds the networking/attach layer (nil in interactive mode).
	Daemon    *Daemon
	StartTime time.Time

	// Quit is set when the user explicitly chooses to quit.
	Quit bool

	mu             sync.Mutex
	state          State
	stateChangedAt time.Time
	stateCh        chan struct{}

	outputNotify chan struct{} // buffered(1), signaled on child output
	exitNotify   chan struct{} // buffered(1), signaled on child exit

	stopCh     chan struct{}
	relaunchCh chan struct{}
	quitCh     chan struct{}

	// OnDeliver is called after each message delivery (e.g. to re-render UI).
	OnDeliver func()
}

// New creates a new Session with the given name and command.
func New(name string, command string, args []string) *Session {
	return &Session{
		Name:           name,
		Command:        command,
		Args:           args,
		AgentName:      name,
		Queue:          message.NewMessageQueue(),
		Agent:          agent.New(agent.NewClaudeCodeHelper()),
		state:          StateActive,
		stateChangedAt: time.Now(),
		stateCh:        make(chan struct{}),
		outputNotify:   make(chan struct{}, 1),
		exitNotify:     make(chan struct{}, 1),
		stopCh:         make(chan struct{}),
		relaunchCh:     make(chan struct{}, 1),
		quitCh:         make(chan struct{}, 1),
	}
}

// PtyWriter returns a writer that writes to the child PTY under VT.Mu.
func (s *Session) PtyWriter() io.Writer {
	return &sessionPtyWriter{s: s}
}

// sessionPtyWriter writes to the child PTY while holding the VT mutex.
type sessionPtyWriter struct {
	s *Session
}

func (pw *sessionPtyWriter) Write(p []byte) (int, error) {
	pw.s.VT.Mu.Lock()
	defer pw.s.VT.Mu.Unlock()
	if pw.s.VT.ChildExited || pw.s.VT.ChildHung {
		return 0, io.ErrClosedPipe
	}
	n, err := pw.s.VT.WritePTY(p, 3*time.Second)
	if err == virtualterminal.ErrPTYWriteTimeout {
		pw.s.VT.ChildHung = true
		pw.s.VT.KillChild()
		pw.s.ForEachClient(func(cl *client.Client) {
			cl.RenderBar()
		})
		return 0, io.ErrClosedPipe
	}
	return n, err
}

// initVT creates and initializes the VT with default dimensions for daemon mode.
func (s *Session) initVT(rows, cols int) {
	s.VT = &virtualterminal.VT{}
	s.VT.Rows = rows
	s.VT.Cols = cols
}

// NewClient creates a new Client with all session callbacks wired.
func (s *Session) NewClient() *client.Client {
	cl := &client.Client{
		VT:        s.VT,
		AgentName: s.Name,
	}
	cl.InitClient()

	// Wire lifecycle callbacks.
	cl.OnRelaunch = func() {
		select {
		case s.relaunchCh <- struct{}{}:
		default:
		}
	}
	cl.OnQuit = func() {
		s.Quit = true
		select {
		case s.quitCh <- struct{}{}:
		default:
		}
	}
	cl.OnModeChange = func(mode client.InputMode) {
		if mode == client.ModePassthrough {
			s.Queue.Pause()
		} else {
			s.Queue.Unpause()
		}
	}
	cl.QueueStatus = func() (int, bool) {
		return s.Queue.PendingCount(), s.Queue.IsPaused()
	}
	cl.OtelMetrics = func() (int64, float64, bool, int) {
		m := s.Agent.Metrics()
		return m.TotalTokens, m.TotalCostUSD, m.EventsReceived, s.Agent.OtelPort()
	}
	cl.OnSubmit = func(text string, pri message.Priority) {
		s.SubmitInput(text, pri)
	}
	return cl
}

// AddClient adds a client to the session's client list.
func (s *Session) AddClient(cl *client.Client) {
	s.clientsMu.Lock()
	s.Clients = append(s.Clients, cl)
	s.clientsMu.Unlock()
}

// RemoveClient removes a client from the session's client list.
func (s *Session) RemoveClient(cl *client.Client) {
	s.clientsMu.Lock()
	for i, c := range s.Clients {
		if c == cl {
			s.Clients = append(s.Clients[:i], s.Clients[i+1:]...)
			break
		}
	}
	s.clientsMu.Unlock()
}

// ForEachClient calls fn for each connected client while holding the clients lock.
// fn is called with VT.Mu already held by the caller.
func (s *Session) ForEachClient(fn func(cl *client.Client)) {
	s.clientsMu.Lock()
	clients := make([]*client.Client, len(s.Clients))
	copy(clients, s.Clients)
	s.clientsMu.Unlock()
	for _, cl := range clients {
		fn(cl)
	}
}

// pipeOutputCallback returns the callback for VT.PipeOutput that renders
// all connected clients. Called with VT.Mu held.
func (s *Session) pipeOutputCallback() func() {
	return func() {
		// NoteOutput for the session (only need to call once).
		s.NoteOutput()
		s.ForEachClient(func(cl *client.Client) {
			if cl.Mode != client.ModeScroll {
				cl.RenderScreen()
				cl.RenderBar()
			}
		})
	}
}

// RunDaemon runs the session in daemon mode: creates VT, client, PTY,
// starts OTEL, socket listener, and manages the child process lifecycle.
// Blocks until the child exits and the user quits.
func (s *Session) RunDaemon() error {
	// Initialize VT with default daemon dimensions.
	s.initVT(24, 80)
	s.VT.ChildRows = s.VT.Rows - 2 // default ReservedRows
	s.VT.Vt = midterm.NewTerminal(s.VT.ChildRows, s.VT.Cols)
	s.VT.Scrollback = midterm.NewTerminal(s.VT.ChildRows, s.VT.Cols)
	s.VT.Scrollback.AutoResizeY = true
	s.VT.Scrollback.AppendOnly = true
	s.VT.LastOut = time.Now()
	s.VT.Output = io.Discard

	// Initialize client and wire callbacks.
	s.Client = s.NewClient()
	s.AddClient(s.Client)

	// Set up delivery callback.
	s.OnDeliver = func() {
		s.VT.Mu.Lock()
		s.ForEachClient(func(cl *client.Client) {
			cl.RenderBar()
		})
		s.VT.Mu.Unlock()
	}

	// Start OTEL collector and set up env vars.
	if err := s.Agent.StartOtelCollector(); err != nil {
		return fmt.Errorf("start otel collector: %w", err)
	}
	s.ExtraEnv = s.Agent.OtelEnv()
	if s.ExtraEnv == nil {
		s.ExtraEnv = make(map[string]string)
	}
	s.ExtraEnv["H2_ACTOR"] = s.Name

	// Start child in a PTY.
	if err := s.VT.StartPTY(s.Command, s.Args, s.VT.ChildRows, s.VT.Cols, s.ExtraEnv); err != nil {
		return err
	}
	// Don't forward requests to stdout in daemon mode - there's no terminal.
	s.VT.Vt.ForwardResponses = s.VT.Ptm

	// Start session services (delivery loop + state watcher).
	go s.StartServices()

	// Update status bar every second.
	stopStatus := make(chan struct{})
	go s.TickStatus(stopStatus)

	// Pipe child output to virtual terminal.
	go s.VT.PipeOutput(s.pipeOutputCallback())

	// Run child process lifecycle loop.
	return s.lifecycleLoop(stopStatus, false)
}

// RunInteractive runs the session in interactive mode: creates VT, client,
// enters raw mode, starts PTY, and manages the child process lifecycle.
// Blocks until the user quits.
func (s *Session) RunInteractive() error {
	fd := int(os.Stdin.Fd())

	cols, rows, err := term.GetSize(fd)
	if err != nil {
		return fmt.Errorf("get terminal size (is this a terminal?): %w", err)
	}

	// Initialize VT.
	s.initVT(rows, cols)

	// Initialize client.
	s.Client = s.NewClient()
	s.AddClient(s.Client)

	minRows := 3
	if s.Client.DebugKeys {
		minRows = 4
	}
	if rows < minRows {
		return fmt.Errorf("terminal too small (need at least %d rows, have %d)", minRows, rows)
	}

	s.VT.ChildRows = rows - s.Client.ReservedRows()
	s.VT.Vt = midterm.NewTerminal(s.VT.ChildRows, cols)
	s.VT.Scrollback = midterm.NewTerminal(s.VT.ChildRows, cols)
	s.VT.Scrollback.AutoResizeY = true
	s.VT.Scrollback.AppendOnly = true
	s.VT.LastOut = time.Now()
	s.VT.Output = os.Stdout
	s.VT.InputSrc = os.Stdin

	// Set up delivery callback.
	s.OnDeliver = func() {
		s.VT.Mu.Lock()
		s.ForEachClient(func(cl *client.Client) {
			cl.RenderBar()
		})
		s.VT.Mu.Unlock()
	}

	// Start OTEL collector.
	if err := s.Agent.StartOtelCollector(); err != nil {
		return fmt.Errorf("start otel collector: %w", err)
	}
	s.ExtraEnv = s.Agent.OtelEnv()
	if s.ExtraEnv == nil {
		s.ExtraEnv = make(map[string]string)
	}
	s.ExtraEnv["H2_ACTOR"] = s.Name

	// Start child in a PTY.
	if err := s.VT.StartPTY(s.Command, s.Args, s.VT.ChildRows, cols, s.ExtraEnv); err != nil {
		return err
	}
	s.VT.Vt.ForwardRequests = os.Stdout
	s.VT.Vt.ForwardResponses = s.VT.Ptm

	// Set up interactive terminal (raw mode, mouse, SIGWINCH, input reading).
	cleanup, stopStatus, err := s.Client.SetupInteractiveTerminal()
	if err != nil {
		s.VT.Ptm.Close()
		return fmt.Errorf("set raw mode: %w", err)
	}
	defer cleanup()

	// Start session services (delivery loop + state watcher).
	go s.StartServices()

	// Pipe child output.
	go s.VT.PipeOutput(s.pipeOutputCallback())

	// Run child process lifecycle loop.
	return s.lifecycleLoop(stopStatus, true)
}

// lifecycleLoop manages the child process wait/relaunch cycle.
// interactive controls whether to forward VT requests to stdout on relaunch.
func (s *Session) lifecycleLoop(stopStatus chan struct{}, interactive bool) error {
	for {
		err := s.VT.Cmd.Wait()

		// If the user explicitly chose Quit, exit immediately.
		if s.Quit {
			s.VT.Ptm.Close()
			close(stopStatus)
			s.Stop()
			return err
		}

		s.VT.Mu.Lock()
		s.VT.ChildExited = true
		s.VT.ExitError = err
		s.ForEachClient(func(cl *client.Client) {
			cl.RenderScreen()
			cl.RenderBar()
		})
		s.VT.Mu.Unlock()

		s.NoteExit()
		s.Queue.Pause()

		select {
		case <-s.relaunchCh:
			s.VT.Ptm.Close()
			if err := s.VT.StartPTY(s.Command, s.Args, s.VT.ChildRows, s.VT.Cols, s.ExtraEnv); err != nil {
				close(stopStatus)
				s.Stop()
				return err
			}
			s.VT.Vt = midterm.NewTerminal(s.VT.ChildRows, s.VT.Cols)
			if interactive {
				s.VT.Vt.ForwardRequests = os.Stdout
			}
			s.VT.Vt.ForwardResponses = s.VT.Ptm
			s.VT.Scrollback = midterm.NewTerminal(s.VT.ChildRows, s.VT.Cols)
			s.VT.Scrollback.AutoResizeY = true
			s.VT.Scrollback.AppendOnly = true

			s.VT.Mu.Lock()
			s.VT.ChildExited = false
			s.VT.ChildHung = false
			s.VT.ExitError = nil
			s.VT.LastOut = time.Now()
			s.ForEachClient(func(cl *client.Client) {
				cl.ScrollOffset = 0
				s.VT.Output.Write([]byte("\033[2J\033[H"))
				cl.RenderScreen()
				cl.RenderBar()
			})
			s.VT.Mu.Unlock()

			go s.VT.PipeOutput(s.pipeOutputCallback())

			s.Queue.Unpause()
			continue

		case <-s.quitCh:
			s.VT.Ptm.Close()
			close(stopStatus)
			s.Stop()
			return err
		}
	}
}

// TickStatus triggers periodic status bar renders for all connected clients.
func (s *Session) TickStatus(stop <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.VT.Mu.Lock()
			s.ForEachClient(func(cl *client.Client) {
				cl.RenderBar()
			})
			s.VT.Mu.Unlock()
		case <-stop:
			return
		}
	}
}

// StartOtelCollector delegates to the Agent.
func (s *Session) StartOtelCollector() error {
	return s.Agent.StartOtelCollector()
}

// StopOtelCollector delegates to the Agent.
func (s *Session) StopOtelCollector() {
	s.Agent.StopOtelCollector()
}

// OtelPort delegates to the Agent.
func (s *Session) OtelPort() int {
	return s.Agent.OtelPort()
}

// OtelEnv delegates to the Agent.
func (s *Session) OtelEnv() map[string]string {
	return s.Agent.OtelEnv()
}

// Metrics delegates to the Agent.
func (s *Session) Metrics() agent.OtelMetricsSnapshot {
	return s.Agent.Metrics()
}

// State returns the current session state.
func (s *Session) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// StateChanged returns a channel that is closed when the session state changes.
func (s *Session) StateChanged() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateCh
}

// WaitForState blocks until the session reaches the target state or ctx is cancelled.
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
func (s *Session) NoteOutput() {
	select {
	case s.outputNotify <- struct{}{}:
	default:
	}
}

// NoteExit signals that the child process has exited or hung.
func (s *Session) NoteExit() {
	select {
	case s.exitNotify <- struct{}{}:
	default:
	}
}

// SubmitInput enqueues user-typed input for priority-aware delivery.
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

// StartServices launches the watchState and delivery goroutines. Blocks until Stop is called.
func (s *Session) StartServices() {
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
			PtyWriter: s.PtyWriter(),
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
	s.Agent.Stop()
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

		case <-s.Agent.OtelNotify():
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

