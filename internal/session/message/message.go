package message

import (
	"time"
)

// Priority defines the delivery priority of a message.
type Priority int

const (
	PriorityInterrupt Priority = 1
	PriorityNormal    Priority = 2
	PriorityIdleFirst Priority = 3
	PriorityIdle      Priority = 4
)

// ParsePriority converts a string to a Priority value.
func ParsePriority(s string) (Priority, bool) {
	switch s {
	case "interrupt":
		return PriorityInterrupt, true
	case "normal":
		return PriorityNormal, true
	case "idle-first":
		return PriorityIdleFirst, true
	case "idle":
		return PriorityIdle, true
	default:
		return 0, false
	}
}

// String returns the string representation of a Priority.
func (p Priority) String() string {
	switch p {
	case PriorityInterrupt:
		return "interrupt"
	case PriorityNormal:
		return "normal"
	case PriorityIdleFirst:
		return "idle-first"
	case PriorityIdle:
		return "idle"
	default:
		return "unknown"
	}
}

// MessageStatus tracks the delivery state of a message.
type MessageStatus string

const (
	StatusQueued    MessageStatus = "queued"
	StatusDelivered MessageStatus = "delivered"
)

// Message represents a queued inter-agent message.
type Message struct {
	ID          string
	From        string
	Priority    Priority
	Body        string
	FilePath    string
	Status      MessageStatus
	CreatedAt   time.Time
	DeliveredAt *time.Time
}
