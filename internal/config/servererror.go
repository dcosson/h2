package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// ServerErrorFileName is the name of the server error tracking file within a
// harness profile directory (e.g. claude-config/default/servererror.json).
const ServerErrorFileName = "servererror.json"

// ServerErrorInfo records when a profile encountered an API server error (5xx).
// Unlike auth errors, server errors auto-clear when the next successful API
// response is received.
type ServerErrorInfo struct {
	StatusCode string    `json:"status_code,omitempty"` // HTTP status code (e.g. "500")
	Message    string    `json:"message,omitempty"`     // raw error message from the harness
	RecordedAt time.Time `json:"recorded_at"`           // when we recorded this
	AgentName  string    `json:"agent_name,omitempty"`  // which agent hit the error
}

// WriteServerError writes server error info to the profile's servererror.json.
// profileDir is the harness-specific profile directory
// (e.g. <h2dir>/claude-config/<profile>).
func WriteServerError(profileDir string, info *ServerErrorInfo) error {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(profileDir, ServerErrorFileName), data, 0o644)
}

// ReadServerError reads server error info from a profile directory.
// Returns nil, nil if the file does not exist.
func ReadServerError(profileDir string) (*ServerErrorInfo, error) {
	data, err := os.ReadFile(filepath.Join(profileDir, ServerErrorFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var info ServerErrorInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// IsProfileServerError checks if a profile has a recorded server error.
// Returns the ServerErrorInfo if the file exists, or nil if not.
func IsProfileServerError(profileDir string) *ServerErrorInfo {
	info, err := ReadServerError(profileDir)
	if err != nil || info == nil {
		return nil
	}
	return info
}

// ClearServerError removes the servererror.json file from a profile directory.
func ClearServerError(profileDir string) error {
	err := os.Remove(filepath.Join(profileDir, ServerErrorFileName))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
