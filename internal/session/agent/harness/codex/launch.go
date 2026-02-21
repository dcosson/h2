package codex

import (
	"fmt"

	"h2/internal/session/agent/harness"
	"h2/internal/session/agent/shared/otelserver"
)

// BuildLaunchConfig creates an OtelServer for receiving Codex OTEL traces
// and returns a LaunchConfig with the -c flag that configures Codex's
// trace exporter to send to our server.
//
// The caller is responsible for calling OtelServer.Stop() when done.
func BuildLaunchConfig(cb otelserver.Callbacks) (harness.LaunchConfig, *otelserver.OtelServer, error) {
	s, err := otelserver.New(cb)
	if err != nil {
		return harness.LaunchConfig{}, nil, fmt.Errorf("create otel server: %w", err)
	}

	endpoint := fmt.Sprintf("http://127.0.0.1:%d", s.Port)
	cfg := harness.LaunchConfig{
		PrependArgs: []string{
			"-c", fmt.Sprintf(`otel.trace_exporter={otlp-http={endpoint="%s",protocol="json"}}`, endpoint),
		},
	}
	return cfg, s, nil
}
