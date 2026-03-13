// Package harness defines the Harness interface — a unified abstraction
// for agent integrations (Claude Code, Codex, generic). Each harness
// encapsulates all agent-type-specific behavior: config, launch, telemetry,
// hooks, and lifecycle.
package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"h2/internal/activitylog"
	"h2/internal/config"
	"h2/internal/session/agent/monitor"
)

// HarnessFactory creates a Harness from a RuntimeConfig and logger.
type HarnessFactory func(*config.RuntimeConfig, *activitylog.Logger) Harness

type registeredHarness struct {
	factory        HarnessFactory
	canonicalName  string
	defaultCommand string
}

// HarnessSpec describes a harness registration.
type HarnessSpec struct {
	Names          []string
	Factory        HarnessFactory
	DefaultCommand string
}

// registry holds registered harness definitions keyed by alias/type name.
var registry = map[string]registeredHarness{}

// Register adds a harness definition for the given type name(s).
// Called from init() in each harness sub-package.
func Register(spec HarnessSpec) {
	if len(spec.Names) == 0 {
		panic("harness.Register: at least one name is required")
	}
	if spec.Factory == nil {
		panic("harness.Register: factory is required")
	}
	canonical := spec.Names[0]
	for _, name := range spec.Names {
		registry[name] = registeredHarness{
			factory:        spec.Factory,
			canonicalName:  canonical,
			defaultCommand: spec.DefaultCommand,
		}
	}
}

// Harness defines how h2 launches, monitors, and interacts with a specific
// kind of agent. Each supported agent (Claude Code, Codex, generic shell)
// implements this interface.
type Harness interface {
	// Identity
	Name() string           // "claude_code", "codex", or "generic"
	Command() string        // executable name: "claude", "codex", or custom
	DisplayCommand() string // for display

	// Config (called before launch).
	// BuildCommandArgs returns the complete argument list for the child process.
	// The harness pulls all config from its stored RuntimeConfig.
	// prependArgs come from PrepareForLaunch, extraArgs from the command line.
	BuildCommandArgs(prependArgs, extraArgs []string) []string
	BuildCommandEnvVars(h2Dir string) map[string]string
	EnsureConfigDir(h2Dir string) error

	// Launch (called once, before child process starts).
	// When dryRun is true, returns what the LaunchConfig would look like
	// without starting servers or creating resources. Placeholder values
	// (e.g. "<PORT>") may be used for dynamic fields.
	PrepareForLaunch(dryRun bool) (LaunchConfig, error)

	// Resume support.
	SupportsResume() bool // whether this harness supports --resume

	// Runtime (called after child process starts)
	Start(ctx context.Context, events chan<- monitor.AgentEvent) error
	HandleHookEvent(eventName string, payload json.RawMessage) bool
	HandleInterrupt() bool // signal local interrupt (e.g. Ctrl+C)
	HandleOutput()         // signal that child process produced output
	Stop()
}

// CombineArgs assembles the complete child process argument list from
// prependArgs, extraArgs, and harness-specific roleArgs.
// Order: prependArgs + extraArgs + roleArgs.
func CombineArgs(prependArgs, extraArgs, roleArgs []string) []string {
	var args []string
	args = append(args, prependArgs...)
	args = append(args, extraArgs...)
	args = append(args, roleArgs...)
	return args
}

// LaunchConfig holds configuration to inject into the agent child process.
type LaunchConfig struct {
	Env         map[string]string // extra env vars for child process
	PrependArgs []string          // args to prepend before user args
}

// InputSender delivers input to an agent process.
// The default implementation writes to PTY stdin, but agent types
// with richer APIs can override this.
type InputSender interface {
	// SendInput delivers text input to the agent.
	SendInput(text string) error

	// SendInterrupt sends an interrupt signal (e.g. Ctrl+C).
	SendInterrupt() error
}

// PTYInputSender is the default InputSender that writes to a PTY master.
// It works for any agent type that accepts input via stdin (Claude Code,
// Codex, generic commands).
type PTYInputSender struct {
	pty io.Writer // PTY master file descriptor
}

// NewPTYInputSender creates a PTYInputSender that writes to the given writer.
// Typically called with vt.Ptm (the PTY master *os.File).
func NewPTYInputSender(pty io.Writer) *PTYInputSender {
	return &PTYInputSender{pty: pty}
}

// SendInput writes text to the PTY stdin.
func (s *PTYInputSender) SendInput(text string) error {
	_, err := s.pty.Write([]byte(text))
	return err
}

// SendInterrupt sends Ctrl+C (ETX, 0x03) to the PTY.
func (s *PTYInputSender) SendInterrupt() error {
	_, err := s.pty.Write([]byte{0x03})
	return err
}

// Resolve maps a RuntimeConfig to a concrete Harness implementation.
// Returns an error for unknown harness types or invalid configs.
func Resolve(rc *config.RuntimeConfig, log *activitylog.Logger) (Harness, error) {
	reg, ok := registry[rc.HarnessType]
	if !ok {
		return nil, fmt.Errorf("unknown harness type: %q (supported: claude_code, codex, generic)", rc.HarnessType)
	}
	if reg.canonicalName == "generic" && rc.Command == "" {
		return nil, fmt.Errorf("generic harness requires a command")
	}
	return reg.factory(rc, log), nil
}

// ResolveNativeSessionLogPath resolves the native session log path from
// the RuntimeConfig's NativeLogPathSuffix and HarnessConfigDir. Returns ""
// if not set.
func ResolveNativeSessionLogPath(rc *config.RuntimeConfig) string {
	return rc.NativeSessionLogPath()
}

// CanonicalName resolves a harness alias to its canonical name.
func CanonicalName(name string) string {
	if reg, ok := registry[name]; ok {
		return reg.canonicalName
	}
	return name
}

// DefaultCommand returns the registered default command for a harness type/alias.
func DefaultCommand(name string) string {
	if reg, ok := registry[name]; ok {
		return reg.defaultCommand
	}
	return ""
}
