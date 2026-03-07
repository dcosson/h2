package automation

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"h2/internal/session/message"
)

// MaxConcurrentExec is the maximum number of concurrent exec actions per agent.
var MaxConcurrentExec = 3

// ExecTimeout is the default timeout for exec actions.
var ExecTimeout = 60 * time.Second

// MessageEnqueuer is the interface needed by ActionRunner to inject messages.
// This avoids importing the full MessageQueue into this package — callers
// provide a concrete implementation backed by message.PrepareMessage.
type MessageEnqueuer interface {
	EnqueueMessage(from, body string, priority message.Priority) (string, error)
}

// ActionRunner dispatches actions (exec or message) with concurrency control.
type ActionRunner struct {
	enqueuer MessageEnqueuer
	baseEnv  map[string]string // base env vars inherited by all actions
	logger   *slog.Logger

	sem chan struct{} // semaphore for concurrent exec
	wg  sync.WaitGroup
}

// NewActionRunner creates a runner with the given message enqueuer and base env.
func NewActionRunner(enqueuer MessageEnqueuer, baseEnv map[string]string, logger *slog.Logger) *ActionRunner {
	if logger == nil {
		logger = slog.Default()
	}
	return &ActionRunner{
		enqueuer: enqueuer,
		baseEnv:  baseEnv,
		logger:   logger,
		sem:      make(chan struct{}, MaxConcurrentExec),
	}
}

// Run dispatches an action with the given extra environment variables.
// For exec actions, this is non-blocking (runs in a goroutine).
// For message actions, this is synchronous.
// Returns an error only for message actions or if the exec was dropped.
func (r *ActionRunner) Run(action Action, extraEnv map[string]string) error {
	if action.Message != "" {
		return r.runMessage(action)
	}
	return r.runExec(action, extraEnv)
}

// Wait blocks until all in-flight exec actions complete.
func (r *ActionRunner) Wait() {
	r.wg.Wait()
}

func (r *ActionRunner) runMessage(action Action) error {
	from := action.From
	if from == "" {
		from = "h2-automation"
	}
	pri := message.PriorityNormal
	if action.Priority != "" {
		p, ok := message.ParsePriority(action.Priority)
		if !ok {
			return fmt.Errorf("invalid priority %q", action.Priority)
		}
		pri = p
	}
	_, err := r.enqueuer.EnqueueMessage(from, action.Message, pri)
	return err
}

func (r *ActionRunner) runExec(action Action, extraEnv map[string]string) error {
	// Try to acquire semaphore slot (non-blocking).
	select {
	case r.sem <- struct{}{}:
	default:
		r.logger.Warn("action dropped (max concurrent exec reached)",
			"command", truncate(action.Exec, 80))
		return fmt.Errorf("exec dropped: max concurrent actions (%d) reached", MaxConcurrentExec)
	}

	env := r.MergeEnv(extraEnv)

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer func() { <-r.sem }()

		ctx, cancel := context.WithTimeout(context.Background(), ExecTimeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, "sh", "-c", action.Exec)
		cmd.Env = buildFullEnv(env)
		cmd.Stdout = os.Stdout // TODO: route to activity log
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			r.logger.Warn("exec action failed",
				"command", truncate(action.Exec, 80),
				"error", err)
		}
	}()
	return nil
}

// MergeEnv combines base env with extra env. Extra overrides base.
// Exported so that trigger/schedule engines can build condition env.
func (r *ActionRunner) MergeEnv(extra map[string]string) map[string]string {
	merged := make(map[string]string, len(r.baseEnv)+len(extra))
	for k, v := range r.baseEnv {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	return merged
}

// buildFullEnv creates a []string env from os.Environ() plus overrides.
func buildFullEnv(overrides map[string]string) []string {
	base := os.Environ()
	result := make([]string, 0, len(base)+len(overrides))

	// Build a set of override keys for quick lookup.
	overrideKeys := make(map[string]bool, len(overrides))
	for k := range overrides {
		overrideKeys[k] = true
	}

	// Copy base env, skipping keys that are overridden.
	for _, entry := range base {
		key := envKey(entry)
		if overrideKeys[key] {
			continue
		}
		result = append(result, entry)
	}

	// Append overrides.
	for k, v := range overrides {
		result = append(result, k+"="+v)
	}
	return result
}

// envKey extracts the key from a "KEY=value" string.
func envKey(entry string) string {
	for i := 0; i < len(entry); i++ {
		if entry[i] == '=' {
			return entry[:i]
		}
	}
	return entry
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
