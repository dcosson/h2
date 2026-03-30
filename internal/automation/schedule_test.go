package automation

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

// fakeClock is a controllable clock for schedule engine tests.
// It manages fake timers that fire when the clock is advanced past their deadline.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

func newFakeClock(t time.Time) *fakeClock {
	return &fakeClock{now: t}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	ft := &fakeTimer{
		ch:       make(chan time.Time, 1),
		deadline: c.now.Add(d),
		clock:    c,
	}
	c.timers = append(c.timers, ft)
	// Fire immediately if already past deadline.
	if !ft.deadline.After(c.now) {
		ft.ch <- c.now
	}
	return ft
}

// Advance moves the clock forward and fires any timers whose deadlines
// have been reached. It yields briefly between rounds to let goroutines
// process timer events and potentially call Reset for the next occurrence.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
	c.settle()
}

// settle repeatedly fires ready timers until no more are eligible,
// yielding between rounds to let handler goroutines process.
func (c *fakeClock) settle() {
	for i := 0; i < 50; i++ {
		if !c.fireReady() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (c *fakeClock) fireReady() bool {
	c.mu.Lock()
	now := c.now
	timers := make([]*fakeTimer, len(c.timers))
	copy(timers, c.timers)
	c.mu.Unlock()

	fired := false
	for _, t := range timers {
		if t.tryFire(now) {
			fired = true
		}
	}
	return fired
}

type fakeTimer struct {
	mu       sync.Mutex
	ch       chan time.Time
	deadline time.Time
	stopped  bool
	clock    *fakeClock
}

func (ft *fakeTimer) tryFire(now time.Time) bool {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	if ft.stopped || ft.deadline.After(now) {
		return false
	}
	select {
	case ft.ch <- now:
		ft.deadline = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
		return true
	default:
		return false
	}
}

func (ft *fakeTimer) C() <-chan time.Time { return ft.ch }

func (ft *fakeTimer) Stop() bool {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	wasActive := !ft.stopped
	ft.stopped = true
	return wasActive
}

func (ft *fakeTimer) Reset(d time.Duration) bool {
	ft.clock.mu.Lock()
	now := ft.clock.now
	ft.clock.mu.Unlock()

	ft.mu.Lock()
	defer ft.mu.Unlock()
	wasActive := !ft.stopped
	ft.stopped = false
	ft.deadline = now.Add(d)
	if !ft.deadline.After(now) {
		select {
		case ft.ch <- now:
			ft.deadline = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
		default:
		}
	}
	return wasActive
}

// --- Test helpers ---

// waitForMessages polls enq until at least n messages have been enqueued
// or the timeout expires. This is needed because handleFiring runs in a
// goroutine and EvalCondition spawns a real subprocess, so there's a delay
// between the fake timer firing and the message being enqueued.
func waitForMessages(enq *mockEnqueuer, n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(enq.getMessages()) >= n {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return len(enq.getMessages()) >= n
}

// waitForScheduleRemoved polls se until the schedule list is empty or timeout.
func waitForScheduleRemoved(se *ScheduleEngine, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(se.List()) == 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return len(se.List()) == 0
}

var baseTime = time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

func newFakeScheduleEngine() (*ScheduleEngine, *mockEnqueuer, *fakeClock) {
	enq := &mockEnqueuer{}
	runner := NewActionRunner(enq, nil)
	clk := newFakeClock(baseTime)
	se := NewScheduleEngine(runner, WithClock(clk))
	return se, enq, clk
}

// --- Tests ---

func TestScheduleEngine_AutoGeneratesID(t *testing.T) {
	se, _, clk := newFakeScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	start := clk.Now().Add(1 * time.Second)
	s1 := &Schedule{Name: "first", RRule: "FREQ=MINUTELY;INTERVAL=10", Start: start.Format(time.RFC3339), Action: Action{Message: "a"}}
	s2 := &Schedule{Name: "second", RRule: "FREQ=MINUTELY;INTERVAL=10", Start: start.Format(time.RFC3339), Action: Action{Message: "b"}}

	if err := se.Add(s1); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if s1.ID == "" {
		t.Fatal("ID should have been auto-generated")
	}
	if err := se.Add(s2); err != nil {
		t.Fatalf("second add: %v", err)
	}
	if s1.ID == s2.ID {
		t.Errorf("auto-generated IDs should be unique, both are %q", s1.ID)
	}
}

func TestScheduleEngine_FiresOnTime(t *testing.T) {
	se, enq, clk := newFakeScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	start := clk.Now().Add(1 * time.Second)
	err := se.Add(&Schedule{
		ID:    "s1",
		Name:  "test-schedule",
		Start: start.Format(time.RFC3339),
		RRule: "FREQ=SECONDLY;INTERVAL=1;COUNT=2",
		Action: Action{
			Message:  "tick",
			Priority: "normal",
		},
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Advance to first occurrence.
	clk.Advance(1 * time.Second)

	msgs := enq.getMessages()
	if len(msgs) < 1 {
		t.Fatalf("expected at least 1 message, got %d", len(msgs))
	}
	if msgs[0].Body != "tick" {
		t.Errorf("expected body 'tick', got %q", msgs[0].Body)
	}
}

func TestScheduleEngine_RunIf_ConditionPass(t *testing.T) {
	se, enq, clk := newFakeScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	start := clk.Now().Add(1 * time.Second)
	err := se.Add(&Schedule{
		ID:            "s1",
		Start:         start.Format(time.RFC3339),
		RRule:         "FREQ=SECONDLY;INTERVAL=1;COUNT=2",
		Condition:     "true",
		ConditionMode: RunIf,
		Action: Action{
			Message: "run_if pass",
		},
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	clk.Advance(1 * time.Second)

	if !waitForMessages(enq, 1, 2*time.Second) {
		t.Fatalf("expected at least 1 message with passing condition, got %d", len(enq.getMessages()))
	}
}

func TestScheduleEngine_RunIf_ConditionFail(t *testing.T) {
	se, enq, clk := newFakeScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	start := clk.Now().Add(1 * time.Second)
	err := se.Add(&Schedule{
		ID:            "s1",
		Start:         start.Format(time.RFC3339),
		RRule:         "FREQ=SECONDLY;INTERVAL=1;COUNT=3",
		Condition:     "false",
		ConditionMode: RunIf,
		Action: Action{
			Message: "should not fire",
		},
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Advance through all 3 occurrences.
	clk.Advance(1 * time.Second)
	clk.Advance(1 * time.Second)
	clk.Advance(1 * time.Second)

	msgs := enq.getMessages()
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages with failing condition, got %d", len(msgs))
	}
}

func TestScheduleEngine_StopWhen_ConditionFail(t *testing.T) {
	se, enq, clk := newFakeScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	start := clk.Now().Add(1 * time.Second)
	err := se.Add(&Schedule{
		ID:            "s1",
		Start:         start.Format(time.RFC3339),
		RRule:         "FREQ=SECONDLY;INTERVAL=1;COUNT=3",
		Condition:     "false",
		ConditionMode: StopWhen,
		Action: Action{
			Message: "stop_when action",
		},
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	clk.Advance(1 * time.Second)
	clk.Advance(1 * time.Second)

	if !waitForMessages(enq, 1, 2*time.Second) {
		t.Fatalf("StopWhen with failing condition should run the action, got %d messages", len(enq.getMessages()))
	}
}

func TestScheduleEngine_StopWhen_ConditionPass(t *testing.T) {
	se, enq, clk := newFakeScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	start := clk.Now().Add(1 * time.Second)
	err := se.Add(&Schedule{
		ID:            "s1",
		Start:         start.Format(time.RFC3339),
		RRule:         "FREQ=SECONDLY;INTERVAL=1;COUNT=5",
		Condition:     "true",
		ConditionMode: StopWhen,
		Action: Action{
			Message: "should not fire",
		},
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	clk.Advance(1 * time.Second)

	// Condition passes immediately → schedule removed, no action.
	// Wait for handleFiring to finish (condition eval spawns a subprocess).
	if !waitForScheduleRemoved(se, 2*time.Second) {
		t.Fatal("schedule should be removed when StopWhen condition passes")
	}
	msgs := enq.getMessages()
	if len(msgs) != 0 {
		t.Fatalf("StopWhen with passing condition should not run action, got %d messages", len(msgs))
	}
}

func TestScheduleEngine_RunOnceWhen_EventuallyFires(t *testing.T) {
	se, enq, clk := newFakeScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	// Use a temp file as a latch: condition passes when file exists.
	tmp := t.TempDir() + "/latch"

	start := clk.Now().Add(1 * time.Second)
	err := se.Add(&Schedule{
		ID:            "s1",
		Start:         start.Format(time.RFC3339),
		RRule:         "FREQ=SECONDLY;INTERVAL=1;COUNT=10",
		Condition:     "test -f " + tmp,
		ConditionMode: RunOnceWhen,
		Action: Action{
			Message: "once when",
		},
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// First two ticks: condition fails (file doesn't exist).
	clk.Advance(1 * time.Second)
	// Wait for condition eval to complete before checking.
	time.Sleep(100 * time.Millisecond)
	clk.Advance(1 * time.Second)
	time.Sleep(100 * time.Millisecond)
	if len(enq.getMessages()) != 0 {
		t.Fatal("should not fire before condition passes")
	}

	// Create the latch file.
	if err := writeFile(tmp, "go"); err != nil {
		t.Fatalf("create latch: %v", err)
	}

	// Next tick: condition passes.
	clk.Advance(1 * time.Second)

	if !waitForMessages(enq, 1, 2*time.Second) {
		t.Fatalf("expected exactly 1 message, got %d", len(enq.getMessages()))
	}
	msgs := enq.getMessages()
	if msgs[0].Body != "once when" {
		t.Errorf("expected 'once when', got %q", msgs[0].Body)
	}
	if !waitForScheduleRemoved(se, 2*time.Second) {
		t.Fatal("RunOnceWhen schedule should be removed after firing")
	}
}

func TestScheduleEngine_RecurringRRule(t *testing.T) {
	se, enq, clk := newFakeScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	start := clk.Now().Add(1 * time.Second)
	err := se.Add(&Schedule{
		ID:    "s1",
		Start: start.Format(time.RFC3339),
		RRule: "FREQ=SECONDLY;INTERVAL=1;COUNT=3",
		Action: Action{
			Message: "recurring",
		},
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Advance through all 3 occurrences.
	clk.Advance(1 * time.Second)
	clk.Advance(1 * time.Second)
	clk.Advance(1 * time.Second)

	msgs := enq.getMessages()
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages from recurring schedule, got %d", len(msgs))
	}
}

func TestScheduleEngine_FiniteRRule(t *testing.T) {
	se, enq, clk := newFakeScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	start := clk.Now().Add(1 * time.Second)
	err := se.Add(&Schedule{
		ID:    "s1",
		Start: start.Format(time.RFC3339),
		RRule: "FREQ=SECONDLY;INTERVAL=1;COUNT=2",
		Action: Action{
			Message: "finite",
		},
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Advance through both occurrences.
	clk.Advance(1 * time.Second)
	clk.Advance(1 * time.Second)

	msgs := enq.getMessages()
	if len(msgs) < 1 {
		t.Fatal("expected at least 1 message from finite schedule")
	}
	// Schedule should be auto-removed after exhaustion.
	// Give a small extra advance to ensure cleanup completed.
	clk.Advance(1 * time.Second)
	if len(se.List()) != 0 {
		t.Fatal("finite schedule should be removed after RRULE exhaustion")
	}
}

func TestScheduleEngine_Remove(t *testing.T) {
	se, enq, clk := newFakeScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	start := clk.Now().Add(2 * time.Second) // starts in the future
	err := se.Add(&Schedule{
		ID:    "s1",
		Start: start.Format(time.RFC3339),
		RRule: "FREQ=SECONDLY;INTERVAL=1;COUNT=5",
		Action: Action{
			Message: "should not fire",
		},
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	if !se.Remove("s1") {
		t.Fatal("Remove should return true for existing schedule")
	}
	if se.Remove("s1") {
		t.Fatal("Remove should return false for non-existent schedule")
	}

	clk.Advance(5 * time.Second)

	if len(enq.getMessages()) != 0 {
		t.Fatal("removed schedule should not fire")
	}
}

func TestScheduleEngine_AddDuplicateID(t *testing.T) {
	se, _, clk := newFakeScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	start := clk.Now().Add(1 * time.Second)
	err := se.Add(&Schedule{
		ID:    "s1",
		Start: start.Format(time.RFC3339),
		RRule: "FREQ=SECONDLY;INTERVAL=1;COUNT=2",
		Action: Action{
			Message: "first",
		},
	})
	if err != nil {
		t.Fatalf("first Add failed: %v", err)
	}

	err = se.Add(&Schedule{
		ID:    "s1",
		Start: start.Format(time.RFC3339),
		RRule: "FREQ=SECONDLY;INTERVAL=1;COUNT=2",
		Action: Action{
			Message: "duplicate",
		},
	})
	if err == nil {
		t.Fatal("Add with duplicate ID should return error")
	}
}

func TestScheduleEngine_InvalidRRule(t *testing.T) {
	se, _, _ := newFakeScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	err := se.Add(&Schedule{
		ID:    "s1",
		RRule: "INVALID",
		Action: Action{
			Message: "test",
		},
	})
	if err == nil {
		t.Fatal("expected error for invalid RRULE")
	}
}

func TestScheduleEngine_EnvVarsSet(t *testing.T) {
	se, enq, clk := newFakeScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	start := clk.Now().Add(1 * time.Second)
	err := se.Add(&Schedule{
		ID:        "s1",
		Start:     start.Format(time.RFC3339),
		RRule:     "FREQ=SECONDLY;INTERVAL=1;COUNT=1",
		Condition: `test "$H2_SCHEDULE_ID" = "s1"`,
		Action: Action{
			Message: "env ok",
		},
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	clk.Advance(1 * time.Second)

	if !waitForMessages(enq, 1, 2*time.Second) {
		t.Fatalf("expected 1 message, got %d", len(enq.getMessages()))
	}
}

func TestEvalConditionMode(t *testing.T) {
	tests := []struct {
		name       string
		mode       ConditionMode
		condPass   bool
		noCond     bool
		wantRun    bool
		wantRemove bool
	}{
		{"RunIf pass", RunIf, true, false, true, false},
		{"RunIf fail", RunIf, false, false, false, false},
		{"StopWhen pass", StopWhen, true, false, false, true},
		{"StopWhen fail", StopWhen, false, false, true, false},
		{"RunOnceWhen pass", RunOnceWhen, true, false, true, true},
		{"RunOnceWhen fail", RunOnceWhen, false, false, false, false},
		{"no condition", RunIf, false, true, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run, remove := evalConditionMode(tt.mode, tt.condPass, tt.noCond)
			if run != tt.wantRun {
				t.Errorf("shouldRun = %v, want %v", run, tt.wantRun)
			}
			if remove != tt.wantRemove {
				t.Errorf("shouldRemove = %v, want %v", remove, tt.wantRemove)
			}
		})
	}
}

// writeFile is a test helper to write content to a file.
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
