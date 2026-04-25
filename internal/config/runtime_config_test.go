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
		AgentName:               "test-agent",
		SessionID:               "uuid-123",
		RoleName:                "coder",
		Pod:                     "pod1",
		HarnessType:             "claude_code",
		HarnessConfigPathPrefix: "/home/user/.h2/claude-config",
		Profile:                 "default",
		HarnessSessionID:        "harness-uuid-456",
		Command:                 "claude",
		Args:                    []string{"--verbose"},
		Model:                   "claude-opus-4-6",
		CWD:                     "/home/user/project",
		Instructions:            "You are a helpful coder.",
		SystemPrompt:            "Custom system prompt.",
		ClaudePermissionMode:    "plan",
		CodexSandboxMode:        "",
		CodexAskForApproval:     "",
		AdditionalDirs:          []string{"/extra/dir1", "/extra/dir2"},
		Triggers: []TriggerYAMLSpec{
			{ID: "t1", Name: "test-trigger", Event: "state_change", State: "idle", Exec: "echo nudge"},
		},
		Schedules: []ScheduleYAMLSpec{
			{ID: "s1", Name: "test-schedule", RRule: "FREQ=SECONDLY;INTERVAL=30", Exec: "echo tick"},
		},
		PermissionReview: &PermissionReview{
			DCG: &DCGConfig{
				Enabled:           boolPtr(true),
				DestructivePolicy: "moderate",
				PrivacyPolicy:     "strict",
				Allowlist:         []string{"echo *"},
				Blocklist:         []string{"rm *"},
			},
			AIReviewer: &AIReviewerConfig{
				Enabled:           boolPtr(true),
				Model:             "haiku",
				InstructionsIntro: "You are a permission reviewer.",
				InstructionsBody:  "Review tool calls carefully.",
			},
		},
		Overrides:           map[string]string{"worktree_enabled": "true"},
		PassthroughEnvKeys:  []string{"OPENAI_API_KEY", "TEAM_CONTEXT"},
		ResumeEnvWarning:    "launch-scoped env passthrough may not be available for unattended resume: OPENAI_API_KEY",
		GatewayPID:          1234,
		GatewayGeneration:   "generation-1",
		GatewayDesiredState: "running",
		GatewayRuntimeState: "running",
		ChildPID:            5678,
		ChildPGID:           5670,
		LastExitReason:      "child_exit",
		LastStateAt:         "2026-03-05T10:01:00Z",
		StartedAt:           "2026-03-05T10:00:00Z",
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
	if got.HarnessConfigPathPrefix != rc.HarnessConfigPathPrefix {
		t.Errorf("HarnessConfigPathPrefix = %q, want %q", got.HarnessConfigPathPrefix, rc.HarnessConfigPathPrefix)
	}
	if got.Profile != rc.Profile {
		t.Errorf("Profile = %q, want %q", got.Profile, rc.Profile)
	}
	if got.HarnessConfigDir() != "/home/user/.h2/claude-config/default" {
		t.Errorf("HarnessConfigDir() = %q, want %q", got.HarnessConfigDir(), "/home/user/.h2/claude-config/default")
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
	if len(got.Triggers) != 1 || got.Triggers[0].ID != "t1" {
		t.Errorf("Triggers = %v, want 1 trigger with ID t1", got.Triggers)
	}
	if len(got.Schedules) != 1 || got.Schedules[0].ID != "s1" {
		t.Errorf("Schedules = %v, want 1 schedule with ID s1", got.Schedules)
	}
	if len(got.Overrides) != 1 || got.Overrides["worktree_enabled"] != "true" {
		t.Errorf("Overrides = %v, want %v", got.Overrides, rc.Overrides)
	}
	if len(got.PassthroughEnvKeys) != len(rc.PassthroughEnvKeys) || got.PassthroughEnvKeys[0] != rc.PassthroughEnvKeys[0] || got.PassthroughEnvKeys[1] != rc.PassthroughEnvKeys[1] {
		t.Errorf("PassthroughEnvKeys = %v, want %v", got.PassthroughEnvKeys, rc.PassthroughEnvKeys)
	}
	if got.ResumeEnvWarning != rc.ResumeEnvWarning {
		t.Errorf("ResumeEnvWarning = %q, want %q", got.ResumeEnvWarning, rc.ResumeEnvWarning)
	}
	if got.GatewayPID != rc.GatewayPID {
		t.Errorf("GatewayPID = %d, want %d", got.GatewayPID, rc.GatewayPID)
	}
	if got.GatewayGeneration != rc.GatewayGeneration {
		t.Errorf("GatewayGeneration = %q, want %q", got.GatewayGeneration, rc.GatewayGeneration)
	}
	if got.GatewayDesiredState != rc.GatewayDesiredState {
		t.Errorf("GatewayDesiredState = %q, want %q", got.GatewayDesiredState, rc.GatewayDesiredState)
	}
	if got.GatewayRuntimeState != rc.GatewayRuntimeState {
		t.Errorf("GatewayRuntimeState = %q, want %q", got.GatewayRuntimeState, rc.GatewayRuntimeState)
	}
	if got.ChildPID != rc.ChildPID {
		t.Errorf("ChildPID = %d, want %d", got.ChildPID, rc.ChildPID)
	}
	if got.ChildPGID != rc.ChildPGID {
		t.Errorf("ChildPGID = %d, want %d", got.ChildPGID, rc.ChildPGID)
	}
	if got.LastExitReason != rc.LastExitReason {
		t.Errorf("LastExitReason = %q, want %q", got.LastExitReason, rc.LastExitReason)
	}
	if got.LastStateAt != rc.LastStateAt {
		t.Errorf("LastStateAt = %q, want %q", got.LastStateAt, rc.LastStateAt)
	}
	if got.StartedAt != rc.StartedAt {
		t.Errorf("StartedAt = %q, want %q", got.StartedAt, rc.StartedAt)
	}
	// PermissionReview round-trip.
	if got.PermissionReview == nil {
		t.Fatal("PermissionReview is nil after round-trip")
	}
	if got.PermissionReview.DCG == nil {
		t.Fatal("DCG config is nil after round-trip")
	}
	if !got.PermissionReview.DCG.IsEnabled() {
		t.Error("DCG should be enabled")
	}
	if got.PermissionReview.DCG.DestructivePolicy != "moderate" {
		t.Errorf("DCG DestructivePolicy = %q, want %q", got.PermissionReview.DCG.DestructivePolicy, "moderate")
	}
	if got.PermissionReview.DCG.PrivacyPolicy != "strict" {
		t.Errorf("DCG PrivacyPolicy = %q, want %q", got.PermissionReview.DCG.PrivacyPolicy, "strict")
	}
	if len(got.PermissionReview.DCG.Allowlist) != 1 || got.PermissionReview.DCG.Allowlist[0] != "echo *" {
		t.Errorf("DCG Allowlist = %v, want [echo *]", got.PermissionReview.DCG.Allowlist)
	}
	if got.PermissionReview.AIReviewer == nil {
		t.Fatal("AIReviewer config is nil after round-trip")
	}
	if !got.PermissionReview.AIReviewer.IsEnabled() {
		t.Error("AIReviewer should be enabled")
	}
	if got.PermissionReview.AIReviewer.GetModel() != "haiku" {
		t.Errorf("AIReviewer Model = %q, want %q", got.PermissionReview.AIReviewer.GetModel(), "haiku")
	}
	wantInstructions := rc.PermissionReview.AIReviewer.GetInstructions()
	if got.PermissionReview.AIReviewer.GetInstructions() != wantInstructions {
		t.Errorf("AIReviewer instructions = %q, want %q", got.PermissionReview.AIReviewer.GetInstructions(), wantInstructions)
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

func TestWriteRuntimeConfig_DoesNotAutoFillStartedAt(t *testing.T) {
	dir := t.TempDir()
	rc := &RuntimeConfig{
		AgentName:   "a",
		SessionID:   "s",
		HarnessType: "generic",
		Command:     "bash",
		CWD:         "/tmp",
	}
	// WriteRuntimeConfig should not auto-fill StartedAt — callers set it.
	if err := WriteRuntimeConfig(dir, rc); err != nil {
		t.Fatalf("write: %v", err)
	}
	if rc.StartedAt != "" {
		t.Errorf("StartedAt should remain empty, got %q", rc.StartedAt)
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

func TestRuntimeConfig_HarnessConfigDir(t *testing.T) {
	tests := []struct {
		name    string
		prefix  string
		profile string
		want    string
	}{
		{"prefix and profile", "/home/.h2/claude-config", "default", "/home/.h2/claude-config/default"},
		{"prefix only defaults profile", "/home/.h2/claude-config", "", "/home/.h2/claude-config/default"},
		{"no prefix returns empty", "", "default", ""},
		{"no prefix no profile returns empty", "", "", ""},
		{"custom profile", "/config", "staging", "/config/staging"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &RuntimeConfig{
				HarnessConfigPathPrefix: tt.prefix,
				Profile:                 tt.profile,
			}
			if got := rc.HarnessConfigDir(); got != tt.want {
				t.Errorf("HarnessConfigDir() = %q, want %q", got, tt.want)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }
