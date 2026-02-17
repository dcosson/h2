package message

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// threadSafeBuffer is a bytes.Buffer safe for concurrent Write calls.
type threadSafeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *threadSafeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *threadSafeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestDeliver_RawInput(t *testing.T) {
	var buf threadSafeBuffer
	q := NewMessageQueue()
	stop := make(chan struct{})

	msg := &Message{
		ID:        "raw-1",
		From:      "user",
		Priority:  PriorityNormal,
		Body:      "echo hello",
		Status:    StatusQueued,
		CreatedAt: time.Now(),
	}
	q.Enqueue(msg)

	delivered := make(chan struct{}, 1)
	go RunDelivery(DeliveryConfig{
		Queue:     q,
		PtyWriter: &buf,
		IsIdle:    func() bool { return true },
		OnDeliver: func() {
			select {
			case delivered <- struct{}{}:
			default:
			}
		},
		Stop: stop,
	})

	select {
	case <-delivered:
	case <-time.After(3 * time.Second):
		t.Fatal("delivery timed out")
	}
	close(stop)

	out := buf.String()
	if !strings.Contains(out, "echo hello") {
		t.Fatalf("expected raw body 'echo hello' in output, got %q", out)
	}
	if strings.Contains(out, "[h2-message") {
		t.Fatal("raw input should not contain [h2-message header")
	}
	if !strings.HasSuffix(out, "\r") {
		t.Fatal("expected output to end with \\r")
	}
}

func TestDeliver_InterAgentMessage(t *testing.T) {
	var buf threadSafeBuffer
	q := NewMessageQueue()
	stop := make(chan struct{})

	msg := &Message{
		ID:        "msg-1",
		From:      "agent-a",
		Priority:  PriorityNormal,
		Body:      "do something",
		FilePath:  "/tmp/test-msg.md",
		Status:    StatusQueued,
		CreatedAt: time.Now(),
	}
	q.Enqueue(msg)

	delivered := make(chan struct{}, 1)
	go RunDelivery(DeliveryConfig{
		Queue:     q,
		PtyWriter: &buf,
		IsIdle:    func() bool { return true },
		OnDeliver: func() {
			select {
			case delivered <- struct{}{}:
			default:
			}
		},
		Stop: stop,
	})

	select {
	case <-delivered:
	case <-time.After(3 * time.Second):
		t.Fatal("delivery timed out")
	}
	close(stop)

	out := buf.String()
	if !strings.Contains(out, "[h2 message from: agent-a] do something") {
		t.Fatalf("expected h2-message header in output, got %q", out)
	}
}

func TestDeliver_InterAgentMessage_LongBody(t *testing.T) {
	var buf threadSafeBuffer
	q := NewMessageQueue()
	stop := make(chan struct{})

	longBody := strings.Repeat("x", 301)
	msg := &Message{
		ID:        "msg-long",
		From:      "agent-a",
		Priority:  PriorityNormal,
		Body:      longBody,
		FilePath:  "/tmp/test-long.md",
		Status:    StatusQueued,
		CreatedAt: time.Now(),
	}
	q.Enqueue(msg)

	delivered := make(chan struct{}, 1)
	go RunDelivery(DeliveryConfig{
		Queue:     q,
		PtyWriter: &buf,
		IsIdle:    func() bool { return true },
		OnDeliver: func() {
			select {
			case delivered <- struct{}{}:
			default:
			}
		},
		Stop: stop,
	})

	select {
	case <-delivered:
	case <-time.After(3 * time.Second):
		t.Fatal("delivery timed out")
	}
	close(stop)

	out := buf.String()
	if !strings.Contains(out, "[h2 message from: agent-a] Read /tmp/test-long.md") {
		t.Fatalf("expected file path reference for long body, got %q", out)
	}
	if strings.Contains(out, longBody) {
		t.Fatal("long body should not be inlined")
	}
}

func TestDeliver_InterAgentMessage_Interrupt(t *testing.T) {
	var buf threadSafeBuffer
	q := NewMessageQueue()
	stop := make(chan struct{})

	msg := &Message{
		ID:        "msg-2",
		From:      "agent-a",
		Priority:  PriorityInterrupt,
		Body:      "urgent task",
		FilePath:  "/tmp/test-urgent.md",
		Status:    StatusQueued,
		CreatedAt: time.Now(),
	}
	q.Enqueue(msg)

	delivered := make(chan struct{}, 1)
	go RunDelivery(DeliveryConfig{
		Queue:     q,
		PtyWriter: &buf,
		IsIdle:    func() bool { return true },
		WaitForIdle: func(ctx context.Context) bool {
			return true
		},
		OnDeliver: func() {
			select {
			case delivered <- struct{}{}:
			default:
			}
		},
		Stop: stop,
	})

	select {
	case <-delivered:
	case <-time.After(3 * time.Second):
		t.Fatal("delivery timed out")
	}
	close(stop)

	out := buf.String()
	if !strings.Contains(out, "[URGENT h2 message from: agent-a] urgent task") {
		t.Fatalf("expected URGENT h2 message header in output, got %q", out)
	}
}

func TestDeliver_InterruptRetry_IdleOnFirst(t *testing.T) {
	var buf threadSafeBuffer
	q := NewMessageQueue()
	stop := make(chan struct{})

	msg := &Message{
		ID:        "int-1",
		From:      "user",
		Priority:  PriorityInterrupt,
		Body:      "urgent",
		Status:    StatusQueued,
		CreatedAt: time.Now(),
	}
	q.Enqueue(msg)

	waitCalls := 0
	delivered := make(chan struct{}, 1)
	go RunDelivery(DeliveryConfig{
		Queue:     q,
		PtyWriter: &buf,
		IsIdle:    func() bool { return true },
		WaitForIdle: func(ctx context.Context) bool {
			waitCalls++
			return true // idle immediately
		},
		OnDeliver: func() {
			select {
			case delivered <- struct{}{}:
			default:
			}
		},
		Stop: stop,
	})

	select {
	case <-delivered:
	case <-time.After(3 * time.Second):
		t.Fatal("delivery timed out")
	}
	close(stop)

	out := buf.String()
	// Should have sent Ctrl+C.
	if !strings.Contains(out, "\x03") {
		t.Fatal("expected Ctrl+C in output")
	}
	// Should have sent the body.
	if !strings.Contains(out, "urgent") {
		t.Fatalf("expected body in output, got %q", out)
	}
	if waitCalls != 1 {
		t.Fatalf("expected 1 WaitForIdle call (idle on first), got %d", waitCalls)
	}
}

func TestDeliver_InterruptRetry_TriesThreeTimes(t *testing.T) {
	old := interruptWaitTimeout
	interruptWaitTimeout = 1 * time.Millisecond
	t.Cleanup(func() { interruptWaitTimeout = old })

	var buf threadSafeBuffer
	q := NewMessageQueue()
	stop := make(chan struct{})

	msg := &Message{
		ID:        "int-2",
		From:      "user",
		Priority:  PriorityInterrupt,
		Body:      "urgent",
		Status:    StatusQueued,
		CreatedAt: time.Now(),
	}
	q.Enqueue(msg)

	waitCalls := 0
	delivered := make(chan struct{}, 1)
	go RunDelivery(DeliveryConfig{
		Queue:     q,
		PtyWriter: &buf,
		IsIdle:    func() bool { return true },
		WaitForIdle: func(ctx context.Context) bool {
			waitCalls++
			// Never go idle — context will time out.
			<-ctx.Done()
			return false
		},
		OnDeliver: func() {
			select {
			case delivered <- struct{}{}:
			default:
			}
		},
		Stop: stop,
	})

	select {
	case <-delivered:
	case <-time.After(3 * time.Second):
		t.Fatal("delivery timed out")
	}
	close(stop)

	// Should have retried 3 times.
	if waitCalls != 3 {
		t.Fatalf("expected 3 WaitForIdle calls, got %d", waitCalls)
	}
	// Should still have delivered the message body.
	out := buf.String()
	if !strings.Contains(out, "urgent") {
		t.Fatalf("expected body in output after retries, got %q", out)
	}
	// Should have sent Ctrl+C 3 times.
	ctrlCCount := strings.Count(out, "\x03")
	if ctrlCCount != 3 {
		t.Fatalf("expected 3 Ctrl+C, got %d", ctrlCCount)
	}
}

func TestDeliver_InterruptCallsNoteInterrupt(t *testing.T) {
	var buf threadSafeBuffer
	q := NewMessageQueue()
	stop := make(chan struct{})

	msg := &Message{
		ID:        "int-ni-1",
		From:      "user",
		Priority:  PriorityInterrupt,
		Body:      "urgent",
		Status:    StatusQueued,
		CreatedAt: time.Now(),
	}
	q.Enqueue(msg)

	interruptCalls := 0
	delivered := make(chan struct{}, 1)
	go RunDelivery(DeliveryConfig{
		Queue:     q,
		PtyWriter: &buf,
		IsIdle:    func() bool { return true },
		WaitForIdle: func(ctx context.Context) bool {
			return true
		},
		NoteInterrupt: func() {
			interruptCalls++
		},
		OnDeliver: func() {
			select {
			case delivered <- struct{}{}:
			default:
			}
		},
		Stop: stop,
	})

	select {
	case <-delivered:
	case <-time.After(3 * time.Second):
		t.Fatal("delivery timed out")
	}
	close(stop)

	if interruptCalls != 1 {
		t.Fatalf("expected 1 NoteInterrupt call, got %d", interruptCalls)
	}
}

func TestDeliver_NormalDoesNotCallNoteInterrupt(t *testing.T) {
	var buf threadSafeBuffer
	q := NewMessageQueue()
	stop := make(chan struct{})

	msg := &Message{
		ID:        "n-ni-1",
		From:      "user",
		Priority:  PriorityNormal,
		Body:      "hello",
		Status:    StatusQueued,
		CreatedAt: time.Now(),
	}
	q.Enqueue(msg)

	interruptCalls := 0
	delivered := make(chan struct{}, 1)
	go RunDelivery(DeliveryConfig{
		Queue:     q,
		PtyWriter: &buf,
		IsIdle:    func() bool { return true },
		NoteInterrupt: func() {
			interruptCalls++
		},
		OnDeliver: func() {
			select {
			case delivered <- struct{}{}:
			default:
			}
		},
		Stop: stop,
	})

	select {
	case <-delivered:
	case <-time.After(3 * time.Second):
		t.Fatal("delivery timed out")
	}
	close(stop)

	if interruptCalls != 0 {
		t.Fatalf("NoteInterrupt should not be called for normal priority, got %d calls", interruptCalls)
	}
}

func TestEnqueueRaw_BypassesBlockedAndNoPrefix(t *testing.T) {
	var buf threadSafeBuffer
	q := NewMessageQueue()
	stop := make(chan struct{})

	// EnqueueRaw should create a message with interrupt priority and no file path.
	id := EnqueueRaw(q, "y")
	if id == "" {
		t.Fatal("expected non-empty message ID")
	}

	delivered := make(chan struct{}, 1)
	go RunDelivery(DeliveryConfig{
		Queue:     q,
		PtyWriter: &buf,
		IsIdle:    func() bool { return false },
		IsBlocked: func() bool { return true }, // agent is blocked on permission
		WaitForIdle: func(ctx context.Context) bool {
			return true // go idle immediately after Ctrl+C
		},
		OnDeliver: func() {
			select {
			case delivered <- struct{}{}:
			default:
			}
		},
		Stop: stop,
	})

	select {
	case <-delivered:
	case <-time.After(3 * time.Second):
		t.Fatal("delivery timed out — raw message should bypass blocked check")
	}
	close(stop)

	out := buf.String()
	// Should NOT contain Ctrl+C despite using interrupt priority.
	if strings.Contains(out, "\x03") {
		t.Fatal("raw message should not send Ctrl+C")
	}
	// Should NOT contain the [h2 message from: ...] prefix.
	if strings.Contains(out, "[h2 message") || strings.Contains(out, "[URGENT") {
		t.Fatalf("raw message should not have prefix, got %q", out)
	}
	// Should contain the body followed by \r.
	if !strings.Contains(out, "y") {
		t.Fatalf("expected body 'y' in output, got %q", out)
	}
	if !strings.HasSuffix(out, "\r") {
		t.Fatal("expected output to end with \\r")
	}
}

func TestDeliver_NormalNoWaitForIdle(t *testing.T) {
	var buf threadSafeBuffer
	q := NewMessageQueue()
	stop := make(chan struct{})

	msg := &Message{
		ID:        "n-1",
		From:      "user",
		Priority:  PriorityNormal,
		Body:      "hello",
		Status:    StatusQueued,
		CreatedAt: time.Now(),
	}
	q.Enqueue(msg)

	waitCalls := 0
	delivered := make(chan struct{}, 1)
	go RunDelivery(DeliveryConfig{
		Queue:     q,
		PtyWriter: &buf,
		IsIdle:    func() bool { return true },
		WaitForIdle: func(ctx context.Context) bool {
			waitCalls++
			return true
		},
		OnDeliver: func() {
			select {
			case delivered <- struct{}{}:
			default:
			}
		},
		Stop: stop,
	})

	select {
	case <-delivered:
	case <-time.After(3 * time.Second):
		t.Fatal("delivery timed out")
	}
	close(stop)

	if waitCalls != 0 {
		t.Fatalf("WaitForIdle should not be called for normal priority, got %d calls", waitCalls)
	}
}
