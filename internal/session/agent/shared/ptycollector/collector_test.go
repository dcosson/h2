package ptycollector

import (
	"testing"
	"time"

	"h2/internal/session/agent/monitor"
)

const testIdleThreshold = 10 * time.Millisecond

func TestCollector_ActiveOnOutput(t *testing.T) {
	c := New(testIdleThreshold)
	defer c.Stop()

	c.NoteOutput()

	select {
	case su := <-c.StateCh():
		if su.State != monitor.StateActive {
			t.Fatalf("expected StateActive, got %v", su.State)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for StateActive")
	}
}

func TestCollector_IdleAfterThreshold(t *testing.T) {
	c := New(testIdleThreshold)
	defer c.Stop()

	c.NoteOutput()
	// Drain the active signal.
	<-c.StateCh()

	select {
	case su := <-c.StateCh():
		if su.State != monitor.StateIdle {
			t.Fatalf("expected StateIdle, got %v", su.State)
		}
	case <-time.After(testIdleThreshold + time.Second):
		t.Fatal("timed out waiting for StateIdle")
	}
}

func TestCollector_ResetTimerOnOutput(t *testing.T) {
	c := New(testIdleThreshold)
	defer c.Stop()

	c.NoteOutput()
	<-c.StateCh() // drain active

	// Send another output before idle fires — should reset the timer.
	time.Sleep(testIdleThreshold / 2)
	c.NoteOutput()

	select {
	case su := <-c.StateCh():
		if su.State != monitor.StateActive {
			t.Fatalf("expected StateActive from second output, got %v", su.State)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second StateActive")
	}
}

func TestCollector_Stop(t *testing.T) {
	c := New(testIdleThreshold)
	c.Stop()

	// After stop, NoteOutput should not panic.
	c.NoteOutput()
}
