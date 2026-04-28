package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// RateLimitFileName is the name of the rate limit tracking file within a
// harness profile directory (e.g. claude-config/default/ratelimit.json).
const RateLimitFileName = "ratelimit.json"

// RateLimitInfo records when a profile hit a usage/rate limit.
type RateLimitInfo struct {
	ResetsAt   time.Time `json:"resets_at"`            // when the limit resets
	Message    string    `json:"message,omitempty"`    // raw message from the harness
	RecordedAt time.Time `json:"recorded_at"`          // when we recorded this
	AgentName  string    `json:"agent_name,omitempty"` // which agent hit the limit
}

// WriteRateLimit writes rate limit info to the profile's ratelimit.json.
// profileDir is the harness-specific profile directory
// (e.g. <h2dir>/claude-config/<profile>).
func WriteRateLimit(profileDir string, info *RateLimitInfo) error {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(profileDir, RateLimitFileName), data, 0o644)
}

// ReadRateLimit reads rate limit info from a profile directory.
// Returns nil, nil if the file does not exist.
func ReadRateLimit(profileDir string) (*RateLimitInfo, error) {
	data, err := os.ReadFile(filepath.Join(profileDir, RateLimitFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var info RateLimitInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// IsProfileRateLimited checks if a profile is currently rate limited.
// Returns the RateLimitInfo if the limit is still active. A zero resets_at is
// treated as an indefinite limit because some harness errors do not include a
// reset timestamp.
func IsProfileRateLimited(profileDir string) *RateLimitInfo {
	info, err := ReadRateLimit(profileDir)
	if err != nil || info == nil {
		return nil
	}
	if info.ResetsAt.IsZero() {
		return info
	}
	if time.Now().Before(info.ResetsAt) {
		return info
	}
	// Expired — clean up the stale file. Best-effort: if removal fails
	// the next call will still see it as expired and try again.
	_ = ClearRateLimit(profileDir)
	return nil
}

// ClearRateLimit removes the ratelimit.json file from a profile directory.
func ClearRateLimit(profileDir string) error {
	err := os.Remove(filepath.Join(profileDir, RateLimitFileName))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
