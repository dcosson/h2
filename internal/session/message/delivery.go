package message

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// IdleFunc returns true if the child process is considered idle.
type IdleFunc func() bool

// WaitForIdleFunc blocks until the child process is idle or ctx is cancelled.
// Returns true if idle was reached.
type WaitForIdleFunc func(ctx context.Context) bool

// IsBlockedFunc returns true if the agent is blocked (e.g. waiting for
// permission approval) and normal-priority messages should not be delivered.
type IsBlockedFunc func() bool

// DeliveryConfig holds configuration for the delivery goroutine.
type DeliveryConfig struct {
	Queue       *MessageQueue
	AgentName   string
	PtyWriter   io.Writer        // writes to the child PTY
	IsIdle      IdleFunc         // checks if child is idle
	IsBlocked   IsBlockedFunc    // checks if agent is blocked (nil = never blocked)
	WaitForIdle WaitForIdleFunc  // blocks until idle (for interrupt retry)
	OnDeliver   func()           // called after each delivery (e.g. to render)
	Stop        <-chan struct{}
}

// PrepareMessage creates a Message, writes its body to disk, and enqueues it.
// Returns the message ID.
func PrepareMessage(q *MessageQueue, agentName, from, body string, priority Priority) (string, error) {
	id := uuid.New().String()
	now := time.Now()

	dir := filepath.Join(os.Getenv("HOME"), ".h2", "messages", agentName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create message dir: %w", err)
	}

	filename := fmt.Sprintf("%s-%s.md", now.Format("20060102-150405"), id[:8])
	filePath := filepath.Join(dir, filename)
	if err := os.WriteFile(filePath, []byte(body), 0o600); err != nil {
		return "", fmt.Errorf("write message file: %w", err)
	}

	msg := &Message{
		ID:        id,
		From:      from,
		Priority:  priority,
		Body:      body,
		FilePath:  filePath,
		Status:    StatusQueued,
		CreatedAt: now,
	}
	q.Enqueue(msg)
	return id, nil
}

// RunDelivery runs the delivery loop that drains the queue and writes to the PTY.
// It blocks until cfg.Stop is closed.
func RunDelivery(cfg DeliveryConfig) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-cfg.Stop:
			return
		case <-cfg.Queue.Notify():
		case <-ticker.C:
		}

		for {
			idle := cfg.IsIdle != nil && cfg.IsIdle()
			blocked := cfg.IsBlocked != nil && cfg.IsBlocked()
			msg := cfg.Queue.Dequeue(idle, blocked)
			if msg == nil {
				break
			}
			deliver(cfg, msg)
		}
	}
}

const (
	interruptRetries       = 3
	interruptWaitTimeout   = 5 * time.Second
	maxInlineBodyLen       = 300
)

func deliver(cfg DeliveryConfig, msg *Message) {
	if msg.Priority == PriorityInterrupt {
		// Send Ctrl+C, wait for idle, retry up to 3 times.
		// If still not idle after retries, send anyway (like normal).
		for attempt := 0; attempt < interruptRetries; attempt++ {
			cfg.PtyWriter.Write([]byte{0x03})
			if cfg.WaitForIdle != nil {
				ctx, cancel := context.WithTimeout(context.Background(), interruptWaitTimeout)
				idle := cfg.WaitForIdle(ctx)
				cancel()
				if idle {
					break
				}
			} else {
				time.Sleep(200 * time.Millisecond)
				break
			}
		}
	}

	if msg.FilePath == "" {
		// Raw user input — send body directly.
		cfg.PtyWriter.Write([]byte(msg.Body))
	} else {
		// Inter-agent message — inline short messages, reference long ones.
		prefix := "h2 message"
		if msg.Priority == PriorityInterrupt {
			prefix = "URGENT h2 message"
		}
		var line string
		if len(msg.Body) <= maxInlineBodyLen {
			line = fmt.Sprintf("[%s from: %s] %s",
				prefix, msg.From, msg.Body)
		} else {
			line = fmt.Sprintf("[%s from: %s] Read %s",
				prefix, msg.From, msg.FilePath)
		}
		cfg.PtyWriter.Write([]byte(line))
	}
	// Delay before sending Enter so the child's UI framework can process
	// the typed text before the submit (same pattern as user Enter).
	time.Sleep(50 * time.Millisecond)
	cfg.PtyWriter.Write([]byte{'\r'})

	now := time.Now()
	msg.Status = StatusDelivered
	msg.DeliveredAt = &now

	if cfg.OnDeliver != nil {
		cfg.OnDeliver()
	}
}
