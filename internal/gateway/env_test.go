package gateway

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestComposeChildEnvPrecedenceAndDenylist(t *testing.T) {
	spec := ChildEnvSpec{
		SupervisorEnv: []string{
			"PATH=/usr/bin",
			"HOME=/home/supervisor",
			"SHARED=supervisor",
			"H2_ACTOR=parent-agent",
			"H2_ROLE=parent-role",
			"H2_POD=parent-pod",
			"H2_SESSION_DIR=/tmp/parent-session",
			"CLAUDECODE=1",
		},
		RuntimeEnv: map[string]string{
			"PATH":    "/opt/h2/bin:/usr/bin",
			"RUNTIME": "yes",
			"SHARED":  "runtime",
		},
		RoleEnv: map[string]string{
			"ROLE_ONLY": "role",
			"SHARED":    "role",
		},
		EnvPassthrough: map[string]string{
			"ANTHROPIC_API_KEY": "launch-secret",
			"SHARED":            "passthrough",
			"NOT_ALLOWED":       "drop",
		},
		PassthroughAllowlist: []string{"SHARED"},
		EnvOverrides: map[string]string{
			"OVERRIDE_ONLY":     "override",
			"ANTHROPIC_API_KEY": "override-secret",
			"SHARED":            "override",
		},
		InternalEnv: map[string]string{
			"H2_DIR":         "/tmp/h2",
			"H2_ACTOR":       "child-agent",
			"H2_ROLE":        "coder",
			"H2_SESSION_DIR": "/tmp/session",
			"SHARED":         "internal",
		},
		HarnessEnv: map[string]string{
			"CLAUDE_CONFIG_DIR": "/tmp/claude",
			"OTEL_EXPORTER":     "http",
			"SHARED":            "harness",
		},
	}

	env := ComposeChildEnvMap(spec)

	want := map[string]string{
		"PATH":              "/opt/h2/bin:/usr/bin",
		"HOME":              "/home/supervisor",
		"RUNTIME":           "yes",
		"ROLE_ONLY":         "role",
		"ANTHROPIC_API_KEY": "override-secret",
		"OVERRIDE_ONLY":     "override",
		"H2_DIR":            "/tmp/h2",
		"H2_ACTOR":          "child-agent",
		"H2_ROLE":           "coder",
		"H2_SESSION_DIR":    "/tmp/session",
		"CLAUDE_CONFIG_DIR": "/tmp/claude",
		"OTEL_EXPORTER":     "http",
		"SHARED":            "harness",
	}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("ComposeChildEnvMap mismatch\n got: %#v\nwant: %#v", env, want)
	}
	for _, key := range []string{"H2_POD", "CLAUDECODE", "NOT_ALLOWED"} {
		if _, ok := env[key]; ok {
			t.Fatalf("unexpected key %s in composed env: %#v", key, env)
		}
	}
}

func TestComposeChildEnvReturnsSortedKEYValuePairs(t *testing.T) {
	got := ComposeChildEnv(ChildEnvSpec{
		SupervisorEnv: []string{"B=2", "A=1"},
		RuntimeEnv:    map[string]string{"C": "3"},
	})
	want := []string{"A=1", "B=2", "C=3"}
	if !slices.Equal(got, want) {
		t.Fatalf("ComposeChildEnv = %#v, want %#v", got, want)
	}
}

func TestExtractEnvPassthroughUsesBuiltinsAndConfiguredAllowlist(t *testing.T) {
	callerEnv := []string{
		"ANTHROPIC_API_KEY=anthropic",
		"ANTHROPIC_AUTH_TOKEN=token",
		"ANTHROPIC_BASE_URL=https://anthropic.example",
		"OPENROUTER_API_KEY=openrouter",
		"OPENAI_API_KEY=openai",
		"AI_GATEWAY_API_KEY=gateway",
		"TEAM_CONTEXT=backend",
		"UNRELATED=drop",
		"H2_ACTOR=parent-agent",
		"CLAUDECODE=1",
	}

	got := ExtractEnvPassthrough(callerEnv, []string{"TEAM_CONTEXT"})
	want := map[string]string{
		"ANTHROPIC_API_KEY":    "anthropic",
		"ANTHROPIC_AUTH_TOKEN": "token",
		"ANTHROPIC_BASE_URL":   "https://anthropic.example",
		"OPENROUTER_API_KEY":   "openrouter",
		"OPENAI_API_KEY":       "openai",
		"AI_GATEWAY_API_KEY":   "gateway",
		"TEAM_CONTEXT":         "backend",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractEnvPassthrough = %#v, want %#v", got, want)
	}
}

func TestPassthroughDiagnosticsDoNotPersistSecretValues(t *testing.T) {
	keys := PassthroughEnvKeys(map[string]string{
		"OPENAI_API_KEY":     "secret",
		"TEAM_CONTEXT":       "backend",
		"AI_GATEWAY_API_KEY": "secret2",
	})

	want := []string{"AI_GATEWAY_API_KEY", "OPENAI_API_KEY", "TEAM_CONTEXT"}
	if !slices.Equal(keys, want) {
		t.Fatalf("PassthroughEnvKeys = %#v, want %#v", keys, want)
	}
	for _, key := range keys {
		if strings.Contains(key, "secret") || strings.Contains(key, "backend") {
			t.Fatalf("diagnostic key %q appears to contain a value", key)
		}
	}
}

func TestResumeEnvWarningForLaunchScopedOnlyPassthrough(t *testing.T) {
	warning := ResumeEnvWarning(
		map[string]string{
			"OPENAI_API_KEY": "launch-secret",
			"TEAM_CONTEXT":   "backend",
		},
		map[string]string{
			"TEAM_CONTEXT": "backend",
		},
	)

	if !strings.Contains(warning, "OPENAI_API_KEY") {
		t.Fatalf("warning %q should mention missing stable key", warning)
	}
	if strings.Contains(warning, "TEAM_CONTEXT") {
		t.Fatalf("warning %q should not mention stable key", warning)
	}
	if got := ResumeEnvWarning(map[string]string{"OPENAI_API_KEY": "stable"}, map[string]string{"OPENAI_API_KEY": "stable"}); got != "" {
		t.Fatalf("ResumeEnvWarning with stable key = %q, want empty", got)
	}
}
