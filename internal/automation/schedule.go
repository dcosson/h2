package automation

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/teambition/rrule-go"
)

// ScheduleEngine evaluates RRULEs and manages timers for scheduled actions.
// It runs as a goroutine started by the daemon.
type ScheduleEngine struct {
	mu        sync.Mutex
	schedules map[string]*activeSchedule
	runner    *ActionRunner
	logger    *slog.Logger
}

// activeSchedule pairs the spec with runtime state.
type activeSchedule struct {
	spec  *Schedule
	rule  *rrule.RRule
	timer *time.Timer
	stop  chan struct{} // closed to cancel this schedule's goroutine
}

// NewScheduleEngine creates a ScheduleEngine that dispatches actions via the given runner.
func NewScheduleEngine(runner *ActionRunner, logger *slog.Logger) *ScheduleEngine {
	if logger == nil {
		logger = slog.Default()
	}
	return &ScheduleEngine{
		schedules: make(map[string]*activeSchedule),
		runner:    runner,
		logger:    logger,
	}
}

// Run blocks until ctx is cancelled, keeping all schedule timers alive.
func (se *ScheduleEngine) Run(ctx context.Context) {
	<-ctx.Done()
	se.mu.Lock()
	defer se.mu.Unlock()
	for _, as := range se.schedules {
		close(as.stop)
		as.timer.Stop()
	}
}

// Add registers a schedule. Returns an error if the RRULE is invalid or the
// ID already exists.
func (se *ScheduleEngine) Add(s *Schedule) error {
	rule, startTime, err := parseSchedule(s)
	if err != nil {
		return err
	}

	se.mu.Lock()
	defer se.mu.Unlock()

	if _, exists := se.schedules[s.ID]; exists {
		return fmt.Errorf("schedule ID %q already exists", s.ID)
	}

	as := &activeSchedule{
		spec: s,
		rule: rule,
		stop: make(chan struct{}),
	}

	next := rule.After(startTime.Add(-time.Millisecond), true)
	if next.IsZero() {
		return fmt.Errorf("schedule %q has no occurrences", s.ID)
	}

	delay := time.Until(next)
	if delay < 0 {
		delay = 0
	}
	as.timer = time.NewTimer(delay)

	se.schedules[s.ID] = as
	go se.runSchedule(as)
	return nil
}

// Remove deletes a schedule by ID. Returns true if it existed.
func (se *ScheduleEngine) Remove(id string) bool {
	se.mu.Lock()
	as, exists := se.schedules[id]
	if exists {
		delete(se.schedules, id)
	}
	se.mu.Unlock()

	if exists {
		close(as.stop)
		as.timer.Stop()
	}
	return exists
}

// List returns a copy of all registered schedule specs.
func (se *ScheduleEngine) List() []*Schedule {
	se.mu.Lock()
	defer se.mu.Unlock()
	result := make([]*Schedule, 0, len(se.schedules))
	for _, as := range se.schedules {
		result = append(result, as.spec)
	}
	return result
}

// runSchedule is the per-schedule goroutine. It waits for timer firings,
// evaluates conditions, runs actions, and schedules the next occurrence.
func (se *ScheduleEngine) runSchedule(as *activeSchedule) {
	for {
		select {
		case <-as.stop:
			return
		case <-as.timer.C:
			se.handleFiring(as)
		}
	}
}

// handleFiring processes a single schedule firing.
func (se *ScheduleEngine) handleFiring(as *activeSchedule) {
	s := as.spec
	env := map[string]string{"H2_SCHEDULE_ID": s.ID}

	condCtx, cancel := context.WithTimeout(context.Background(), DefaultConditionTimeout)
	condPass := EvalCondition(condCtx, s.Condition, env)
	cancel()

	shouldRun, shouldRemove := evalConditionMode(s.ConditionMode, condPass, s.Condition == "")

	if shouldRun {
		se.logger.Info("schedule fired",
			"schedule_id", s.ID, "schedule_name", s.Name,
			"condition_mode", s.ConditionMode.String())
		if err := se.runner.Run(s.Action, env); err != nil {
			se.logger.Warn("schedule action failed",
				"schedule_id", s.ID, "error", err)
		}
	}

	if shouldRemove {
		se.logger.Info("schedule removed",
			"schedule_id", s.ID, "reason", "condition mode completed")
		se.mu.Lock()
		delete(se.schedules, s.ID)
		se.mu.Unlock()
		return
	}

	// Schedule next occurrence.
	now := time.Now()
	next := as.rule.After(now, false)
	if next.IsZero() {
		se.logger.Info("schedule exhausted (RRULE complete)",
			"schedule_id", s.ID)
		se.mu.Lock()
		delete(se.schedules, s.ID)
		se.mu.Unlock()
		return
	}

	delay := time.Until(next)
	if delay < 0 {
		delay = 0
	}
	as.timer.Reset(delay)
}

// evalConditionMode returns (shouldRun, shouldRemove) based on the condition
// mode and whether the condition passed.
func evalConditionMode(mode ConditionMode, condPass bool, noCondition bool) (bool, bool) {
	if noCondition {
		return true, false
	}
	switch mode {
	case RunIf:
		if condPass {
			return true, false
		}
		return false, false
	case StopWhen:
		if condPass {
			return false, true
		}
		return true, false
	case RunOnceWhen:
		if condPass {
			return true, true // fire once, then remove
		}
		return false, false
	default:
		return true, false
	}
}

// parseSchedule parses the RRULE string and start time from a Schedule spec.
func parseSchedule(s *Schedule) (*rrule.RRule, time.Time, error) {
	startTime := time.Now()
	if s.Start != "" {
		t, err := time.Parse(time.RFC3339, s.Start)
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("parse start time: %w", err)
		}
		startTime = t
	}

	// Build the full RRULE string with DTSTART for the library.
	ruleStr := fmt.Sprintf("DTSTART:%s\nRRULE:%s",
		startTime.UTC().Format("20060102T150405Z"),
		s.RRule)

	rule, err := rrule.StrToRRule(ruleStr)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("parse RRULE: %w", err)
	}

	return rule, startTime, nil
}
