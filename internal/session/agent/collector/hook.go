package collector

import (
	"encoding/json"
	"sync"
	"time"

	"h2/internal/activitylog"
)

// HookCollector accumulates lifecycle data from Claude Code hooks and
// derives active/idle/exited state from hook event names.
//
// Unlike OutputCollector and OtelCollector, HookCollector has no idle timer —
// hooks provide precise start/stop signals so state is derived directly from
// event names.
type HookCollector struct {
	mu                  sync.RWMutex
	lastEvent           string
	lastEventTime       time.Time
	lastToolName        string
	toolUseCount        int64
	blockedOnPermission bool
	blockedToolName     string
	compacting          bool

	activityCh  chan string // internal: event names for state derivation
	stateCh     chan StateUpdate
	stopCh      chan struct{}
	activityLog *activitylog.Logger
}

// NewHookCollector creates and starts a HookCollector.
func NewHookCollector(log *activitylog.Logger) *HookCollector {
	if log == nil {
		log = activitylog.Nop()
	}
	c := &HookCollector{
		activityCh:  make(chan string, 8),
		stateCh:     make(chan StateUpdate, 1),
		stopCh:      make(chan struct{}),
		activityLog: log,
	}
	go c.runStateLoop()
	return c
}

// StateCh returns the channel that receives state updates.
func (c *HookCollector) StateCh() <-chan StateUpdate {
	return c.stateCh
}

// Stop stops the internal state derivation goroutine.
func (c *HookCollector) Stop() {
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
}

// NoteInterrupt signals that a Ctrl+C was sent to the child process.
// This is treated like a Stop event — the agent is assumed to return to
// its prompt after an interrupt.
func (c *HookCollector) NoteInterrupt() {
	c.mu.Lock()
	c.lastEvent = "Interrupt"
	c.lastEventTime = time.Now()
	c.blockedOnPermission = false
	c.blockedToolName = ""
	c.mu.Unlock()

	select {
	case c.activityCh <- "Interrupt":
	default:
	}
}

// ProcessEvent records a hook event and sends it for state derivation.
func (c *HookCollector) ProcessEvent(eventName string, payload json.RawMessage) {
	toolName := extractToolName(payload)
	sessionID := extractSessionID(payload)

	c.mu.Lock()
	c.lastEvent = eventName
	c.lastEventTime = time.Now()
	if eventName == "PreToolUse" || eventName == "PostToolUse" {
		c.lastToolName = toolName
	}
	if eventName == "PreToolUse" {
		c.toolUseCount++
	}

	// Handle permission_decision: update blocked state based on decision,
	// and derive a synthetic event name for the state loop.
	stateEvent := eventName
	if eventName == "permission_decision" {
		decision := extractDecision(payload)
		reason := extractReason(payload)
		c.activityLog.PermissionDecision(sessionID, toolName, decision, reason)
		if decision == "ask_user" {
			c.blockedOnPermission = true
			c.blockedToolName = toolName
		} else {
			c.blockedOnPermission = false
			c.blockedToolName = ""
			// Permission granted — the tool is about to execute.
			stateEvent = "permission_allow"
		}
	} else {
		c.activityLog.HookEvent(sessionID, eventName, toolName)
	}

	// Legacy: handle blocked_permission for backward compatibility.
	if eventName == "blocked_permission" {
		c.blockedOnPermission = true
		c.blockedToolName = toolName
	}

	// Track compaction state.
	if eventName == "PreCompact" {
		c.compacting = true
	} else if c.compacting && eventName != "permission_decision" {
		// Any non-permission event after PreCompact means compaction finished.
		// SessionStart is the typical "compaction done" signal.
		c.compacting = false
	}

	// Clear blocked state on events that indicate the agent has resumed.
	if c.blockedOnPermission &&
		eventName != "blocked_permission" &&
		eventName != "permission_decision" &&
		eventName != "PermissionRequest" {
		c.blockedOnPermission = false
		c.blockedToolName = ""
	}
	c.mu.Unlock()

	// Send event name to state derivation loop (non-blocking).
	select {
	case c.activityCh <- stateEvent:
	default:
	}
}

// Snapshot returns a point-in-time snapshot of the hook collector's data.
func (c *HookCollector) Snapshot() HookState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return HookState{
		LastEvent:           c.lastEvent,
		LastEventTime:       c.lastEventTime,
		LastToolName:        c.lastToolName,
		ToolUseCount:        c.toolUseCount,
		BlockedOnPermission: c.blockedOnPermission,
		BlockedToolName:     c.blockedToolName,
		Compacting:          c.compacting,
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
	Compacting          bool
}

// SubState derives the agent sub-state from this snapshot.
func (hs HookState) SubState() SubState {
	if hs.Compacting {
		return SubStateCompacting
	}
	if hs.BlockedOnPermission {
		return SubStateWaitingForPermission
	}
	switch hs.LastEvent {
	case "UserPromptSubmit", "PostToolUse":
		return SubStateThinking
	case "PreToolUse":
		return SubStateToolUse
	case "PermissionRequest":
		return SubStateWaitingForPermission
	default:
		return SubStateNone
	}
}

// runStateLoop derives active/idle/exited state from hook event names.
// No idle timer — hooks provide precise signals.
// Emits on every event because sub-state can change without state changing
// (e.g. permission_decision → WaitingForPermission).
func (c *HookCollector) runStateLoop() {
	currentState := StateInitialized
	for {
		select {
		case eventName := <-c.activityCh:
			switch eventName {
			case "UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest", "permission_allow", "PreCompact":
				currentState = StateActive
			case "SessionStart", "Stop", "Interrupt":
				currentState = StateIdle
			case "SessionEnd":
				currentState = StateExited
			}
			// Always emit: sub-state may have changed even if state didn't.
			subState := c.Snapshot().SubState()
			c.send(StateUpdate{State: currentState, SubState: subState})
		case <-c.stopCh:
			return
		}
	}
}

// send delivers the latest StateUpdate using drain-and-replace to ensure
// the consumer always sees the most recent state.
func (c *HookCollector) send(su StateUpdate) {
	select {
	case <-c.stateCh:
	default:
	}
	c.stateCh <- su
}

// --- Payload extraction helpers ---

// hookPayload is used to extract fields from the hook JSON payload.
type hookPayload struct {
	ToolName  string `json:"tool_name"`
	SessionID string `json:"session_id"`
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`
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

// extractSessionID pulls the session_id field from a hook payload.
func extractSessionID(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var p hookPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	return p.SessionID
}

// extractDecision pulls the decision field from a hook payload.
func extractDecision(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var p hookPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	return p.Decision
}

// extractReason pulls the reason field from a hook payload.
func extractReason(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var p hookPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	return p.Reason
}
