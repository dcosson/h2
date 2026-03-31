package automation

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/teambition/rrule-go"
)

// StateProvider returns the current agent state and sub-state as strings.
// Used by ScheduleEngine to inject H2_AGENT_STATE/H2_AGENT_SUBSTATE.
type StateProvider func() (state, subState string)

// ScheduleEngine evaluates RRULEs and manages timers for scheduled actions.
// It runs as a goroutine started by the daemon.
type ScheduleEngine struct {
	mu            sync.Mutex
	schedules     map[string]*activeSchedule
	runner        *ActionRunner
	stateProvider StateProvider
	clock         Clock
}

// activeSchedule pairs the spec with runtime state.
type activeSchedule struct {
	spec  *Schedule
	rule  *rrule.RRule
	timer Timer
	stop  chan struct{} // closed to cancel this schedule's goroutine
}

// ScheduleEngineOption configures the ScheduleEngine.
type ScheduleEngineOption func(*ScheduleEngine)

// WithClock sets a custom clock for the schedule engine (used in tests).
func WithClock(c Clock) ScheduleEngineOption {
	return func(se *ScheduleEngine) { se.clock = c }
}

// WithStateProvider sets a state provider for injecting agent state into env.
func WithStateProvider(sp StateProvider) ScheduleEngineOption {
	return func(se *ScheduleEngine) { se.stateProvider = sp }
}

// NewScheduleEngine creates a ScheduleEngine that dispatches actions via the given runner.
func NewScheduleEngine(runner *ActionRunner, opts ...ScheduleEngineOption) *ScheduleEngine {
	se := &ScheduleEngine{
		schedules: make(map[string]*activeSchedule),
		runner:    runner,
		clock:     realClock{},
	}
	for _, opt := range opts {
		opt(se)
	}
	return se
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

	if s.ID == "" {
		s.ID = uuid.New().String()[:8]
	}
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

	delay := next.Sub(se.clock.Now())
	if delay < 0 {
		delay = 0
	}
	as.timer = se.clock.NewTimer(delay)

	se.schedules[s.ID] = as
	go se.runSchedule(as)
	return nil
}

// Clear removes all registered schedules, stopping their goroutines.
func (se *ScheduleEngine) Clear() {
	se.mu.Lock()
	all := make([]*activeSchedule, 0, len(se.schedules))
	for _, as := range se.schedules {
		all = append(all, as)
	}
	se.schedules = make(map[string]*activeSchedule)
	se.mu.Unlock()

	for _, as := range all {
		close(as.stop)
		as.timer.Stop()
	}
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
	now := se.clock.Now()
	result := make([]*Schedule, 0, len(se.schedules))
	for _, as := range se.schedules {
		s := *as.spec // copy
		s.NextFireAt = as.rule.After(now, true)
		result = append(result, &s)
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
		case <-as.timer.C():
			se.handleFiring(as)
		}
	}
}

// handleFiring processes a single schedule firing.
func (se *ScheduleEngine) handleFiring(as *activeSchedule) {
	s := as.spec
	env := map[string]string{"H2_SCHEDULE_ID": s.ID}
	if se.stateProvider != nil {
		state, subState := se.stateProvider()
		env["H2_AGENT_STATE"] = state
		env["H2_AGENT_SUBSTATE"] = subState
	}

	// Merge runner's base env (H2_ACTOR, H2_ROLE, etc.) into condition env.
	condEnv := se.runner.MergeEnv(env)
	condCtx, cancel := context.WithTimeout(context.Background(), DefaultConditionTimeout)
	condPass := EvalCondition(condCtx, s.Condition, condEnv, se.runner.WorkDir())
	cancel()

	shouldRun, shouldRemove := evalConditionMode(s.ConditionMode, condPass, s.Condition == "")

	if shouldRun {
		fmt.Fprintf(os.Stderr, "automation: schedule fired id=%s name=%s condition_mode=%s\n",
			s.ID, s.Name, s.ConditionMode.String())
		action := s.Action
		action.Header = s.ScheduleHeader()
		if err := se.runner.Run(action, env); err != nil {
			fmt.Fprintf(os.Stderr, "automation: schedule action failed id=%s error=%v\n", s.ID, err)
		}
	}

	if shouldRemove {
		se.mu.Lock()
		delete(se.schedules, s.ID)
		se.mu.Unlock()
		return
	}

	// Schedule next occurrence.
	now := se.clock.Now()
	next := as.rule.After(now, false)
	if next.IsZero() {
		fmt.Fprintf(os.Stderr, "automation: schedule exhausted (RRULE complete) id=%s\n", s.ID)
		se.mu.Lock()
		delete(se.schedules, s.ID)
		se.mu.Unlock()
		return
	}

	delay := next.Sub(now)
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
	var startTime time.Time
	if s.Start != "" {
		t, err := time.Parse(time.RFC3339, s.Start)
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("parse start time: %w", err)
		}
		startTime = t
	} else {
		// No explicit start: align to the top of the current minute so
		// minutely schedules fire at :00 seconds (e.g. 10:00, 10:10).
		startTime = time.Now().Truncate(time.Minute)
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
