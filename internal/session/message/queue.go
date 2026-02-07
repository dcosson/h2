package message

import (
	"sync"
)

// MessageQueue is a priority queue for inter-agent messages.
// Messages are ordered by priority: interrupt > normal > idle-first > idle.
// Within each priority level, messages are FIFO except idle-first which
// prepends (most recent first).
type MessageQueue struct {
	mu          sync.Mutex
	interrupt   []*Message        // priority 1 - FIFO
	normal      []*Message        // priority 2 - FIFO
	idleFirst   []*Message        // priority 3 - prepended (most recent first)
	idle        []*Message        // priority 4 - FIFO
	allMessages map[string]*Message
	paused      bool
	notify      chan struct{}
}

// NewMessageQueue creates a new empty message queue.
func NewMessageQueue() *MessageQueue {
	return &MessageQueue{
		allMessages: make(map[string]*Message),
		notify:      make(chan struct{}, 1),
	}
}

// Enqueue adds a message to the appropriate sub-queue and signals the
// delivery goroutine.
func (q *MessageQueue) Enqueue(msg *Message) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.allMessages[msg.ID] = msg

	switch msg.Priority {
	case PriorityInterrupt:
		q.interrupt = append(q.interrupt, msg)
	case PriorityNormal:
		q.normal = append(q.normal, msg)
	case PriorityIdleFirst:
		q.idleFirst = append([]*Message{msg}, q.idleFirst...)
	case PriorityIdle:
		q.idle = append(q.idle, msg)
	}

	q.signal()
}

// Dequeue returns the next message to deliver based on priority ordering.
// If idle is false, only interrupt and normal messages are returned.
// Returns nil if no deliverable message is available.
func (q *MessageQueue) Dequeue(idle bool) *Message {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.paused {
		// Interrupt bypasses pause.
		if len(q.interrupt) > 0 {
			msg := q.interrupt[0]
			q.interrupt = q.interrupt[1:]
			return msg
		}
		return nil
	}

	if len(q.interrupt) > 0 {
		msg := q.interrupt[0]
		q.interrupt = q.interrupt[1:]
		return msg
	}
	if len(q.normal) > 0 {
		msg := q.normal[0]
		q.normal = q.normal[1:]
		return msg
	}
	if idle {
		if len(q.idleFirst) > 0 {
			msg := q.idleFirst[0]
			q.idleFirst = q.idleFirst[1:]
			return msg
		}
		if len(q.idle) > 0 {
			msg := q.idle[0]
			q.idle = q.idle[1:]
			return msg
		}
	}
	return nil
}

// Pause pauses delivery of non-interrupt messages.
func (q *MessageQueue) Pause() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.paused = true
}

// Unpause resumes delivery and signals the delivery goroutine.
func (q *MessageQueue) Unpause() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.paused = false
	q.signal()
}

// IsPaused returns whether the queue is paused.
func (q *MessageQueue) IsPaused() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.paused
}

// Lookup returns a message by ID.
func (q *MessageQueue) Lookup(id string) *Message {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.allMessages[id]
}

// PendingCount returns the number of undelivered messages.
func (q *MessageQueue) PendingCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.interrupt) + len(q.normal) + len(q.idleFirst) + len(q.idle)
}

// Notify returns the channel that is signaled on enqueue or unpause.
func (q *MessageQueue) Notify() <-chan struct{} {
	return q.notify
}

func (q *MessageQueue) signal() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}
