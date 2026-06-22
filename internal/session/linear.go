package session

import (
	"context"
	"log"

	"h2/internal/config"
	"h2/internal/linear"
)

// linearObserver adapts a Session to linear.Observer, exposing agent state as
// the monitor's lowercase strings without leaking the monitor package into the
// linear package.
type linearObserver struct{ s *Session }

func (o linearObserver) State() (state, subState string) {
	st, sub := o.s.State()
	return st.String(), sub.String()
}

func (o linearObserver) StateChanged() <-chan struct{} { return o.s.StateChanged() }

// startLinearWatcher launches the outbound Linear attachment watcher if the
// session is linked to a Linear issue and a Linear API token is configured.
// It is a no-op otherwise — no config error, no network calls. The watcher runs
// in its own goroutine bound to ctx and swallows all errors, so a misconfigured
// or unreachable Linear never affects the agent or the daemon.
func startLinearWatcher(ctx context.Context, s *Session, rc *config.RuntimeConfig) {
	if rc.LinearIssue == "" {
		return
	}
	cfg, err := config.Load()
	if err != nil || cfg.Linear == nil || cfg.Linear.APIToken == "" {
		return
	}

	client := linear.NewClient(cfg.Linear.APIToken)
	// Stable per-agent dedup key / link. Linear dedupes attachments by URL per
	// issue, so this keeps a single attachment per agent across resume.
	url := "h2://agent/" + rc.AgentName
	w := linear.NewWatcher(client, linearObserver{s: s}, rc.LinearIssue, rc.AgentName, url, log.Printf)
	go w.Run(ctx)
}
