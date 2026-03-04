package debugenv

import "testing"

func TestOtelDebugLoggingEnabled(t *testing.T) {
	tests := []struct {
		name      string
		h2Val     string
		legacyVal string
		want      bool
	}{
		{name: "off by default", want: false},
		{name: "h2 enabled", h2Val: "1", want: true},
		{name: "h2 enabled true", h2Val: "true", want: true},
		{name: "legacy enabled", legacyVal: "yes", want: true},
		{name: "h2 false legacy true", h2Val: "0", legacyVal: "on", want: true},
		{name: "both disabled", h2Val: "no", legacyVal: "0", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("H2_OTEL_DEBUG_LOGGING_ENABLED", tt.h2Val)
			t.Setenv("OTEL_DEBUG_LOGGING_ENABLED", tt.legacyVal)
			if got := OtelDebugLoggingEnabled(); got != tt.want {
				t.Fatalf("OtelDebugLoggingEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}
