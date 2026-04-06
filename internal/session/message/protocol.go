package message

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
)

// Request is the JSON request sent over the Unix socket.
type Request struct {
	Type string `json:"type"` // "send", "attach", "show", "status", "hook_event", "stop", "relaunch", "trigger_add", "trigger_list", "trigger_remove", "schedule_add", "schedule_list", "schedule_remove"

	// send fields
	Priority        string `json:"priority,omitempty"`
	From            string `json:"from,omitempty"`
	Body            string `json:"body,omitempty"`
	Raw             bool   `json:"raw,omitempty"`              // send body directly to PTY without prefix
	ExpectsResponse bool   `json:"expects_response,omitempty"` // sender expects a response (adds annotation)
	ERTriggerID     string `json:"er_trigger_id,omitempty"`    // trigger ID for expects-response annotation

	// attach fields
	Cols      int    `json:"cols,omitempty"`
	Rows      int    `json:"rows,omitempty"`
	OscFg     string `json:"osc_fg,omitempty"`    // X11 rgb:rrrr/gggg/bbbb
	OscBg     string `json:"osc_bg,omitempty"`    // X11 rgb:rrrr/gggg/bbbb
	ColorFGBG string `json:"colorfgbg,omitempty"` // terminal COLORFGBG hint

	// show fields
	MessageID string `json:"message_id,omitempty"`

	// relaunch fields
	Resume bool `json:"resume,omitempty"` // set ResumeSessionID from HarnessSessionID
	Rotate bool `json:"rotate,omitempty"` // true when relaunch is due to profile rotation

	// hook_event fields
	EventName string          `json:"event_name,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`

	// trigger fields
	Trigger   *TriggerSpec `json:"trigger,omitempty"`
	TriggerID string       `json:"trigger_id,omitempty"`

	// schedule fields
	Schedule   *ScheduleSpec `json:"schedule,omitempty"`
	ScheduleID string        `json:"schedule_id,omitempty"`
}

// TriggerSpec is the wire representation of a trigger for socket requests/responses.
type TriggerSpec struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Event     string `json:"event"`
	State     string `json:"state,omitempty"`
	SubState  string `json:"sub_state,omitempty"`
	Condition string `json:"condition,omitempty"`
	Exec      string `json:"exec,omitempty"`
	Message   string `json:"message,omitempty"`
	From      string `json:"from,omitempty"`
	Priority  string `json:"priority,omitempty"`

	// Lifecycle control (repeating triggers).
	MaxFirings int    `json:"max_firings,omitempty"` // -1=unlimited, 0=default (one-shot)
	ExpiresAt  string `json:"expires_at,omitempty"`  // RFC 3339 timestamp
	Cooldown   string `json:"cooldown,omitempty"`    // Go duration string (e.g. "5m", "30s")

	// Read-only in responses (trigger_list).
	FireCount   int    `json:"fire_count,omitempty"`
	LastFiredAt string `json:"last_fired_at,omitempty"` // RFC 3339
}

// ScheduleSpec is the wire representation of a schedule for socket requests/responses.
type ScheduleSpec struct {
	ID            string `json:"id,omitempty"`
	Name          string `json:"name,omitempty"`
	Start         string `json:"start,omitempty"`
	RRule         string `json:"rrule"`
	Condition     string `json:"condition,omitempty"`
	ConditionMode string `json:"condition_mode,omitempty"`
	Exec          string `json:"exec,omitempty"`
	Message       string `json:"message,omitempty"`
	From          string `json:"from,omitempty"`
	Priority      string `json:"priority,omitempty"`
	NextFireAt    string `json:"next_fire_at,omitempty"` // RFC 3339, computed at list time
}

// Response is the JSON response sent back over the Unix socket.
type Response struct {
	OK           bool         `json:"ok"`
	Error        string       `json:"error,omitempty"`
	MessageID    string       `json:"message_id,omitempty"`
	OldConcierge string       `json:"old_concierge,omitempty"`
	Message      *MessageInfo `json:"message,omitempty"`
	Agent        *AgentInfo   `json:"agent,omitempty"`
	Bridge       *BridgeInfo  `json:"bridge,omitempty"`

	// trigger/schedule responses
	TriggerID  string          `json:"trigger_id,omitempty"`
	Triggers   []*TriggerSpec  `json:"triggers,omitempty"`
	ScheduleID string          `json:"schedule_id,omitempty"`
	Schedules  []*ScheduleSpec `json:"schedules,omitempty"`
}

// BridgeInfo is the public representation of bridge status.
type BridgeInfo struct {
	Name             string   `json:"name"`
	Pod              string   `json:"pod,omitempty"` // pod name if launched from a pod
	Channels         []string `json:"channels"`
	Uptime           string   `json:"uptime"`
	MessagesSent     int64    `json:"messages_sent"`
	MessagesReceived int64    `json:"messages_received"`
	LastActivity     string   `json:"last_activity,omitempty"` // duration since last message, empty if none
}

// MessageInfo is the public representation of a message in responses.
type MessageInfo struct {
	ID          string `json:"id"`
	From        string `json:"from"`
	Priority    string `json:"priority"`
	Status      string `json:"status"`
	FilePath    string `json:"file_path"`
	CreatedAt   string `json:"created_at"`
	DeliveredAt string `json:"delivered_at,omitempty"`
}

// AgentInfo is the public representation of agent status.
type AgentInfo struct {
	Name             string `json:"name"`
	Command          string `json:"command"`
	SessionID        string `json:"session_id,omitempty"`
	Profile          string `json:"profile,omitempty"`
	RoleName         string `json:"role,omitempty"`
	Pod              string `json:"pod,omitempty"`
	PodIndex         int    `json:"pod_index,omitempty"` // position in pod YAML agent list (0-based)
	Uptime           string `json:"uptime"`
	LastActivity     string `json:"last_activity,omitempty"` // duration since last agent activity
	State            string `json:"state"`
	SubState         string `json:"sub_state,omitempty"`
	StateDisplayText string `json:"state_display_text"`
	StateDuration    string `json:"state_duration"`
	QueuedCount      int    `json:"queued_count"`

	// Per-model cost and token breakdowns from OTEL metrics
	ModelStats   []ModelStat `json:"model_stats,omitempty"`
	InputTokens  int64       `json:"input_tokens,omitempty"`
	OutputTokens int64       `json:"output_tokens,omitempty"`
	TotalTokens  int64       `json:"total_tokens,omitempty"`
	TotalCostUSD float64     `json:"total_cost_usd,omitempty"`

	// Cumulative session LOC from OTEL metrics
	LinesAdded   int64 `json:"lines_added,omitempty"`
	LinesRemoved int64 `json:"lines_removed,omitempty"`

	// Per-tool counts from OTEL logs
	ToolCounts map[string]int64 `json:"tool_counts,omitempty"`

	// Point-in-time git working tree stats
	GitFilesChanged int   `json:"git_files_changed,omitempty"`
	GitLinesAdded   int64 `json:"git_lines_added,omitempty"`
	GitLinesRemoved int64 `json:"git_lines_removed,omitempty"`

	// Hook collector data (omitted if collector not active)
	LastToolUse         string `json:"last_tool_use,omitempty"`
	ToolUseCount        int64  `json:"tool_use_count,omitempty"`
	BlockedOnPermission bool   `json:"blocked_on_permission,omitempty"`
	BlockedToolName     string `json:"blocked_tool_name,omitempty"`

	// Usage limit info (populated when sub_state is usage_limit)
	UsageLimitResetsAt string `json:"usage_limit_resets_at,omitempty"` // RFC3339 timestamp
	UsageLimitMessage  string `json:"usage_limit_message,omitempty"`

	// Auth error info (populated when sub_state is auth_error)
	AuthErrorMessage string `json:"auth_error_message,omitempty"`
}

// ModelStat holds per-model cost and token breakdown.
type ModelStat struct {
	Model        string  `json:"model"`
	CostUSD      float64 `json:"cost_usd"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CacheRead    int64   `json:"cache_read,omitempty"`
	CacheCreate  int64   `json:"cache_create,omitempty"`
}

// Attach frame types.
const (
	FrameTypeData    byte = 0x00
	FrameTypeControl byte = 0x01
)

// ResizeControl is the JSON payload for a resize control frame.
type ResizeControl struct {
	Type string `json:"type"` // "resize"
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// SendRequest sends a JSON-encoded request over a connection.
func SendRequest(conn net.Conn, req *Request) error {
	return json.NewEncoder(conn).Encode(req)
}

// ReadRequest reads a JSON-encoded request from a connection.
func ReadRequest(conn net.Conn) (*Request, error) {
	var req Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		return nil, err
	}
	return &req, nil
}

// SendResponse sends a JSON-encoded response over a connection.
func SendResponse(conn net.Conn, resp *Response) error {
	return json.NewEncoder(conn).Encode(resp)
}

// ReadResponse reads a JSON-encoded response from a connection.
func ReadResponse(conn net.Conn) (*Response, error) {
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// WriteFrame writes a framed message: [1 byte type][4 bytes big-endian length][payload].
func WriteFrame(w io.Writer, frameType byte, payload []byte) error {
	header := make([]byte, 5)
	header[0] = frameType
	binary.BigEndian.PutUint32(header[1:5], uint32(len(payload)))
	if _, err := w.Write(header); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

// ReadFrame reads a framed message. Returns the frame type and payload.
func ReadFrame(r io.Reader) (byte, []byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	frameType := header[0]
	length := binary.BigEndian.Uint32(header[1:5])
	if length > 10*1024*1024 { // 10MB sanity limit
		return 0, nil, fmt.Errorf("frame too large: %d bytes", length)
	}
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return 0, nil, err
		}
	}
	return frameType, payload, nil
}
