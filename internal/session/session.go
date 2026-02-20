package session

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/vito/midterm"
	"golang.org/x/term"

	"h2/internal/activitylog"
	"h2/internal/config"
	"h2/internal/session/agent"
	"h2/internal/session/agent/monitor"
	"h2/internal/session/agent/shared/eventstore"
	"h2/internal/session/client"
	"h2/internal/session/message"
	"h2/internal/session/virtualterminal"
)

// Session manages the message queue, delivery loop, observable state,
// child process lifecycle, and client connections for an h2 session.
type Session struct {
	Name       string
	Command    string
	Args       []string
	SessionID      string // Claude Code session ID (UUID), set for claude commands
	RoleName       string // Role name, if launched with --role
	SessionDir     string // Session directory path (~/.h2/sessions/<name>/)
	ClaudeConfigDir string // Shared Claude config dir (used as CLAUDE_CONFIG_DIR)
	Instructions    string   // Role instructions, passed via --append-system-prompt
	SystemPrompt    string   // Replaces default system prompt, passed via --system-prompt
	Model           string   // Model selection, passed via --model
	PermissionMode  string   // Permission mode, passed via --permission-mode
	AllowedTools    []string // Allowed tools, passed via --allowedTools (comma-joined)
	DisallowedTools []string // Disallowed tools, passed via --disallowedTools (comma-joined)
	Queue      *message.MessageQueue
	AgentName  string
	Agent      *agent.Agent
	VT         *virtualterminal.VT
	Client     *client.Client // primary/interactive client (nil in daemon-only)
	Clients          []*client.Client
	clientsMu        sync.Mutex
	PassthroughOwner *client.Client // which client owns passthrough mode (nil = none)

	// prependArgs holds CLI args from the adapter's launch config
	// (e.g. --session-id for Claude Code). Set by setupAgent().
	prependArgs []string

	// eventStore persists AgentEvents to events.jsonl for peek/replay.
	eventStore *eventstore.EventStore

	// ExtraEnv holds additional environment variables to pass to the child process.
	ExtraEnv map[string]string

	// Heartbeat nudge configuration.
	HeartbeatIdleTimeout time.Duration
	HeartbeatMessage     string
	HeartbeatCondition   string

	// Daemon holds the networking/attach layer (nil in interactive mode).
	Daemon    *Daemon
	StartTime time.Time

	// Quit is set when the user explicitly chooses to quit.
	Quit bool

	exitNotify chan struct{} // buffered(1), signaled on child exit

	stopCh     chan struct{}
	relaunchCh chan struct{}
	quitCh     chan struct{}

	// OnDeliver is called after each message delivery (e.g. to re-render UI).
	OnDeliver func()
}

// New creates a new Session with the given name and command.
func New(name string, command string, args []string) *Session {
	agentType := agent.ResolveAgentType(command)
	return &Session{
		Name:       name,
		Command:    command,
		Args:       args,
		AgentName:  name,
		Queue:      message.NewMessageQueue(),
		Agent:      agent.New(agentType),
		exitNotify: make(chan struct{}, 1),
		stopCh:     make(chan struct{}),
		relaunchCh: make(chan struct{}, 1),
		quitCh:     make(chan struct{}, 1),
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

// setupAgent configures the agent adapter and launch config. Sets up
// activity logger, calls PrepareForLaunch to get env vars and CLI args,
// and merges them into the session's ExtraEnv and prependArgs.
func (s *Session) setupAgent() error {
	// Set up activity logger and raw OTEL log files.
	logDir := filepath.Join(config.ConfigDir(), "logs")
	os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, "session-activity.jsonl")
	s.Agent.SetActivityLog(activitylog.New(true, logPath, s.Name, s.SessionID))
	s.Agent.SetOtelLogFiles(logDir)

	// Create adapter and get launch config (env vars, prepend args).
	launchCfg, err := s.Agent.PrepareForLaunch(s.Name, s.SessionID)
	if err != nil {
		return fmt.Errorf("prepare agent for launch: %w", err)
	}

	// Merge adapter env with session env.
	s.ExtraEnv = launchCfg.Env
	if s.ExtraEnv == nil {
		s.ExtraEnv = make(map[string]string)
	}

	// Store prepend args for childArgs().
	s.prependArgs = launchCfg.PrependArgs

	// Wire event store for event persistence (best-effort).
	if s.SessionDir != "" {
		if es, err := eventstore.Open(s.SessionDir); err == nil {
			s.eventStore = es
			s.Agent.SetEventWriter(es.Append)
		}
	}

	return nil
}

// childArgs returns the command args, prepending any adapter-supplied args
// (e.g. --session-id for Claude Code) and appending agent-type-specific
// role flags via BuildCommandArgs.
func (s *Session) childArgs() []string {
	var args []string
	if len(s.prependArgs) > 0 {
		args = append(s.prependArgs, s.Args...)
	} else {
		args = s.Args
	}

	roleArgs := s.Agent.AgentType().BuildCommandArgs(agent.CommandArgsConfig{
		Instructions:    s.Instructions,
		SystemPrompt:    s.SystemPrompt,
		Model:           s.Model,
		PermissionMode:  s.PermissionMode,
		AllowedTools:    s.AllowedTools,
		DisallowedTools: s.DisallowedTools,
	})
	args = append(args, roleArgs...)

	return args
}

// NewClient creates a new Client with all session callbacks wired.
func (s *Session) NewClient() *client.Client {
	cl := &client.Client{
		VT:        s.VT,
		Output:    io.Discard, // overridden by caller (attach sets frameWriter, interactive sets os.Stdout)
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
		// If leaving passthrough, release the lock.
		// ModePassthroughScroll preserves passthrough ownership.
		if mode != client.ModePassthrough && mode != client.ModePassthroughScroll && s.PassthroughOwner == cl {
			s.PassthroughOwner = nil
			s.Queue.Unpause()
		}
	}

	// Passthrough locking callbacks.
	cl.TryPassthrough = func() bool {
		if s.PassthroughOwner != nil && s.PassthroughOwner != cl {
			return false // locked by another client
		}
		s.PassthroughOwner = cl
		s.Queue.Pause()
		return true
	}
	cl.ReleasePassthrough = func() {
		if s.PassthroughOwner == cl {
			s.PassthroughOwner = nil
			s.Queue.Unpause()
		}
	}
	cl.TakePassthrough = func() {
		prev := s.PassthroughOwner
		if prev != nil && prev != cl {
			// Kick the previous owner back to default mode.
			prev.Mode = client.ModeNormal
			prev.RenderBar()
		}
		s.PassthroughOwner = cl
		s.Queue.Pause()
	}
	cl.IsPassthroughLocked = func() bool {
		return s.PassthroughOwner != nil && s.PassthroughOwner != cl
	}
	cl.QueueStatus = func() (int, bool) {
		return s.Queue.PendingCount(), s.Queue.IsPaused()
	}
	cl.OtelMetrics = func() (int64, int64, float64, bool, int) {
		m := s.Agent.Metrics()
		return m.InputTokens, m.OutputTokens, m.TotalCostUSD, m.EventsReceived, s.Agent.OtelPort()
	}
	cl.AgentState = func() (string, string, string) {
		st, sub := s.State()
		return st.String(), sub.String(), virtualterminal.FormatIdleDuration(s.StateDuration())
	}
	cl.HookState = func() string {
		if hc := s.Agent.HookCollector(); hc != nil {
			return hc.Snapshot().LastToolName
		}
		return ""
	}
	cl.OnInterrupt = func() {
		s.Agent.NoteInterrupt()
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
			if !cl.IsScrollMode() {
				cl.RenderScreen()
				cl.RenderBar()
			}
		})
	}
}

// RunDaemon runs the session in daemon mode: creates VT, client, PTY,
// starts adapter+monitor, socket listener, and manages the child process lifecycle.
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

	// Set up agent: activity logger, adapter, launch config.
	if err := s.setupAgent(); err != nil {
		return err
	}
	s.ExtraEnv["H2_ACTOR"] = s.Name
	if s.RoleName != "" {
		s.ExtraEnv["H2_ROLE"] = s.RoleName
	}
	if s.SessionDir != "" {
		s.ExtraEnv["H2_SESSION_DIR"] = s.SessionDir
	}
	if s.ClaudeConfigDir != "" {
		s.ExtraEnv["CLAUDE_CONFIG_DIR"] = s.ClaudeConfigDir
	}

	// Start child in a PTY.
	if err := s.VT.StartPTY(s.Command, s.childArgs(), s.VT.ChildRows, s.VT.Cols, s.ExtraEnv); err != nil {
		return err
	}
	// Don't forward requests to stdout in daemon mode - there's no terminal.
	s.VT.Vt.ForwardResponses = s.VT.Ptm

	// Start adapter+monitor pipeline (after child process is running).
	s.Agent.Start(context.Background())

	// Start delivery loop.
	go s.StartServices()

	// Launch heartbeat nudge goroutine if configured.
	if s.HeartbeatIdleTimeout > 0 {
		go RunHeartbeat(HeartbeatConfig{
			IdleTimeout: s.HeartbeatIdleTimeout,
			Message:     s.HeartbeatMessage,
			Condition:   s.HeartbeatCondition,
			Agent:       s.Agent,
			Queue:       s.Queue,
			AgentName:   s.AgentName,
			Stop:        s.stopCh,
		})
	}

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
	s.Client.TermRows = rows
	s.Client.TermCols = cols
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
	s.Client.Output = os.Stdout
	s.VT.InputSrc = os.Stdin

	// Set up delivery callback.
	s.OnDeliver = func() {
		s.VT.Mu.Lock()
		s.ForEachClient(func(cl *client.Client) {
			cl.RenderBar()
		})
		s.VT.Mu.Unlock()
	}

	// Set up agent: activity logger, adapter, launch config.
	if err := s.setupAgent(); err != nil {
		return err
	}
	s.ExtraEnv["H2_ACTOR"] = s.Name

	// Start child in a PTY.
	if err := s.VT.StartPTY(s.Command, s.childArgs(), s.VT.ChildRows, cols, s.ExtraEnv); err != nil {
		return err
	}
	s.VT.Vt.ForwardRequests = os.Stdout
	s.VT.Vt.ForwardResponses = s.VT.Ptm

	// Start adapter+monitor pipeline (after child process is running).
	s.Agent.Start(context.Background())

	// Set up interactive terminal (raw mode, mouse, SIGWINCH, input reading).
	cleanup, stopStatus, err := s.Client.SetupInteractiveTerminal()
	if err != nil {
		s.VT.Ptm.Close()
		return fmt.Errorf("set raw mode: %w", err)
	}
	defer cleanup()

	// Start delivery loop.
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
			if err := s.VT.StartPTY(s.Command, s.childArgs(), s.VT.ChildRows, s.VT.Cols, s.ExtraEnv); err != nil {
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
				cl.Output.Write([]byte("\033[2J\033[H"))
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

// --- Delegators to Agent ---

// OtelPort delegates to the Agent.
func (s *Session) OtelPort() int {
	return s.Agent.OtelPort()
}

// Metrics delegates to the Agent.
func (s *Session) Metrics() agent.OtelMetricsSnapshot {
	return s.Agent.Metrics()
}

// State returns the current agent state and sub-state.
func (s *Session) State() (monitor.State, monitor.SubState) {
	return s.Agent.State()
}

// StateChanged returns a channel that is closed when the agent state changes.
func (s *Session) StateChanged() <-chan struct{} {
	return s.Agent.StateChanged()
}

// WaitForState blocks until the agent reaches the target state or ctx is cancelled.
func (s *Session) WaitForState(ctx context.Context, target monitor.State) bool {
	return s.Agent.WaitForState(ctx, target)
}

// StateDuration returns how long the agent has been in its current state.
func (s *Session) StateDuration() time.Duration {
	return s.Agent.StateDuration()
}

// NoteOutput signals that the child process has produced output.
// Feeds through to the Agent's output collector.
func (s *Session) NoteOutput() {
	s.Agent.NoteOutput()
}

// NoteExit signals that the child process has exited or hung.
func (s *Session) NoteExit() {
	s.Agent.SetExited()
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

// StartServices launches the delivery goroutine. Blocks until Stop is called.
func (s *Session) StartServices() {
	message.RunDelivery(message.DeliveryConfig{
		Queue:     s.Queue,
		AgentName: s.AgentName,
		PtyWriter: s.PtyWriter(),
		IsIdle: func() bool {
			st, _ := s.Agent.State()
			return st == monitor.StateIdle
		},
		IsBlocked: func() bool {
			st, sub := s.Agent.State()
			return st == monitor.StateActive && sub == monitor.SubStateWaitingForPermission
		},
		WaitForIdle: func(ctx context.Context) bool {
			return s.Agent.WaitForState(ctx, monitor.StateIdle)
		},
		NoteInterrupt: func() {
			s.Agent.NoteInterrupt()
		},
		OnDeliver: s.OnDeliver,
		Stop:      s.stopCh,
	})
}

// Stop signals all goroutines to stop and cleans up resources.
func (s *Session) Stop() {
	select {
	case <-s.stopCh:
		// already stopped
	default:
		close(s.stopCh)
	}

	// Gather session summary data before stopping the agent (which closes files).
	summary := s.buildSessionSummary()
	s.Agent.ActivityLog().SessionSummary(summary)

	s.Agent.Stop()

	if s.eventStore != nil {
		s.eventStore.Close()
	}
}

// buildSessionSummary collects metrics from all available sources.
func (s *Session) buildSessionSummary() activitylog.SessionSummaryData {
	snap := s.Agent.Metrics()

	d := activitylog.SessionSummaryData{
		InputTokens:  snap.InputTokens,
		OutputTokens: snap.OutputTokens,
		TotalTokens:  snap.TotalTokens,
		CostUSD:      snap.TotalCostUSD,
		APIRequests:  snap.APIRequestCount,
		ToolCalls:    snap.ToolResultCount,
		LinesAdded:   snap.LinesAdded,
		LinesRemoved: snap.LinesRemoved,
		ToolCounts:   snap.ToolCounts,

		ActiveTimeHrs: snap.ActiveTimeHrs,
		ModelCosts:    snap.ModelCosts,
		ModelTokens:   snap.ModelTokens,
	}

	// Hook collector metrics.
	if hc := s.Agent.HookCollector(); hc != nil {
		d.ToolUseCount = hc.Snapshot().ToolUseCount
	}

	// Session uptime.
	if !s.StartTime.IsZero() {
		d.Uptime = time.Since(s.StartTime).Round(time.Second).String()
	}

	// Point-in-time git working tree stats.
	if gs := gitStats(); gs != nil {
		d.GitFilesChanged = gs.FilesChanged
		d.GitLinesAdded = gs.LinesAdded
		d.GitLinesRemoved = gs.LinesRemoved
	}

	return d
}
