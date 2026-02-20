package codex

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"h2/internal/session/agent/shared/otelserver"
)

func TestBuildLaunchConfig_CreatesOtelServer(t *testing.T) {
	cfg, s, err := BuildLaunchConfig(otelserver.Callbacks{})
	if err != nil {
		t.Fatalf("BuildLaunchConfig: %v", err)
	}
	defer s.Stop()

	if s.Port == 0 {
		t.Fatal("expected non-zero OtelServer port")
	}

	// PrependArgs should be ["-c", "otel.trace_exporter={...}"]
	if len(cfg.PrependArgs) != 2 {
		t.Fatalf("PrependArgs length = %d, want 2", len(cfg.PrependArgs))
	}
	if cfg.PrependArgs[0] != "-c" {
		t.Errorf("PrependArgs[0] = %q, want %q", cfg.PrependArgs[0], "-c")
	}

	// The -c value should contain the OTEL trace exporter config with the correct port.
	cFlag := cfg.PrependArgs[1]
	expectedEndpoint := fmt.Sprintf("http://127.0.0.1:%d", s.Port)
	if !strings.Contains(cFlag, expectedEndpoint) {
		t.Errorf("PrependArgs[1] = %q, want to contain %q", cFlag, expectedEndpoint)
	}
	expectedPrefix := `otel.trace_exporter={type="otlp-http"`
	if !strings.Contains(cFlag, expectedPrefix) {
		t.Errorf("PrependArgs[1] = %q, want to contain %q", cFlag, expectedPrefix)
	}
}

func TestBuildLaunchConfig_TwoServers_DifferentPorts(t *testing.T) {
	cfg1, s1, err := BuildLaunchConfig(otelserver.Callbacks{})
	if err != nil {
		t.Fatalf("BuildLaunchConfig 1: %v", err)
	}
	defer s1.Stop()

	cfg2, s2, err := BuildLaunchConfig(otelserver.Callbacks{})
	if err != nil {
		t.Fatalf("BuildLaunchConfig 2: %v", err)
	}
	defer s2.Stop()

	if s1.Port == s2.Port {
		t.Fatalf("expected different ports, both got %d", s1.Port)
	}

	// Verify each config has its own port.
	if cfg1.PrependArgs[1] == cfg2.PrependArgs[1] {
		t.Error("expected different -c flags for different servers")
	}
}

func TestBuildLaunchConfig_CallbacksWired(t *testing.T) {
	var gotTraces bool
	cfg, s, err := BuildLaunchConfig(otelserver.Callbacks{
		OnTraces: func(body []byte) {
			gotTraces = true
		},
	})
	if err != nil {
		t.Fatalf("BuildLaunchConfig: %v", err)
	}
	defer s.Stop()

	// Verify cfg is valid (not empty).
	if len(cfg.PrependArgs) != 2 {
		t.Fatalf("PrependArgs length = %d, want 2", len(cfg.PrependArgs))
	}

	// Post a trace payload to verify the callback is wired.
	url := fmt.Sprintf("http://127.0.0.1:%d/v1/traces", s.Port)
	resp, err := http.Post(url, "application/json", strings.NewReader(`{"resourceSpans":[]}`))
	if err != nil {
		t.Fatalf("POST /v1/traces: %v", err)
	}
	resp.Body.Close()

	if !gotTraces {
		t.Error("OnTraces callback was not called")
	}
}
