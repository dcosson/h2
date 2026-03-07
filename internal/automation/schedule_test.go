package automation

import (
	"context"
	"os"
	"testing"
	"time"
)

func newTestScheduleEngine() (*ScheduleEngine, *mockEnqueuer) {
	enq := &mockEnqueuer{}
	runner := NewActionRunner(enq, nil, nil)
	se := NewScheduleEngine(runner, nil)
	return se, enq
}

func TestScheduleEngine_FiresOnTime(t *testing.T) {
	se, enq := newTestScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	now := time.Now().Add(-1 * time.Second)
	err := se.Add(&Schedule{
		ID:    "s1",
		Name:  "test-schedule",
		Start: now.Format(time.RFC3339),
		RRule: "FREQ=SECONDLY;INTERVAL=1;COUNT=2",
		Action: Action{
			Message:  "tick",
			Priority: "normal",
		},
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Wait for at least one firing.
	time.Sleep(1500 * time.Millisecond)

	msgs := enq.getMessages()
	if len(msgs) < 1 {
		t.Fatalf("expected at least 1 message, got %d", len(msgs))
	}
	if msgs[0].Body != "tick" {
		t.Errorf("expected body 'tick', got %q", msgs[0].Body)
	}
}

func TestScheduleEngine_RunIf_ConditionPass(t *testing.T) {
	se, enq := newTestScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	now := time.Now().Add(-1 * time.Second)
	err := se.Add(&Schedule{
		ID:            "s1",
		Start:         now.Format(time.RFC3339),
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

	time.Sleep(1500 * time.Millisecond)

	msgs := enq.getMessages()
	if len(msgs) < 1 {
		t.Fatal("expected at least 1 message with passing condition")
	}
}

func TestScheduleEngine_RunIf_ConditionFail(t *testing.T) {
	se, enq := newTestScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	now := time.Now().Add(-1 * time.Second)
	err := se.Add(&Schedule{
		ID:            "s1",
		Start:         now.Format(time.RFC3339),
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

	time.Sleep(2 * time.Second)

	msgs := enq.getMessages()
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages with failing condition, got %d", len(msgs))
	}
}

func TestScheduleEngine_StopWhen_ConditionFail(t *testing.T) {
	se, enq := newTestScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	now := time.Now().Add(-1 * time.Second)
	err := se.Add(&Schedule{
		ID:            "s1",
		Start:         now.Format(time.RFC3339),
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

	time.Sleep(2 * time.Second)

	msgs := enq.getMessages()
	if len(msgs) < 1 {
		t.Fatal("StopWhen with failing condition should run the action")
	}
}

func TestScheduleEngine_StopWhen_ConditionPass(t *testing.T) {
	se, enq := newTestScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	now := time.Now().Add(-1 * time.Second)
	err := se.Add(&Schedule{
		ID:            "s1",
		Start:         now.Format(time.RFC3339),
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

	time.Sleep(1500 * time.Millisecond)

	// Condition passes immediately → schedule removed, no action.
	msgs := enq.getMessages()
	if len(msgs) != 0 {
		t.Fatalf("StopWhen with passing condition should not run action, got %d messages", len(msgs))
	}
	if len(se.List()) != 0 {
		t.Fatal("schedule should be removed when StopWhen condition passes")
	}
}

func TestScheduleEngine_RunOnceWhen_EventuallyFires(t *testing.T) {
	se, enq := newTestScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	// Use a temp file as a latch: condition passes when file exists.
	tmp := t.TempDir() + "/latch"

	now := time.Now().Add(-1 * time.Second)
	err := se.Add(&Schedule{
		ID:            "s1",
		Start:         now.Format(time.RFC3339),
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

	// First few firings: condition fails (file doesn't exist).
	time.Sleep(1500 * time.Millisecond)
	if len(enq.getMessages()) != 0 {
		t.Fatal("should not fire before condition passes")
	}

	// Create the latch file.
	if err := writeFile(tmp, "go"); err != nil {
		t.Fatalf("create latch: %v", err)
	}

	// Wait for the next firing.
	time.Sleep(1500 * time.Millisecond)

	msgs := enq.getMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected exactly 1 message, got %d", len(msgs))
	}
	if msgs[0].Body != "once when" {
		t.Errorf("expected 'once when', got %q", msgs[0].Body)
	}
	if len(se.List()) != 0 {
		t.Fatal("RunOnceWhen schedule should be removed after firing")
	}
}

func TestScheduleEngine_RecurringRRule(t *testing.T) {
	se, enq := newTestScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	now := time.Now().Add(-1 * time.Second)
	err := se.Add(&Schedule{
		ID:    "s1",
		Start: now.Format(time.RFC3339),
		RRule: "FREQ=SECONDLY;INTERVAL=1;COUNT=3",
		Action: Action{
			Message: "recurring",
		},
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Wait for all 3 firings.
	time.Sleep(3500 * time.Millisecond)

	msgs := enq.getMessages()
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages from recurring schedule, got %d", len(msgs))
	}
}

func TestScheduleEngine_FiniteRRule(t *testing.T) {
	se, enq := newTestScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	now := time.Now().Add(-1 * time.Second)
	err := se.Add(&Schedule{
		ID:    "s1",
		Start: now.Format(time.RFC3339),
		RRule: "FREQ=SECONDLY;INTERVAL=1;COUNT=2",
		Action: Action{
			Message: "finite",
		},
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Wait for schedule to exhaust.
	time.Sleep(3 * time.Second)

	msgs := enq.getMessages()
	if len(msgs) < 1 {
		t.Fatal("expected at least 1 message from finite schedule")
	}
	// Schedule should be auto-removed after exhaustion.
	if len(se.List()) != 0 {
		t.Fatal("finite schedule should be removed after RRULE exhaustion")
	}
}

func TestScheduleEngine_Remove(t *testing.T) {
	se, enq := newTestScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	now := time.Now().Add(2 * time.Second) // starts in the future
	err := se.Add(&Schedule{
		ID:    "s1",
		Start: now.Format(time.RFC3339),
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

	time.Sleep(3 * time.Second)

	if len(enq.getMessages()) != 0 {
		t.Fatal("removed schedule should not fire")
	}
}

func TestScheduleEngine_AddDuplicateID(t *testing.T) {
	se, _ := newTestScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	now := time.Now().Add(-1 * time.Second)
	err := se.Add(&Schedule{
		ID:    "s1",
		Start: now.Format(time.RFC3339),
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
		Start: now.Format(time.RFC3339),
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
	se, _ := newTestScheduleEngine()
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
	se, enq := newTestScheduleEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go se.Run(ctx)

	now := time.Now().Add(-1 * time.Second)
	err := se.Add(&Schedule{
		ID:        "s1",
		Start:     now.Format(time.RFC3339),
		RRule:     "FREQ=SECONDLY;INTERVAL=1;COUNT=1",
		Condition: `test "$H2_SCHEDULE_ID" = "s1"`,
		Action: Action{
			Message: "env ok",
		},
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	time.Sleep(1500 * time.Millisecond)

	if len(enq.getMessages()) != 1 {
		t.Fatalf("expected 1 message, got %d", len(enq.getMessages()))
	}
}

func TestEvalConditionMode(t *testing.T) {
	tests := []struct {
		name        string
		mode        ConditionMode
		condPass    bool
		noCond      bool
		wantRun     bool
		wantRemove  bool
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
