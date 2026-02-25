package cmd

import (
	"testing"

	"h2/internal/config"
	"h2/internal/session/message"
)

func makeAgent(name, pod string) *message.AgentInfo {
	return &message.AgentInfo{
		Name:    name,
		Command: "claude",
		State:   "idle",
		Pod:     pod,
	}
}

func TestGroupAgentsByPod_NoPods(t *testing.T) {
	agents := []*message.AgentInfo{
		makeAgent("a1", ""),
		makeAgent("a2", ""),
	}
	groups := groupAgentsByPod(agents, "")
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].Pod != "" {
		t.Errorf("expected empty pod name, got %q", groups[0].Pod)
	}
	if len(groups[0].Agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(groups[0].Agents))
	}
}

func TestGroupAgentsByPod_MixedPods(t *testing.T) {
	agents := []*message.AgentInfo{
		makeAgent("a1", "backend"),
		makeAgent("a2", "frontend"),
		makeAgent("a3", ""),
		makeAgent("a4", "backend"),
	}
	groups := groupAgentsByPod(agents, "")

	// Should have 3 groups: backend, frontend, no-pod (alphabetical order for named pods).
	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(groups))
	}
	if groups[0].Pod != "backend" {
		t.Errorf("group[0] pod = %q, want %q", groups[0].Pod, "backend")
	}
	if len(groups[0].Agents) != 2 {
		t.Errorf("group[0] agents = %d, want 2", len(groups[0].Agents))
	}
	if groups[1].Pod != "frontend" {
		t.Errorf("group[1] pod = %q, want %q", groups[1].Pod, "frontend")
	}
	if len(groups[1].Agents) != 1 {
		t.Errorf("group[1] agents = %d, want 1", len(groups[1].Agents))
	}
	if groups[2].Pod != "" {
		t.Errorf("group[2] pod = %q, want empty", groups[2].Pod)
	}
	if len(groups[2].Agents) != 1 {
		t.Errorf("group[2] agents = %d, want 1", len(groups[2].Agents))
	}
}

func TestGroupAgentsByPod_FilterByName(t *testing.T) {
	agents := []*message.AgentInfo{
		makeAgent("a1", "backend"),
		makeAgent("a2", "frontend"),
		makeAgent("a3", "backend"),
		makeAgent("a4", ""),
	}
	groups := groupAgentsByPod(agents, "backend")

	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].Pod != "backend" {
		t.Errorf("group[0] pod = %q, want %q", groups[0].Pod, "backend")
	}
	if len(groups[0].Agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(groups[0].Agents))
	}
}

func TestGroupAgentsByPod_FilterByNameNoMatch(t *testing.T) {
	agents := []*message.AgentInfo{
		makeAgent("a1", "backend"),
		makeAgent("a2", ""),
	}
	groups := groupAgentsByPod(agents, "nonexistent")

	if len(groups) != 0 {
		t.Fatalf("expected 0 groups, got %d", len(groups))
	}
}

func TestGroupAgentsByPod_StarShowsAll(t *testing.T) {
	agents := []*message.AgentInfo{
		makeAgent("a1", "backend"),
		makeAgent("a2", ""),
	}
	groups := groupAgentsByPod(agents, "*")

	// Star should show all grouped even if some have no pod.
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0].Pod != "backend" {
		t.Errorf("group[0] pod = %q, want %q", groups[0].Pod, "backend")
	}
	if groups[1].Pod != "" {
		t.Errorf("group[1] pod = %q, want empty", groups[1].Pod)
	}
}

func TestGroupAgentsByPod_StarAllNoPod(t *testing.T) {
	agents := []*message.AgentInfo{
		makeAgent("a1", ""),
		makeAgent("a2", ""),
	}
	groups := groupAgentsByPod(agents, "*")

	// Star with no pods still groups them.
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].Pod != "" {
		t.Errorf("group[0] pod = %q, want empty", groups[0].Pod)
	}
}

func TestGroupAgentsByPod_Empty(t *testing.T) {
	groups := groupAgentsByPod(nil, "")
	if groups != nil {
		t.Errorf("expected nil, got %v", groups)
	}
}

func TestGroupAgentsByPod_OnlyPoddedAgents(t *testing.T) {
	agents := []*message.AgentInfo{
		makeAgent("a1", "backend"),
		makeAgent("a2", "frontend"),
	}
	groups := groupAgentsByPod(agents, "")

	// All agents have pods, so should be grouped.
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0].Pod != "backend" {
		t.Errorf("group[0] pod = %q, want %q", groups[0].Pod, "backend")
	}
	if groups[1].Pod != "frontend" {
		t.Errorf("group[1] pod = %q, want %q", groups[1].Pod, "frontend")
	}
}

// --- orderRoutes tests ---

func TestOrderRoutes_CurrentFirst(t *testing.T) {
	routes := []config.Route{
		{Prefix: "root", Path: "/root"},
		{Prefix: "project-a", Path: "/project-a"},
		{Prefix: "h2home", Path: "/h2home"},
	}

	ordered := orderRoutes(routes, "/h2home", "/root")

	if len(ordered) != 3 {
		t.Fatalf("expected 3 ordered routes, got %d", len(ordered))
	}

	// Current should be first.
	if ordered[0].route.Prefix != "h2home" || !ordered[0].isCurrent {
		t.Errorf("ordered[0] = %+v, want h2home (current)", ordered[0])
	}

	// Root should be second.
	if ordered[1].route.Prefix != "root" || ordered[1].isCurrent {
		t.Errorf("ordered[1] = %+v, want root (not current)", ordered[1])
	}

	// Others after.
	if ordered[2].route.Prefix != "project-a" || ordered[2].isCurrent {
		t.Errorf("ordered[2] = %+v, want project-a (not current)", ordered[2])
	}
}

func TestOrderRoutes_CurrentIsRoot(t *testing.T) {
	routes := []config.Route{
		{Prefix: "root", Path: "/root"},
		{Prefix: "project-a", Path: "/project-a"},
	}

	// Current dir is the root dir.
	ordered := orderRoutes(routes, "/root", "/root")

	if len(ordered) != 2 {
		t.Fatalf("expected 2 ordered routes, got %d", len(ordered))
	}

	// Root should be first and marked current.
	if ordered[0].route.Prefix != "root" || !ordered[0].isCurrent {
		t.Errorf("ordered[0] = %+v, want root (current)", ordered[0])
	}

	// Other follows.
	if ordered[1].route.Prefix != "project-a" {
		t.Errorf("ordered[1] = %+v, want project-a", ordered[1])
	}
}

func TestOrderRoutes_NoCurrentDir(t *testing.T) {
	routes := []config.Route{
		{Prefix: "root", Path: "/root"},
		{Prefix: "project-a", Path: "/project-a"},
	}

	// No current dir resolved (empty string).
	ordered := orderRoutes(routes, "", "/root")

	if len(ordered) != 2 {
		t.Fatalf("expected 2 ordered routes, got %d", len(ordered))
	}

	// Root should still come first (as "current" since it's the fallback).
	if ordered[0].route.Prefix != "root" {
		t.Errorf("ordered[0] = %+v, want root", ordered[0])
	}
}

func TestOrderRoutes_PreservesFileOrder(t *testing.T) {
	routes := []config.Route{
		{Prefix: "root", Path: "/root"},
		{Prefix: "charlie", Path: "/charlie"},
		{Prefix: "alpha", Path: "/alpha"},
		{Prefix: "beta", Path: "/beta"},
	}

	ordered := orderRoutes(routes, "/root", "/root")

	// root is current, so first. Then others should be in file order.
	if ordered[0].route.Prefix != "root" {
		t.Errorf("ordered[0] = %q, want root", ordered[0].route.Prefix)
	}
	if ordered[1].route.Prefix != "charlie" {
		t.Errorf("ordered[1] = %q, want charlie", ordered[1].route.Prefix)
	}
	if ordered[2].route.Prefix != "alpha" {
		t.Errorf("ordered[2] = %q, want alpha", ordered[2].route.Prefix)
	}
	if ordered[3].route.Prefix != "beta" {
		t.Errorf("ordered[3] = %q, want beta", ordered[3].route.Prefix)
	}
}

func TestOrderRoutes_Empty(t *testing.T) {
	ordered := orderRoutes(nil, "/whatever", "/root")
	if len(ordered) != 0 {
		t.Errorf("expected 0 ordered routes, got %d", len(ordered))
	}
}

func TestOrderRoutes_CurrentNotInRoutes(t *testing.T) {
	routes := []config.Route{
		{Prefix: "root", Path: "/root"},
		{Prefix: "project-a", Path: "/project-a"},
	}

	// Current dir isn't in routes.
	ordered := orderRoutes(routes, "/unknown", "/root")

	// Root should be first (no current found), then project-a.
	if len(ordered) != 2 {
		t.Fatalf("expected 2 ordered routes, got %d", len(ordered))
	}
	if ordered[0].route.Prefix != "root" {
		t.Errorf("ordered[0] = %q, want root", ordered[0].route.Prefix)
	}
	if ordered[1].route.Prefix != "project-a" {
		t.Errorf("ordered[1] = %q, want project-a", ordered[1].route.Prefix)
	}
}
