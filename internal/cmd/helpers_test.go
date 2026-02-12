package cmd

import (
	"strings"
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

// --- parseVarFlags tests ---

func TestParseVarFlags_SingleVar(t *testing.T) {
	vars, err := parseVarFlags([]string{"team=backend"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vars["team"] != "backend" {
		t.Errorf("vars[team] = %q, want backend", vars["team"])
	}
}

func TestParseVarFlags_MultipleVars(t *testing.T) {
	vars, err := parseVarFlags([]string{"team=backend", "project=h2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vars["team"] != "backend" {
		t.Errorf("vars[team] = %q, want backend", vars["team"])
	}
	if vars["project"] != "h2" {
		t.Errorf("vars[project] = %q, want h2", vars["project"])
	}
}

func TestParseVarFlags_ValueWithEquals(t *testing.T) {
	vars, err := parseVarFlags([]string{"query=a=b=c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vars["query"] != "a=b=c" {
		t.Errorf("vars[query] = %q, want a=b=c", vars["query"])
	}
}

func TestParseVarFlags_EmptyValue(t *testing.T) {
	vars, err := parseVarFlags([]string{"prefix="})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	val, ok := vars["prefix"]
	if !ok {
		t.Fatal("expected 'prefix' key to exist")
	}
	if val != "" {
		t.Errorf("vars[prefix] = %q, want empty string", val)
	}
}

func TestParseVarFlags_MissingEquals(t *testing.T) {
	_, err := parseVarFlags([]string{"noequals"})
	if err == nil {
		t.Fatal("expected error for missing =")
	}
	if !strings.Contains(err.Error(), "must be key=value") {
		t.Errorf("error should mention key=value format: %v", err)
	}
}

func TestParseVarFlags_EmptyKey(t *testing.T) {
	_, err := parseVarFlags([]string{"=value"})
	if err == nil {
		t.Fatal("expected error for empty key")
	}
	if !strings.Contains(err.Error(), "key cannot be empty") {
		t.Errorf("error should mention empty key: %v", err)
	}
}

func TestParseVarFlags_EmptySlice(t *testing.T) {
	vars, err := parseVarFlags(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vars) != 0 {
		t.Errorf("expected empty map, got %v", vars)
	}
}
