package debugenv

import (
	"os"
	"strings"
)

// OtelDebugLoggingEnabled returns true when OTEL debug logging is enabled.
// Preferred variable is H2_OTEL_DEBUG_LOGGING_ENABLED; OTEL_DEBUG_LOGGING_ENABLED
// is still honored for backward compatibility.
func OtelDebugLoggingEnabled() bool {
	return isTruthyEnv("H2_OTEL_DEBUG_LOGGING_ENABLED") || isTruthyEnv("OTEL_DEBUG_LOGGING_ENABLED")
}

func isTruthyEnv(name string) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}
