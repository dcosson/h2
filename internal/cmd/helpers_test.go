package cmd

import (
	"testing"
)

func TestResolveActor_H2ActorEnv(t *testing.T) {
	t.Setenv("H2_ACTOR", "fast-deer")
	got := resolveActor()
	if got != "fast-deer" {
		t.Errorf("resolveActor() = %q, want %q", got, "fast-deer")
	}
}

func TestResolveActor_FallsBackToUser(t *testing.T) {
	t.Setenv("H2_ACTOR", "")
	t.Setenv("USER", "testuser")
	got := resolveActor()
	// Should be either git user.name or $USER — not empty or "unknown".
	if got == "" || got == "unknown" {
		t.Errorf("resolveActor() = %q, expected a real value", got)
	}
}

func TestResolveActor_H2ActorTakesPrecedence(t *testing.T) {
	t.Setenv("H2_ACTOR", "my-agent")
	t.Setenv("USER", "testuser")
	got := resolveActor()
	if got != "my-agent" {
		t.Errorf("resolveActor() = %q, want %q", got, "my-agent")
	}
}
