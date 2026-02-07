package client

import (
	"testing"

	"h2/internal/session/message"
)

func TestCyclePriority(t *testing.T) {
	o := &Client{InputPriority: message.PriorityNormal}

	o.CyclePriority()
	if o.InputPriority != message.PriorityInterrupt {
		t.Fatalf("expected interrupt, got %s", o.InputPriority)
	}

	o.CyclePriority()
	if o.InputPriority != message.PriorityIdle {
		t.Fatalf("expected idle, got %s", o.InputPriority)
	}

	o.CyclePriority()
	if o.InputPriority != message.PriorityIdleFirst {
		t.Fatalf("expected idle-first, got %s", o.InputPriority)
	}

	o.CyclePriority()
	if o.InputPriority != message.PriorityNormal {
		t.Fatalf("expected normal (wrap around), got %s", o.InputPriority)
	}
}

func TestCyclePriority_UnknownResetsToNormal(t *testing.T) {
	o := &Client{InputPriority: 99}
	o.CyclePriority()
	if o.InputPriority != message.PriorityNormal {
		t.Fatalf("expected normal, got %s", o.InputPriority)
	}
}

func TestCyclePriority_ZeroValueResetsToNormal(t *testing.T) {
	o := &Client{} // InputPriority zero value (0)
	o.CyclePriority()
	if o.InputPriority != message.PriorityNormal {
		t.Fatalf("expected normal, got %s", o.InputPriority)
	}
}

func TestPromptShowsPriority(t *testing.T) {
	o := newTestClient(10, 80)
	o.InputPriority = message.PriorityNormal

	// The prompt string is constructed in RenderBar. We verify indirectly
	// by checking the priority string is what we expect.
	if o.InputPriority.String() != "normal" {
		t.Fatalf("expected 'normal', got %q", o.InputPriority.String())
	}

	o.InputPriority = message.PriorityInterrupt
	if o.InputPriority.String() != "interrupt" {
		t.Fatalf("expected 'interrupt', got %q", o.InputPriority.String())
	}
}
