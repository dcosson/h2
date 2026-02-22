// Package generic implements the Harness for generic (non-Claude, non-Codex)
// agent commands. It provides output-based idle detection via an internal
// ptycollector.Collector — no OTEL, no hooks.
package generic

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"h2/internal/activitylog"
	"h2/internal/session/agent/harness"
	"h2/internal/session/agent/monitor"
	"h2/internal/session/agent/shared/ptycollector"
)

func init() {
	harness.Register(func(cfg harness.HarnessConfig, log *activitylog.Logger) harness.Harness {
		return New(cfg)
	}, "generic")
}

// GenericHarness implements harness.Harness for arbitrary shell commands.
type GenericHarness struct {
	command   string
	collector *ptycollector.Collector // created in PrepareForLaunch()
}

// New creates a GenericHarness for the given command.
func New(cfg harness.HarnessConfig) *GenericHarness {
	return &GenericHarness{command: cfg.Command}
}

// --- Identity ---

func (g *GenericHarness) Name() string           { return "generic" }
func (g *GenericHarness) Command() string        { return g.command }
func (g *GenericHarness) DisplayCommand() string { return g.command }

// --- Config (no-ops for generic) ---

func (g *GenericHarness) BuildCommandArgs(cfg harness.CommandArgsConfig) []string {
	return harness.CombineArgs(cfg, nil)
}
func (g *GenericHarness) BuildCommandEnvVars(h2Dir string) map[string]string { return nil }
func (g *GenericHarness) EnsureConfigDir(h2Dir string) error                 { return nil }

// --- Launch ---

// PrepareForLaunch creates the output collector and returns an empty
// LaunchConfig — generic agents don't need OTEL servers or special env vars.
// The collector is created here (not in Start) so that HandleOutput() works
// immediately after the child process starts without a race.
func (g *GenericHarness) PrepareForLaunch(agentName, sessionID string, dryRun bool) (harness.LaunchConfig, error) {
	if g.command == "" {
		return harness.LaunchConfig{}, fmt.Errorf("generic harness: command is empty")
	}
	g.collector = ptycollector.New(monitor.IdleThreshold)
	return harness.LaunchConfig{}, nil
}

// --- Runtime ---

// Start bridges the output collector's state updates to the events channel.
// The collector must have been created by PrepareForLaunch.
// Blocks until ctx is cancelled.
func (g *GenericHarness) Start(ctx context.Context, events chan<- monitor.AgentEvent) error {
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

// HandleInterrupt forces an immediate idle state update for local Ctrl+C.
func (g *GenericHarness) HandleInterrupt() bool {
	if g.collector != nil {
		g.collector.SignalInterrupt()
		return true
	}
	return false
}

// HandleOutput feeds the output collector to detect activity/idle transitions.
func (g *GenericHarness) HandleOutput() {
	if g.collector != nil {
		g.collector.SignalOutput()
	}
}

// Stop cleans up the output collector.
func (g *GenericHarness) Stop() {
	if g.collector != nil {
		g.collector.Stop()
	}
}
