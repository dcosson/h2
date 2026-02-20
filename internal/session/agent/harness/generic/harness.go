// Package generic implements the Harness for generic (non-Claude, non-Codex)
// agent commands. It provides output-based idle detection via an internal
// outputcollector.Collector — no OTEL, no hooks.
package generic

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"h2/internal/activitylog"
	"h2/internal/session/agent/harness"
	"h2/internal/session/agent/monitor"
	"h2/internal/session/agent/shared/outputcollector"
)

func init() {
	harness.Register(func(cfg harness.HarnessConfig, log *activitylog.Logger) harness.Harness {
		return New(cfg)
	}, "generic")
}

// GenericHarness implements harness.Harness for arbitrary shell commands.
type GenericHarness struct {
	command   string
	collector *outputcollector.Collector // created in Start()
}

// New creates a GenericHarness for the given command.
func New(cfg harness.HarnessConfig) *GenericHarness {
	return &GenericHarness{command: cfg.Command}
}

// --- Identity ---

func (g *GenericHarness) Name() string           { return "generic" }
func (g *GenericHarness) Command() string         { return g.command }
func (g *GenericHarness) DisplayCommand() string   { return g.command }

// --- Config (no-ops for generic) ---

func (g *GenericHarness) BuildCommandArgs(cfg harness.CommandArgsConfig) []string { return nil }
func (g *GenericHarness) BuildCommandEnvVars(h2Dir string) map[string]string     { return nil }
func (g *GenericHarness) EnsureConfigDir(h2Dir string) error                     { return nil }

// --- Launch ---

// PrepareForLaunch returns an empty LaunchConfig — generic agents don't
// need OTEL servers or special env vars.
func (g *GenericHarness) PrepareForLaunch(agentName, sessionID string) (harness.LaunchConfig, error) {
	if g.command == "" {
		return harness.LaunchConfig{}, fmt.Errorf("generic harness: command is empty")
	}
	return harness.LaunchConfig{}, nil
}

// --- Runtime ---

// Start creates an output collector and bridges its state updates to the
// events channel. Blocks until ctx is cancelled.
func (g *GenericHarness) Start(ctx context.Context, events chan<- monitor.AgentEvent) error {
	g.collector = outputcollector.New(monitor.IdleThreshold)
	for {
		select {
		case su := <-g.collector.StateCh():
			select {
			case events <- monitor.AgentEvent{
				Type:      monitor.EventStateChange,
				Timestamp: time.Now(),
				Data:      monitor.StateChangeData{State: su.State, SubState: su.SubState},
			}:
			case <-ctx.Done():
				return ctx.Err()
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// HandleHookEvent returns false — generic agents don't use hooks.
func (g *GenericHarness) HandleHookEvent(eventName string, payload json.RawMessage) bool {
	return false
}

// HandleOutput feeds the output collector to detect activity/idle transitions.
func (g *GenericHarness) HandleOutput() {
	if g.collector != nil {
		g.collector.NoteOutput()
	}
}

// Stop cleans up the output collector.
func (g *GenericHarness) Stop() {
	if g.collector != nil {
		g.collector.Stop()
	}
}
