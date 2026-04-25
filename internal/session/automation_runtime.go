package session

import (
	"context"
	"fmt"
	"time"

	"h2/internal/automation"
	"h2/internal/config"
)

// RuntimeAutomation owns the automation engines attached to one Session.
// Legacy daemon sessions and gateway-managed sessions both use this helper so
// triggers/schedules keep identical semantics across runtime models.
type RuntimeAutomation struct {
	TriggerEngine  *automation.TriggerEngine
	ScheduleEngine *automation.ScheduleEngine

	cancel context.CancelFunc
	runner *automation.ActionRunner
}

func newRuntimeAutomation(s *Session, sessionDir string, rc *config.RuntimeConfig) (*RuntimeAutomation, error) {
	baseEnv := map[string]string{
		"H2_ACTOR": rc.AgentName,
	}
	if rc.RoleName != "" {
		baseEnv["H2_ROLE"] = rc.RoleName
	}
	if sessionDir != "" {
		baseEnv["H2_SESSION_DIR"] = sessionDir
	}

	stateProvider := func() (string, string) {
		st, sub := s.State()
		return st.String(), sub.String()
	}

	enqueuer := &sessionEnqueuer{queue: s.Queue, agentName: rc.AgentName}
	runner := automation.NewActionRunner(enqueuer, baseEnv, rc.CWD)
	triggerEngine := automation.NewTriggerEngine(runner, stateProvider)
	scheduleEngine := automation.NewScheduleEngine(runner, automation.WithStateProvider(stateProvider))

	if err := loadRoleAutomations(triggerEngine, scheduleEngine, rc); err != nil {
		triggerEngine.Clear()
		scheduleEngine.Clear()
		runner.Wait()
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	eventCh := s.monitor.Subscribe()
	go triggerEngine.Run(ctx, eventCh)
	go scheduleEngine.Run(ctx)

	return &RuntimeAutomation{
		TriggerEngine:  triggerEngine,
		ScheduleEngine: scheduleEngine,
		cancel:         cancel,
		runner:         runner,
	}, nil
}

func (ra *RuntimeAutomation) Stop() {
	if ra == nil {
		return
	}
	ra.cancel()
	ra.runner.Wait()
}

func (ra *RuntimeAutomation) Reload(rc *config.RuntimeConfig) error {
	ra.TriggerEngine.Clear()
	ra.ScheduleEngine.Clear()
	return loadRoleAutomations(ra.TriggerEngine, ra.ScheduleEngine, rc)
}

// loadRoleAutomations registers triggers and schedules from the RuntimeConfig
// (originally defined in the role YAML).
func loadRoleAutomations(triggerEngine *automation.TriggerEngine, scheduleEngine *automation.ScheduleEngine, rc *config.RuntimeConfig) error {
	now := time.Now()
	for _, ts := range rc.Triggers {
		t := &automation.Trigger{
			ID:        ts.ID,
			Name:      ts.Name,
			Event:     ts.Event,
			State:     ts.State,
			SubState:  ts.SubState,
			Condition: ts.Condition,
			Action: automation.Action{
				Exec:     ts.Exec,
				Message:  ts.Message,
				From:     ts.From,
				Priority: ts.Priority,
			},
			MaxFirings: ts.MaxFirings,
		}
		if ts.ExpiresAt != "" {
			parsed, err := automation.ResolveExpiresAt(ts.ExpiresAt, now)
			if err != nil {
				return fmt.Errorf("trigger %q: %w", ts.ID, err)
			}
			t.ExpiresAt = parsed
		}
		if ts.Cooldown != "" {
			parsed, err := time.ParseDuration(ts.Cooldown)
			if err != nil {
				return fmt.Errorf("trigger %q: parse cooldown %q: %w", ts.ID, ts.Cooldown, err)
			}
			if parsed < 0 {
				return fmt.Errorf("trigger %q: cooldown must be non-negative, got %s", ts.ID, parsed)
			}
			t.Cooldown = parsed
		}
		if !triggerEngine.Add(t) {
			return fmt.Errorf("duplicate trigger ID %q in role config", ts.ID)
		}
	}

	for _, ss := range rc.Schedules {
		mode, _ := automation.ParseConditionMode(ss.ConditionMode)
		s := &automation.Schedule{
			ID:            ss.ID,
			Name:          ss.Name,
			Start:         ss.Start,
			RRule:         ss.RRule,
			Condition:     ss.Condition,
			ConditionMode: mode,
			Action: automation.Action{
				Exec:     ss.Exec,
				Message:  ss.Message,
				From:     ss.From,
				Priority: ss.Priority,
			},
		}
		if err := scheduleEngine.Add(s); err != nil {
			return fmt.Errorf("register schedule %q: %w", ss.ID, err)
		}
	}
	return nil
}
