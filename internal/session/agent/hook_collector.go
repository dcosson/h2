package agent

import (
	"encoding/json"
	"sync"
	"time"
)

// HookCollector accumulates lifecycle data from Claude Code hooks.
// It is a pure data collector — it receives events, updates internal state,
// and signals an event channel. It knows nothing about idle/active state.
type HookCollector struct {
	mu                  sync.RWMutex
	lastEvent           string
	lastEventTime       time.Time
	lastToolName        string
	toolUseCount        int64
	blockedOnPermission bool
	blockedToolName     string
	eventCh             chan string // sends event name so Agent can interpret
}

// NewHookCollector creates a new HookCollector.
func NewHookCollector() *HookCollector {
	return &HookCollector{
		eventCh: make(chan string, 1),
	}
}

// EventCh returns the channel that receives hook event names.
func (c *HookCollector) EventCh() <-chan string {
	return c.eventCh
}

// ProcessEvent records a hook event and sends the event name to the Agent.
func (c *HookCollector) ProcessEvent(eventName string, payload json.RawMessage) {
	c.mu.Lock()
	c.lastEvent = eventName
	c.lastEventTime = time.Now()
	if eventName == "PreToolUse" || eventName == "PostToolUse" {
		c.lastToolName = extractToolName(payload)
	}
	if eventName == "PreToolUse" {
		c.toolUseCount++
	}

	// Handle blocked_permission: set blocked state.
	if eventName == "blocked_permission" {
		c.blockedOnPermission = true
		c.blockedToolName = extractToolName(payload)
	}

	// Clear blocked state on events that indicate the agent has resumed.
	if c.blockedOnPermission && eventName != "blocked_permission" && eventName != "PermissionRequest" {
		c.blockedOnPermission = false
		c.blockedToolName = ""
	}
	c.mu.Unlock()

	// Send event name to Agent's state watcher (non-blocking).
	select {
	case c.eventCh <- eventName:
	default:
	}
}

// State returns a point-in-time snapshot of the hook collector's data.
func (c *HookCollector) State() HookState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return HookState{
		LastEvent:           c.lastEvent,
		LastEventTime:       c.lastEventTime,
		LastToolName:        c.lastToolName,
		ToolUseCount:        c.toolUseCount,
		BlockedOnPermission: c.blockedOnPermission,
		BlockedToolName:     c.blockedToolName,
	}
}

// HookState is a point-in-time snapshot of hook collector data.
type HookState struct {
	LastEvent           string
	LastEventTime       time.Time
	LastToolName        string
	ToolUseCount        int64
	BlockedOnPermission bool
	BlockedToolName     string
}

// hookPayload is used to extract fields from the hook JSON payload.
type hookPayload struct {
	ToolName string `json:"tool_name"`
}

// extractToolName pulls the tool_name field from a hook payload.
func extractToolName(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var p hookPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	return p.ToolName
}
