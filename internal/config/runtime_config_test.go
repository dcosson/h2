package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteReadRuntimeConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	rc := &RuntimeConfig{
		AgentName:            "test-agent",
		SessionID:            "uuid-123",
		RoleName:             "coder",
		Pod:                  "pod1",
		HarnessType:          "claude_code",
		HarnessConfigDir:     "/home/user/.h2/claude-config/default",
		HarnessSessionID:     "harness-uuid-456",
		Command:              "claude",
		Args:                 []string{"--verbose"},
		Model:                "claude-opus-4-6",
		CWD:                  "/home/user/project",
		Instructions:         "You are a helpful coder.",
		SystemPrompt:         "Custom system prompt.",
		ClaudePermissionMode: "plan",
		CodexSandboxMode:     "",
		CodexAskForApproval:  "",
		AdditionalDirs:       []string{"/extra/dir1", "/extra/dir2"},
		HeartbeatIdleTimeout: "30s",
		HeartbeatMessage:     "Are you still working?",
		HeartbeatCondition:   "test -f /tmp/check",
		Overrides:            map[string]string{"worktree_enabled": "true"},
		ResumeSessionID:      "old-uuid-789",
		StartedAt:            "2026-03-05T10:00:00Z",
	}

	if err := WriteRuntimeConfig(dir, rc); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := ReadRuntimeConfig(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Verify every field.
	if got.AgentName != rc.AgentName {
		t.Errorf("AgentName = %q, want %q", got.AgentName, rc.AgentName)
	}
	if got.SessionID != rc.SessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, rc.SessionID)
	}
	if got.RoleName != rc.RoleName {
		t.Errorf("RoleName = %q, want %q", got.RoleName, rc.RoleName)
	}
	if got.Pod != rc.Pod {
		t.Errorf("Pod = %q, want %q", got.Pod, rc.Pod)
	}
	if got.HarnessType != rc.HarnessType {
		t.Errorf("HarnessType = %q, want %q", got.HarnessType, rc.HarnessType)
	}
	if got.HarnessConfigDir != rc.HarnessConfigDir {
		t.Errorf("HarnessConfigDir = %q, want %q", got.HarnessConfigDir, rc.HarnessConfigDir)
	}
	if got.HarnessSessionID != rc.HarnessSessionID {
		t.Errorf("HarnessSessionID = %q, want %q", got.HarnessSessionID, rc.HarnessSessionID)
	}
	if got.Command != rc.Command {
		t.Errorf("Command = %q, want %q", got.Command, rc.Command)
	}
	if len(got.Args) != len(rc.Args) || (len(got.Args) > 0 && got.Args[0] != rc.Args[0]) {
		t.Errorf("Args = %v, want %v", got.Args, rc.Args)
	}
	if got.Model != rc.Model {
		t.Errorf("Model = %q, want %q", got.Model, rc.Model)
	}
	if got.CWD != rc.CWD {
		t.Errorf("CWD = %q, want %q", got.CWD, rc.CWD)
	}
	if got.Instructions != rc.Instructions {
		t.Errorf("Instructions = %q, want %q", got.Instructions, rc.Instructions)
	}
	if got.SystemPrompt != rc.SystemPrompt {
		t.Errorf("SystemPrompt = %q, want %q", got.SystemPrompt, rc.SystemPrompt)
	}
	if got.ClaudePermissionMode != rc.ClaudePermissionMode {
		t.Errorf("ClaudePermissionMode = %q, want %q", got.ClaudePermissionMode, rc.ClaudePermissionMode)
	}
	if len(got.AdditionalDirs) != 2 {
		t.Errorf("AdditionalDirs len = %d, want 2", len(got.AdditionalDirs))
	}
	if got.HeartbeatIdleTimeout != rc.HeartbeatIdleTimeout {
		t.Errorf("HeartbeatIdleTimeout = %q, want %q", got.HeartbeatIdleTimeout, rc.HeartbeatIdleTimeout)
	}
	if got.HeartbeatMessage != rc.HeartbeatMessage {
		t.Errorf("HeartbeatMessage = %q, want %q", got.HeartbeatMessage, rc.HeartbeatMessage)
	}
	if got.HeartbeatCondition != rc.HeartbeatCondition {
		t.Errorf("HeartbeatCondition = %q, want %q", got.HeartbeatCondition, rc.HeartbeatCondition)
	}
	if len(got.Overrides) != 1 || got.Overrides["worktree_enabled"] != "true" {
		t.Errorf("Overrides = %v, want %v", got.Overrides, rc.Overrides)
	}
	if got.ResumeSessionID != rc.ResumeSessionID {
		t.Errorf("ResumeSessionID = %q, want %q", got.ResumeSessionID, rc.ResumeSessionID)
	}
	if got.StartedAt != rc.StartedAt {
		t.Errorf("StartedAt = %q, want %q", got.StartedAt, rc.StartedAt)
	}
	// Deprecated field should not be set after round-trip.
	if got.ClaudeConfigDir != "" {
		t.Errorf("ClaudeConfigDir should be empty after round-trip, got %q", got.ClaudeConfigDir)
	}
}

func TestReadRuntimeConfig_ValidationRejectsMissingRequired(t *testing.T) {
	tests := []struct {
		name    string
		rc      RuntimeConfig
		missing []string
	}{
		{
			name:    "all missing",
			rc:      RuntimeConfig{},
			missing: []string{"agent_name", "session_id", "harness_type", "command", "cwd", "started_at"},
		},
		{
			name: "missing harness_type",
			rc: RuntimeConfig{
				AgentName: "a",
				SessionID: "s",
				Command:   "claude",
				CWD:       "/tmp",
				StartedAt: "2026-01-01T00:00:00Z",
			},
			missing: []string{"harness_type"},
		},
		{
			name: "empty command",
			rc: RuntimeConfig{
				AgentName:   "a",
				SessionID:   "s",
				HarnessType: "claude_code",
				Command:     "",
				CWD:         "/tmp",
				StartedAt:   "2026-01-01T00:00:00Z",
			},
			missing: []string{"command"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			// Write directly to bypass WriteRuntimeConfig's validation.
			data, _ := json.MarshalIndent(tt.rc, "", "  ")
			path := filepath.Join(dir, runtimeConfigFilename)
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatalf("write file: %v", err)
			}

			_, err := ReadRuntimeConfig(dir)
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			for _, field := range tt.missing {
				if !strings.Contains(err.Error(), field) {
					t.Errorf("error %q should mention missing field %q", err.Error(), field)
				}
			}
		})
	}
}

func TestReadRuntimeConfig_ValidWithAllRequired(t *testing.T) {
	dir := t.TempDir()
	rc := RuntimeConfig{
		AgentName:   "a",
		SessionID:   "s",
		HarnessType: "claude_code",
		Command:     "claude",
		CWD:         "/tmp",
		StartedAt:   "2026-01-01T00:00:00Z",
	}
	data, _ := json.MarshalIndent(rc, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, runtimeConfigFilename), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := ReadRuntimeConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.AgentName != "a" {
		t.Errorf("AgentName = %q, want %q", got.AgentName, "a")
	}
}

func TestReadRuntimeConfig_MigratesClaudeConfigDir(t *testing.T) {
	dir := t.TempDir()
	// Simulate old metadata with claude_config_dir but no harness_config_dir.
	old := map[string]any{
		"agent_name":        "old-agent",
		"session_id":        "old-session",
		"harness_type":      "claude_code",
		"command":           "claude",
		"cwd":               "/home/user/project",
		"claude_config_dir": "/home/user/.h2/claude-config/default",
		"started_at":        "2026-01-01T00:00:00Z",
	}
	data, _ := json.MarshalIndent(old, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, runtimeConfigFilename), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := ReadRuntimeConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.HarnessConfigDir != "/home/user/.h2/claude-config/default" {
		t.Errorf("HarnessConfigDir = %q, want migrated value", got.HarnessConfigDir)
	}
	if got.ClaudeConfigDir != "" {
		t.Errorf("ClaudeConfigDir should be cleared after migration, got %q", got.ClaudeConfigDir)
	}
}

func TestReadRuntimeConfig_OldMetadataWithoutHarnessTypeFails(t *testing.T) {
	dir := t.TempDir()
	// Simulate very old metadata that only has command, no harness_type.
	old := map[string]any{
		"agent_name": "old-agent",
		"session_id": "old-session",
		"command":    "claude",
		"cwd":        "/home/user/project",
		"started_at": "2026-01-01T00:00:00Z",
	}
	data, _ := json.MarshalIndent(old, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, runtimeConfigFilename), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := ReadRuntimeConfig(dir)
	if err == nil {
		t.Fatal("expected validation error for missing harness_type")
	}
	if !strings.Contains(err.Error(), "harness_type") {
		t.Errorf("error %q should mention harness_type", err.Error())
	}
}

func TestReadRuntimeConfig_OptionalFieldsAllowedEmpty(t *testing.T) {
	dir := t.TempDir()
	// Only required fields — all optional fields missing.
	rc := RuntimeConfig{
		AgentName:   "a",
		SessionID:   "s",
		HarnessType: "generic",
		Command:     "bash",
		CWD:         "/tmp",
		StartedAt:   "2026-01-01T00:00:00Z",
	}
	data, _ := json.MarshalIndent(rc, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, runtimeConfigFilename), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := ReadRuntimeConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Model != "" {
		t.Errorf("Model should be empty, got %q", got.Model)
	}
	if got.Instructions != "" {
		t.Errorf("Instructions should be empty, got %q", got.Instructions)
	}
}

func TestReadRuntimeConfig_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, runtimeConfigFilename), []byte("{"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := ReadRuntimeConfig(dir)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestReadRuntimeConfig_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadRuntimeConfig(dir)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestWriteRuntimeConfig_EmptySessionDir(t *testing.T) {
	rc := &RuntimeConfig{
		AgentName:   "a",
		SessionID:   "s",
		HarnessType: "generic",
		Command:     "bash",
		CWD:         "/tmp",
		StartedAt:   "2026-01-01T00:00:00Z",
	}
	// Should be a no-op, not an error.
	if err := WriteRuntimeConfig("", rc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWriteRuntimeConfig_SetsStartedAtIfEmpty(t *testing.T) {
	dir := t.TempDir()
	rc := &RuntimeConfig{
		AgentName:   "a",
		SessionID:   "s",
		HarnessType: "generic",
		Command:     "bash",
		CWD:         "/tmp",
	}
	if err := WriteRuntimeConfig(dir, rc); err != nil {
		t.Fatalf("write: %v", err)
	}
	if rc.StartedAt == "" {
		t.Error("StartedAt should have been set")
	}
}

func TestWriteRuntimeConfig_AtomicNoTmpLeftover(t *testing.T) {
	dir := t.TempDir()
	rc := &RuntimeConfig{
		AgentName:   "a",
		SessionID:   "s",
		HarnessType: "generic",
		Command:     "bash",
		CWD:         "/tmp",
		StartedAt:   "2026-01-01T00:00:00Z",
	}
	if err := WriteRuntimeConfig(dir, rc); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Verify no .tmp file remains.
	tmpPath := filepath.Join(dir, runtimeConfigFilename+".tmp")
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("tmp file should not exist after successful write")
	}

	// Verify the real file exists and is valid.
	got, err := ReadRuntimeConfig(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.AgentName != "a" {
		t.Errorf("AgentName = %q, want %q", got.AgentName, "a")
	}
}

func TestParseHeartbeatIdleTimeout(t *testing.T) {
	rc := &RuntimeConfig{HeartbeatIdleTimeout: "30s"}
	d, err := rc.ParseHeartbeatIdleTimeout()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.Seconds() != 30 {
		t.Errorf("duration = %v, want 30s", d)
	}
}

func TestParseHeartbeatIdleTimeout_Empty(t *testing.T) {
	rc := &RuntimeConfig{}
	d, err := rc.ParseHeartbeatIdleTimeout()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d != 0 {
		t.Errorf("duration = %v, want 0", d)
	}
}

func TestParseHeartbeatIdleTimeout_Invalid(t *testing.T) {
	rc := &RuntimeConfig{HeartbeatIdleTimeout: "not-a-duration"}
	_, err := rc.ParseHeartbeatIdleTimeout()
	if err == nil {
		t.Fatal("expected error for invalid duration")
	}
}

func TestIsLegacySessionMetadata_NewFormat(t *testing.T) {
	dir := t.TempDir()
	rc := &RuntimeConfig{
		AgentName:    "test",
		SessionID:    "uuid",
		HarnessType:  "claude_code",
		Command:      "claude",
		CWD:          "/tmp",
		Instructions: "Do things",
	}
	if err := WriteRuntimeConfig(dir, rc); err != nil {
		t.Fatalf("write: %v", err)
	}
	if IsLegacySessionMetadata(dir) {
		t.Error("expected IsLegacySessionMetadata=false for RuntimeConfig file")
	}
}

func TestIsLegacySessionMetadata_NewFormatMinimalFields(t *testing.T) {
	// RuntimeConfig with only required fields (no optional runtime-only keys).
	dir := t.TempDir()
	rc := &RuntimeConfig{
		AgentName:   "test",
		SessionID:   "uuid",
		HarnessType: "generic",
		Command:     "bash",
		CWD:         "/tmp",
	}
	if err := WriteRuntimeConfig(dir, rc); err != nil {
		t.Fatalf("write: %v", err)
	}
	if IsLegacySessionMetadata(dir) {
		t.Error("expected IsLegacySessionMetadata=false for minimal RuntimeConfig")
	}
}

func TestIsLegacySessionMetadata_LegacyFormat(t *testing.T) {
	dir := t.TempDir()
	legacy := `{
		"agent_name": "test",
		"session_id": "uuid",
		"claude_config_dir": "/some/path",
		"cwd": "/tmp",
		"command": "claude",
		"role": "default",
		"started_at": "2026-01-01T00:00:00Z"
	}`
	if err := os.WriteFile(filepath.Join(dir, runtimeConfigFilename), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !IsLegacySessionMetadata(dir) {
		t.Error("expected IsLegacySessionMetadata=true for legacy SessionMetadata file")
	}
}

func TestIsLegacySessionMetadata_NoFile(t *testing.T) {
	dir := t.TempDir()
	if IsLegacySessionMetadata(dir) {
		t.Error("expected IsLegacySessionMetadata=false when file doesn't exist")
	}
}

func TestReadRuntimeConfig_UnknownFieldsIgnored(t *testing.T) {
	dir := t.TempDir()
	// JSON with an unknown field — should be silently ignored.
	raw := `{
		"agent_name": "a",
		"session_id": "s",
		"harness_type": "generic",
		"command": "bash",
		"cwd": "/tmp",
		"started_at": "2026-01-01T00:00:00Z",
		"some_future_field": "value"
	}`
	if err := os.WriteFile(filepath.Join(dir, runtimeConfigFilename), []byte(raw), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := ReadRuntimeConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.AgentName != "a" {
		t.Errorf("AgentName = %q, want %q", got.AgentName, "a")
	}
}
