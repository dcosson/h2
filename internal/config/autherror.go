package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// AuthErrorFileName is the name of the auth error tracking file within a
// harness profile directory (e.g. claude-config/default/autherror.json).
const AuthErrorFileName = "autherror.json"

// AuthErrorInfo records when a profile encountered an authentication error.
type AuthErrorInfo struct {
	Message    string    `json:"message,omitempty"`    // raw error message from the harness
	RecordedAt time.Time `json:"recorded_at"`          // when we recorded this
	AgentName  string    `json:"agent_name,omitempty"` // which agent hit the error
}

// WriteAuthError writes auth error info to the profile's autherror.json.
// profileDir is the harness-specific profile directory
// (e.g. <h2dir>/claude-config/<profile>).
func WriteAuthError(profileDir string, info *AuthErrorInfo) error {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(profileDir, AuthErrorFileName), data, 0o644)
}

// ReadAuthError reads auth error info from a profile directory.
// Returns nil, nil if the file does not exist.
func ReadAuthError(profileDir string) (*AuthErrorInfo, error) {
	data, err := os.ReadFile(filepath.Join(profileDir, AuthErrorFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var info AuthErrorInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// IsProfileAuthError checks if a profile has a recorded auth error.
// Returns the AuthErrorInfo if the file exists, or nil if not.
// Unlike rate limits, auth errors do not expire — they require user
// action (re-authentication via h2 auth claude).
func IsProfileAuthError(profileDir string) *AuthErrorInfo {
	info, err := ReadAuthError(profileDir)
	if err != nil || info == nil {
		return nil
	}
	return info
}

// ClearAuthError removes the autherror.json file from a profile directory.
func ClearAuthError(profileDir string) error {
	err := os.Remove(filepath.Join(profileDir, AuthErrorFileName))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
