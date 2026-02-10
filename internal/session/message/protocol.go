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
	Type string `json:"type"` // "send", "attach", "show", "status", "hook_event"

	// send fields
	Priority string `json:"priority,omitempty"`
	From     string `json:"from,omitempty"`
	Body     string `json:"body,omitempty"`

	// attach fields
	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`

	// show fields
	MessageID string `json:"message_id,omitempty"`

	// hook_event fields
	EventName string          `json:"event_name,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// Response is the JSON response sent back over the Unix socket.
type Response struct {
	OK        bool         `json:"ok"`
	Error     string       `json:"error,omitempty"`
	MessageID string       `json:"message_id,omitempty"`
	Message   *MessageInfo `json:"message,omitempty"`
	Agent     *AgentInfo   `json:"agent,omitempty"`
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
	Name          string `json:"name"`
	Command       string `json:"command"`
	SessionID     string `json:"session_id,omitempty"`
	RoleName      string `json:"role,omitempty"`
	Uptime        string `json:"uptime"`
	State            string `json:"state"`
	SubState         string `json:"sub_state,omitempty"`
	StateDisplayText string `json:"state_display_text"`
	StateDuration    string `json:"state_duration"`
	QueuedCount   int    `json:"queued_count"`

	// Per-model cost and token breakdowns from OTEL metrics
	ModelStats   []ModelStat `json:"model_stats,omitempty"`
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
