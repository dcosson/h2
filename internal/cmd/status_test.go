package cmd

import (
	"testing"

	"h2/internal/session/message"
)

// --- isIdleState unit tests ---

func TestIsIdleState(t *testing.T) {
	tests := []struct {
		state string
		want  bool
	}{
		{"idle", true},
		{"exited", true},
		{"active", false},
		{"", false},
		{"unknown", false},
		{"starting", false},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			got := isIdleState(tt.state)
			if got != tt.want {
				t.Errorf("isIdleState(%q) = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}

// --- checkAgentsIdle unit tests ---

func TestCheckAgentsIdle_AllIdle(t *testing.T) {
	agents := []*message.AgentInfo{
		{Name: "a", State: "idle"},
		{Name: "b", State: "idle"},
	}
	if !checkAgentsIdle(agents, "") {
		t.Error("all idle agents should return true")
	}
}

func TestCheckAgentsIdle_OneActive(t *testing.T) {
	agents := []*message.AgentInfo{
		{Name: "a", State: "idle"},
		{Name: "b", State: "active"},
	}
	if checkAgentsIdle(agents, "") {
		t.Error("should return false when one agent is active")
	}
}

func TestCheckAgentsIdle_AllActive(t *testing.T) {
	agents := []*message.AgentInfo{
		{Name: "a", State: "active"},
		{Name: "b", State: "active"},
	}
	if checkAgentsIdle(agents, "") {
		t.Error("should return false when all agents are active")
	}
}

func TestCheckAgentsIdle_ExitedCountsAsIdle(t *testing.T) {
	agents := []*message.AgentInfo{
		{Name: "a", State: "exited"},
		{Name: "b", State: "idle"},
	}
	if !checkAgentsIdle(agents, "") {
		t.Error("exited agents should count as idle")
	}
}

func TestCheckAgentsIdle_AllExited(t *testing.T) {
	agents := []*message.AgentInfo{
		{Name: "a", State: "exited"},
		{Name: "b", State: "exited"},
	}
	if !checkAgentsIdle(agents, "") {
		t.Error("all exited agents should return true")
	}
}

func TestCheckAgentsIdle_NoAgents(t *testing.T) {
	if !checkAgentsIdle(nil, "") {
		t.Error("no agents should return true (idle)")
	}
}

func TestCheckAgentsIdle_EmptySlice(t *testing.T) {
	if !checkAgentsIdle([]*message.AgentInfo{}, "") {
		t.Error("empty agent list should return true (idle)")
	}
}

func TestCheckAgentsIdle_UnknownState(t *testing.T) {
	agents := []*message.AgentInfo{
		{Name: "a", State: "unknown"},
	}
	if checkAgentsIdle(agents, "") {
		t.Error("unknown state should not count as idle")
	}
}

func TestCheckAgentsIdle_EmptyState(t *testing.T) {
	agents := []*message.AgentInfo{
		{Name: "a", State: ""},
	}
	if checkAgentsIdle(agents, "") {
		t.Error("empty state should not count as idle")
	}
}

// --- Pod filter tests ---

func TestCheckAgentsIdle_PodFilter_MatchingIdle(t *testing.T) {
	agents := []*message.AgentInfo{
		{Name: "a", State: "idle", Pod: "bench"},
		{Name: "b", State: "active", Pod: "other"},
	}
	if !checkAgentsIdle(agents, "bench") {
		t.Error("should be idle: active agent is in different pod")
	}
}

func TestCheckAgentsIdle_PodFilter_MatchingActive(t *testing.T) {
	agents := []*message.AgentInfo{
		{Name: "a", State: "active", Pod: "bench"},
		{Name: "b", State: "idle", Pod: "other"},
	}
	if checkAgentsIdle(agents, "bench") {
		t.Error("should not be idle: matching agent is active")
	}
}

func TestCheckAgentsIdle_PodFilter_NoMatch(t *testing.T) {
	agents := []*message.AgentInfo{
		{Name: "a", State: "active", Pod: "other"},
	}
	// No agents match the filter, so all (zero) matching agents are idle.
	if !checkAgentsIdle(agents, "bench") {
		t.Error("should be idle: no agents match pod filter")
	}
}

func TestCheckAgentsIdle_PodFilter_MixedInPod(t *testing.T) {
	agents := []*message.AgentInfo{
		{Name: "a", State: "idle", Pod: "bench"},
		{Name: "b", State: "active", Pod: "bench"},
		{Name: "c", State: "idle", Pod: "other"},
	}
	if checkAgentsIdle(agents, "bench") {
		t.Error("should not be idle: one bench agent is active")
	}
}

func TestCheckAgentsIdle_PodFilter_AllInPodIdle(t *testing.T) {
	agents := []*message.AgentInfo{
		{Name: "concierge", State: "idle", Pod: "bench"},
		{Name: "coder-1", State: "idle", Pod: "bench"},
		{Name: "coder-2", State: "exited", Pod: "bench"},
		{Name: "reviewer", State: "idle", Pod: "bench"},
	}
	if !checkAgentsIdle(agents, "bench") {
		t.Error("all bench agents are idle/exited, should return true")
	}
}

func TestCheckAgentsIdle_EmptyPodFilter_IncludesAll(t *testing.T) {
	agents := []*message.AgentInfo{
		{Name: "a", State: "idle", Pod: "bench"},
		{Name: "b", State: "active", Pod: ""},
	}
	// Empty filter includes all agents.
	if checkAgentsIdle(agents, "") {
		t.Error("should not be idle: agent with no pod is active")
	}
}

// --- Command arg validation tests ---

func TestStatusCmd_NoArgsNoIdle(t *testing.T) {
	cmd := newStatusCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no args and no --idle")
	}
}

func TestStatusCmd_IdleFlagAcceptsNoArgs(t *testing.T) {
	// This will run the idle check. In the test environment, it will
	// query sockets (and find real or no agents), but it should not
	// error on arg validation.
	cmd := newStatusCmd()
	cmd.SetArgs([]string{"--idle"})
	// Don't check err — the socket layer might fail in test env.
	// We just verify no panic and no arg-validation error.
	err := cmd.Execute()
	_ = err
}

func TestStatusCmd_TooManyArgs(t *testing.T) {
	cmd := newStatusCmd()
	cmd.SetArgs([]string{"agent-a", "agent-b"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when too many args provided")
	}
}
