package gateway

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"h2/internal/config"
	"h2/internal/session"
	"h2/internal/session/agent/monitor"
	"h2/internal/session/message"
)

const (
	GatewayDesiredRunning = "running"
	GatewayDesiredStopped = "stopped"

	GatewayRuntimeStarting     = "starting"
	GatewayRuntimeRunning      = "running"
	GatewayRuntimeExited       = "exited"
	GatewayRuntimeStopped      = "stopped"
	GatewayRuntimeResumeFailed = "resume_failed"
)

type ManagerOpts struct {
	H2Dir      string
	Generation string
}

type StartSessionRequest struct {
	SessionDir    string
	RuntimeConfig *config.RuntimeConfig
	Resume        bool
}

type SessionStatus struct {
	Agent        *message.AgentInfo `json:"agent,omitempty"`
	SessionDir   string             `json:"session_dir"`
	DesiredState string             `json:"desired_state,omitempty"`
	RuntimeState string             `json:"runtime_state,omitempty"`
	ChildPID     int                `json:"child_pid,omitempty"`
	ChildPGID    int                `json:"child_pgid,omitempty"`
	LastExit     string             `json:"last_exit,omitempty"`
	LastStateAt  string             `json:"last_state_at,omitempty"`
}

type Manager struct {
	h2Dir      string
	generation string

	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	sessions map[string]*managedSession
}

type managedSession struct {
	mu sync.Mutex

	sessionDir      string
	rc              *config.RuntimeConfig
	runtime         *session.ManagedRuntime
	stopReason      string
	done            chan struct{}
	exitedPersisted bool
}

func NewManager(opts ManagerOpts) *Manager {
	h2Dir := opts.H2Dir
	if h2Dir == "" {
		h2Dir = config.ConfigDir()
	}
	generation := opts.Generation
	if generation == "" {
		generation = fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		h2Dir:      h2Dir,
		generation: generation,
		ctx:        ctx,
		cancel:     cancel,
		sessions:   make(map[string]*managedSession),
	}
}

func (m *Manager) H2Dir() string {
	return m.h2Dir
}

func (m *Manager) StartSession(req StartSessionRequest) (*SessionStatus, error) {
	if req.RuntimeConfig == nil {
		return nil, fmt.Errorf("runtime config is required")
	}
	if req.SessionDir == "" {
		return nil, fmt.Errorf("session dir is required")
	}
	if req.RuntimeConfig.AgentName == "" {
		return nil, fmt.Errorf("agent name is required")
	}
	if err := os.MkdirAll(req.SessionDir, 0o700); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}

	m.mu.Lock()
	if _, exists := m.sessions[req.RuntimeConfig.AgentName]; exists {
		m.mu.Unlock()
		return nil, fmt.Errorf("session %q is already managed by gateway", req.RuntimeConfig.AgentName)
	}
	rc := *req.RuntimeConfig
	entry := &managedSession{
		sessionDir: req.SessionDir,
		rc:         &rc,
		runtime:    session.NewManagedRuntime(&rc, session.ManagedOpts{SessionDir: req.SessionDir, Resume: req.Resume}),
		done:       make(chan struct{}),
	}
	m.sessions[rc.AgentName] = entry
	m.mu.Unlock()

	if err := entry.updateMetadata(func(current *config.RuntimeConfig) {
		current.GatewayPID = os.Getpid()
		current.GatewayGeneration = m.generation
		current.GatewayDesiredState = GatewayDesiredRunning
		current.GatewayRuntimeState = GatewayRuntimeStarting
		current.ChildPID = 0
		current.ChildPGID = 0
		current.LastExitReason = ""
		current.LastStateAt = nowRFC3339()
	}); err != nil {
		m.removeSession(rc.AgentName)
		return nil, err
	}

	if err := entry.runtime.Start(m.ctx); err != nil {
		m.removeSession(rc.AgentName)
		_ = entry.updateMetadata(func(current *config.RuntimeConfig) {
			current.GatewayRuntimeState = GatewayRuntimeResumeFailed
			current.LastExitReason = "gateway_start_failed: " + err.Error()
			current.LastStateAt = nowRFC3339()
		})
		return nil, err
	}

	go m.watchSession(entry)
	return entry.status(), nil
}

func (m *Manager) StopSession(agentName string) error {
	m.mu.Lock()
	entry := m.sessions[agentName]
	if entry == nil {
		m.mu.Unlock()
		return fmt.Errorf("session %q is not managed by gateway", agentName)
	}
	entry.stopReason = "user_stop"
	m.mu.Unlock()

	if err := entry.updateMetadata(func(current *config.RuntimeConfig) {
		current.GatewayDesiredState = GatewayDesiredStopped
		current.LastExitReason = "user_stop_requested"
		current.LastStateAt = nowRFC3339()
	}); err != nil {
		return err
	}
	entry.runtime.Stop()
	return nil
}

func (m *Manager) Status(agentName string) (*SessionStatus, error) {
	m.mu.Lock()
	entry := m.sessions[agentName]
	m.mu.Unlock()
	if entry == nil {
		return nil, fmt.Errorf("session %q is not managed by gateway", agentName)
	}
	return entry.status(), nil
}

func (m *Manager) List() []*SessionStatus {
	m.mu.Lock()
	entries := make([]*managedSession, 0, len(m.sessions))
	for _, entry := range m.sessions {
		entries = append(entries, entry)
	}
	m.mu.Unlock()

	statuses := make([]*SessionStatus, 0, len(entries))
	for _, entry := range entries {
		statuses = append(statuses, entry.status())
	}
	return statuses
}

func (m *Manager) Shutdown(ctx context.Context) error {
	m.cancel()

	m.mu.Lock()
	entries := make([]*managedSession, 0, len(m.sessions))
	for _, entry := range m.sessions {
		entry.stopReason = "gateway_shutdown"
		entries = append(entries, entry)
	}
	m.mu.Unlock()

	for _, entry := range entries {
		entry.runtime.Stop()
	}
	for _, entry := range entries {
		select {
		case <-entry.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (m *Manager) watchSession(entry *managedSession) {
	runningPersisted := false
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for !runningPersisted {
		select {
		case err := <-entry.runtime.Done():
			m.finishSession(entry, err)
			return
		case <-ticker.C:
			if state, _ := entry.runtime.Session.State(); state == monitor.StateExited {
				entry.markExited(nil)
				runningPersisted = true
				continue
			}
			pid, pgid := childProcessIDs(entry.runtime)
			if pid > 0 {
				_ = entry.updateMetadata(func(current *config.RuntimeConfig) {
					current.GatewayRuntimeState = GatewayRuntimeRunning
					current.ChildPID = pid
					current.ChildPGID = pgid
					current.LastStateAt = nowRFC3339()
				})
				runningPersisted = true
			}
		}
	}

	stateChanged := entry.runtime.Session.StateChanged()
	for {
		select {
		case err := <-entry.runtime.Done():
			m.finishSession(entry, err)
			return
		case <-stateChanged:
			if state, _ := entry.runtime.Session.State(); state == monitor.StateExited {
				entry.markExited(nil)
			}
			stateChanged = entry.runtime.Session.StateChanged()
		}
	}
}

func (m *Manager) finishSession(entry *managedSession, runErr error) {
	defer close(entry.done)

	m.mu.Lock()
	stopReason := entry.stopReason
	delete(m.sessions, entry.rc.AgentName)
	m.mu.Unlock()

	_ = entry.updateMetadata(func(current *config.RuntimeConfig) {
		current.ChildPID = 0
		current.ChildPGID = 0
		current.LastStateAt = nowRFC3339()
		switch stopReason {
		case "user_stop":
			current.GatewayDesiredState = GatewayDesiredStopped
			current.GatewayRuntimeState = GatewayRuntimeStopped
			current.LastExitReason = "user_stop"
		case "gateway_shutdown":
			current.GatewayRuntimeState = GatewayRuntimeExited
			current.LastExitReason = "gateway_shutdown"
		default:
			current.GatewayDesiredState = GatewayDesiredRunning
			current.GatewayRuntimeState = GatewayRuntimeExited
			current.LastExitReason = exitReason(runErr)
		}
	})
}

func (m *Manager) removeSession(agentName string) {
	m.mu.Lock()
	delete(m.sessions, agentName)
	m.mu.Unlock()
}

func (entry *managedSession) status() *SessionStatus {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return &SessionStatus{
		Agent:        entry.runtime.Status(),
		SessionDir:   entry.sessionDir,
		DesiredState: entry.rc.GatewayDesiredState,
		RuntimeState: entry.rc.GatewayRuntimeState,
		ChildPID:     entry.rc.ChildPID,
		ChildPGID:    entry.rc.ChildPGID,
		LastExit:     entry.rc.LastExitReason,
		LastStateAt:  entry.rc.LastStateAt,
	}
}

func (entry *managedSession) markExited(runErr error) {
	entry.mu.Lock()
	if entry.exitedPersisted {
		entry.mu.Unlock()
		return
	}
	entry.exitedPersisted = true
	entry.mu.Unlock()

	_ = entry.updateMetadata(func(current *config.RuntimeConfig) {
		current.GatewayDesiredState = GatewayDesiredRunning
		current.GatewayRuntimeState = GatewayRuntimeExited
		current.LastExitReason = exitReason(runErr)
		current.LastStateAt = nowRFC3339()
	})
}

func (entry *managedSession) updateMetadata(mutate func(*config.RuntimeConfig)) error {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return updateSessionMetadata(entry.sessionDir, entry.rc, mutate)
}

func childProcessIDs(runtime *session.ManagedRuntime) (int, int) {
	if runtime == nil || runtime.Session == nil || runtime.Session.VT == nil || runtime.Session.VT.Cmd == nil || runtime.Session.VT.Cmd.Process == nil {
		return 0, 0
	}
	pid := runtime.Session.VT.Cmd.Process.Pid
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		return pid, 0
	}
	return pid, pgid
}

func updateSessionMetadata(sessionDir string, fallback *config.RuntimeConfig, mutate func(*config.RuntimeConfig)) error {
	current, err := config.ReadRuntimeConfig(sessionDir)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read gateway session metadata %s: %w", filepath.Base(sessionDir), err)
		}
		copy := *fallback
		current = &copy
	}
	mutate(current)
	syncGatewayFields(fallback, current)
	if err := config.WriteRuntimeConfig(sessionDir, current); err != nil {
		return fmt.Errorf("write gateway session metadata %s: %w", filepath.Base(sessionDir), err)
	}
	return nil
}

func syncGatewayFields(dst, src *config.RuntimeConfig) {
	dst.GatewayPID = src.GatewayPID
	dst.GatewayGeneration = src.GatewayGeneration
	dst.GatewayDesiredState = src.GatewayDesiredState
	dst.GatewayRuntimeState = src.GatewayRuntimeState
	dst.ChildPID = src.ChildPID
	dst.ChildPGID = src.ChildPGID
	dst.LastExitReason = src.LastExitReason
	dst.LastStateAt = src.LastStateAt
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func exitReason(err error) string {
	if err == nil {
		return "child_exit_0"
	}
	return "child_exit: " + err.Error()
}
