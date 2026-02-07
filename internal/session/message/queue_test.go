package message

import (
	"testing"
	"time"
)

func newMsg(id string, priority Priority) *Message {
	return &Message{
		ID:        id,
		Priority:  priority,
		Status:    StatusQueued,
		CreatedAt: time.Now(),
	}
}

func TestDequeueOrder_InterruptFirst(t *testing.T) {
	q := NewMessageQueue()
	q.Enqueue(newMsg("normal-1", PriorityNormal))
	q.Enqueue(newMsg("idle-1", PriorityIdle))
	q.Enqueue(newMsg("interrupt-1", PriorityInterrupt))

	msg := q.Dequeue(true)
	if msg == nil || msg.ID != "interrupt-1" {
		t.Fatalf("expected interrupt-1, got %v", msg)
	}
	msg = q.Dequeue(true)
	if msg == nil || msg.ID != "normal-1" {
		t.Fatalf("expected normal-1, got %v", msg)
	}
	msg = q.Dequeue(true)
	if msg == nil || msg.ID != "idle-1" {
		t.Fatalf("expected idle-1, got %v", msg)
	}
	msg = q.Dequeue(true)
	if msg != nil {
		t.Fatalf("expected nil, got %v", msg)
	}
}

func TestDequeueOrder_NormalBeforeIdle(t *testing.T) {
	q := NewMessageQueue()
	q.Enqueue(newMsg("idle-1", PriorityIdle))
	q.Enqueue(newMsg("normal-1", PriorityNormal))

	msg := q.Dequeue(true)
	if msg == nil || msg.ID != "normal-1" {
		t.Fatalf("expected normal-1, got %v", msg)
	}
	msg = q.Dequeue(true)
	if msg == nil || msg.ID != "idle-1" {
		t.Fatalf("expected idle-1, got %v", msg)
	}
}

func TestDequeueOrder_IdleFirstBeforeIdle(t *testing.T) {
	q := NewMessageQueue()
	q.Enqueue(newMsg("idle-1", PriorityIdle))
	q.Enqueue(newMsg("idle-first-1", PriorityIdleFirst))

	msg := q.Dequeue(true)
	if msg == nil || msg.ID != "idle-first-1" {
		t.Fatalf("expected idle-first-1, got %v", msg)
	}
	msg = q.Dequeue(true)
	if msg == nil || msg.ID != "idle-1" {
		t.Fatalf("expected idle-1, got %v", msg)
	}
}

func TestDequeueOrder_IdleFirstPrepends(t *testing.T) {
	q := NewMessageQueue()
	q.Enqueue(newMsg("if-1", PriorityIdleFirst))
	q.Enqueue(newMsg("if-2", PriorityIdleFirst))
	q.Enqueue(newMsg("if-3", PriorityIdleFirst))

	// Most recently enqueued idle-first should come first.
	msg := q.Dequeue(true)
	if msg == nil || msg.ID != "if-3" {
		t.Fatalf("expected if-3, got %v", msg)
	}
	msg = q.Dequeue(true)
	if msg == nil || msg.ID != "if-2" {
		t.Fatalf("expected if-2, got %v", msg)
	}
	msg = q.Dequeue(true)
	if msg == nil || msg.ID != "if-1" {
		t.Fatalf("expected if-1, got %v", msg)
	}
}

func TestDequeueOrder_NormalFIFO(t *testing.T) {
	q := NewMessageQueue()
	q.Enqueue(newMsg("n-1", PriorityNormal))
	q.Enqueue(newMsg("n-2", PriorityNormal))
	q.Enqueue(newMsg("n-3", PriorityNormal))

	for _, expected := range []string{"n-1", "n-2", "n-3"} {
		msg := q.Dequeue(true)
		if msg == nil || msg.ID != expected {
			t.Fatalf("expected %s, got %v", expected, msg)
		}
	}
}

func TestDequeueOrder_IdleFIFO(t *testing.T) {
	q := NewMessageQueue()
	q.Enqueue(newMsg("i-1", PriorityIdle))
	q.Enqueue(newMsg("i-2", PriorityIdle))
	q.Enqueue(newMsg("i-3", PriorityIdle))

	for _, expected := range []string{"i-1", "i-2", "i-3"} {
		msg := q.Dequeue(true)
		if msg == nil || msg.ID != expected {
			t.Fatalf("expected %s, got %v", expected, msg)
		}
	}
}

func TestDequeue_IdleNotReturnedWhenNotIdle(t *testing.T) {
	q := NewMessageQueue()
	q.Enqueue(newMsg("idle-1", PriorityIdle))
	q.Enqueue(newMsg("idle-first-1", PriorityIdleFirst))

	msg := q.Dequeue(false) // not idle
	if msg != nil {
		t.Fatalf("expected nil when not idle, got %v", msg)
	}

	// But normal messages are still returned.
	q.Enqueue(newMsg("normal-1", PriorityNormal))
	msg = q.Dequeue(false)
	if msg == nil || msg.ID != "normal-1" {
		t.Fatalf("expected normal-1, got %v", msg)
	}
}

func TestPause_BlocksNonInterrupt(t *testing.T) {
	q := NewMessageQueue()
	q.Enqueue(newMsg("normal-1", PriorityNormal))
	q.Enqueue(newMsg("idle-1", PriorityIdle))
	q.Pause()

	msg := q.Dequeue(true)
	if msg != nil {
		t.Fatalf("expected nil when paused, got %v", msg)
	}

	if !q.IsPaused() {
		t.Fatal("expected paused")
	}
}

func TestPause_InterruptBypassesPause(t *testing.T) {
	q := NewMessageQueue()
	q.Enqueue(newMsg("normal-1", PriorityNormal))
	q.Enqueue(newMsg("interrupt-1", PriorityInterrupt))
	q.Pause()

	msg := q.Dequeue(true)
	if msg == nil || msg.ID != "interrupt-1" {
		t.Fatalf("expected interrupt-1 to bypass pause, got %v", msg)
	}

	// Normal still blocked.
	msg = q.Dequeue(true)
	if msg != nil {
		t.Fatalf("expected nil for normal while paused, got %v", msg)
	}
}

func TestUnpause_DeliversNormal(t *testing.T) {
	q := NewMessageQueue()
	q.Enqueue(newMsg("normal-1", PriorityNormal))
	q.Pause()

	msg := q.Dequeue(true)
	if msg != nil {
		t.Fatalf("expected nil while paused, got %v", msg)
	}

	q.Unpause()
	msg = q.Dequeue(true)
	if msg == nil || msg.ID != "normal-1" {
		t.Fatalf("expected normal-1 after unpause, got %v", msg)
	}
}

func TestPendingCount(t *testing.T) {
	q := NewMessageQueue()
	if q.PendingCount() != 0 {
		t.Fatalf("expected 0, got %d", q.PendingCount())
	}

	q.Enqueue(newMsg("a", PriorityInterrupt))
	q.Enqueue(newMsg("b", PriorityNormal))
	q.Enqueue(newMsg("c", PriorityIdle))
	if q.PendingCount() != 3 {
		t.Fatalf("expected 3, got %d", q.PendingCount())
	}

	q.Dequeue(true) // dequeue interrupt
	if q.PendingCount() != 2 {
		t.Fatalf("expected 2, got %d", q.PendingCount())
	}
}

func TestLookup(t *testing.T) {
	q := NewMessageQueue()
	q.Enqueue(newMsg("msg-1", PriorityNormal))

	msg := q.Lookup("msg-1")
	if msg == nil || msg.ID != "msg-1" {
		t.Fatalf("expected msg-1, got %v", msg)
	}

	msg = q.Lookup("nonexistent")
	if msg != nil {
		t.Fatalf("expected nil for nonexistent, got %v", msg)
	}
}

func TestFullPriorityOrder(t *testing.T) {
	q := NewMessageQueue()
	q.Enqueue(newMsg("idle-1", PriorityIdle))
	q.Enqueue(newMsg("idle-first-1", PriorityIdleFirst))
	q.Enqueue(newMsg("normal-1", PriorityNormal))
	q.Enqueue(newMsg("interrupt-1", PriorityInterrupt))
	q.Enqueue(newMsg("normal-2", PriorityNormal))
	q.Enqueue(newMsg("idle-first-2", PriorityIdleFirst))
	q.Enqueue(newMsg("idle-2", PriorityIdle))

	expected := []string{
		"interrupt-1",
		"normal-1",
		"normal-2",
		"idle-first-2", // most recently enqueued idle-first comes first
		"idle-first-1",
		"idle-1",
		"idle-2",
	}

	for _, exp := range expected {
		msg := q.Dequeue(true)
		if msg == nil || msg.ID != exp {
			var got string
			if msg != nil {
				got = msg.ID
			}
			t.Fatalf("expected %s, got %s", exp, got)
		}
	}
}
