package session

import (
	"context"
	"fmt"
	"sync"

	"h2/internal/config"
	"h2/internal/session/message"
)

type ManagedOpts struct {
	SessionDir string
	Resume     bool
}

// ManagedRuntime owns one session without creating a per-agent socket. The
// gateway uses this as the process/lifecycle seam while session.Session keeps
// terminal, queue, monitor, and harness behavior.
type ManagedRuntime struct {
	Session    *Session
	Automation *RuntimeAutomation

	done     chan error
	startMu  sync.Mutex
	started  bool
	stopOnce sync.Once
}

func NewManagedRuntime(rc *config.RuntimeConfig, opts ManagedOpts) *ManagedRuntime {
	return &ManagedRuntime{
		Session: newRuntimeSession(opts.SessionDir, rc, opts.Resume),
		done:    make(chan error, 1),
	}
}

func (r *ManagedRuntime) Start(ctx context.Context) error {
	r.startMu.Lock()
	defer r.startMu.Unlock()

	if r.started {
		return fmt.Errorf("managed runtime already started")
	}
	automationRuntime, err := newRuntimeAutomation(r.Session, r.Session.SessionDir, r.Session.RC)
	if err != nil {
		return fmt.Errorf("load role automations: %w", err)
	}
	r.Automation = automationRuntime
	r.started = true

	go func() {
		stopCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			<-stopCtx.Done()
			r.Stop()
		}()
		err := r.Session.RunDaemon()
		automationRuntime.Stop()
		r.done <- err
	}()
	return nil
}

func (r *ManagedRuntime) Done() <-chan error {
	return r.done
}

func (r *ManagedRuntime) Stop() {
	r.stopOnce.Do(func() {
		s := r.Session
		s.Quit = true
		if s.VT != nil {
			s.VT.KillChild()
		}
		select {
		case s.quitCh <- struct{}{}:
		default:
		}
	})
}

func (r *ManagedRuntime) Status() *message.AgentInfo {
	return r.Session.AgentInfo(r.Session.StartTime, r.Session.RC.Pod)
}
