package linear

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Observer is the read-only view of agent state the Watcher needs. The daemon
// adapts its AgentMonitor to this interface, which keeps this package free of
// any dependency on the session/monitor packages and makes the Watcher trivial
// to test with a fake.
type Observer interface {
	// State returns the current (state, subState) as the monitor's lowercase
	// strings, e.g. ("active", "tool_use") or ("idle", "").
	State() (state, subState string)
	// StateChanged returns a channel closed when the top-level state changes.
	// The channel is replaced after firing, so callers must re-fetch it.
	StateChanged() <-chan struct{}
}

// sink is the subset of the Linear client the Watcher uses. Client satisfies
// it; tests provide a fake.
type sink interface {
	ResolveIssue(ctx context.Context, identifier string) (string, error)
	UpsertAttachment(ctx context.Context, in AttachmentInput) (string, error)
	UpdateAttachment(ctx context.Context, id string, p AttachmentPatch) error
}

// logf is an optional structured-ish logger. Errors are logged and dropped so
// Linear problems never propagate to the agent.
type logf func(format string, args ...any)

// Watcher observes an agent's state and mirrors it to a Linear attachment. It
// is created by the daemon only when an issue is linked and a token configured.
type Watcher struct {
	client    sink
	obs       Observer
	issueRef  string // human identifier, e.g. "LIN-123"
	agentName string
	url       string // stable dedup key / link for the attachment
	log       logf

	// Tunables (overridable in tests).
	pollInterval     time.Duration // periodic re-check to catch sub-state changes
	minWriteInterval time.Duration // rate cap between Linear writes
}

// NewWatcher builds a Watcher. agentName labels the attachment; url is the
// stable per-agent link Linear dedupes on. log may be nil.
func NewWatcher(client sink, obs Observer, issueRef, agentName, url string, log logf) *Watcher {
	if log == nil {
		log = func(string, ...any) {}
	}
	return &Watcher{
		client:           client,
		obs:              obs,
		issueRef:         issueRef,
		agentName:        agentName,
		url:              url,
		log:              log,
		pollInterval:     2 * time.Second,
		minWriteInterval: 3 * time.Second,
	}
}

// Run resolves the issue, creates the attachment, and mirrors state changes
// until ctx is cancelled. It is intended to be launched in its own goroutine.
// Every failure is logged and dropped; Run never affects the agent.
func (w *Watcher) Run(ctx context.Context) {
	issueID, err := w.client.ResolveIssue(ctx, w.issueRef)
	if err != nil {
		w.log("linear: resolve issue %s: %v", w.issueRef, err)
		return
	}

	var (
		attachmentID string
		lastStatus   string
		lastWrite    time.Time
	)

	// push computes the current coarse status and writes it to Linear if it
	// changed and the rate cap allows. It is the only place that talks to
	// Linear after the initial resolve.
	push := func(now time.Time, force bool) {
		state, sub := w.obs.State()
		st := coarseStatus(state, sub)
		if st.key == lastStatus && !force {
			return
		}
		if !force && !lastWrite.IsZero() && now.Sub(lastWrite) < w.minWriteInterval {
			return // rate-capped; the next poll tick will retry
		}

		meta := map[string]any{
			"h2AgentName": w.agentName,
			"h2State":     st.key,
		}
		title := w.agentName
		subtitle := st.subtitle

		var err error
		if attachmentID == "" {
			attachmentID, err = w.client.UpsertAttachment(ctx, AttachmentInput{
				IssueID:  issueID,
				Title:    title,
				Subtitle: subtitle,
				URL:      w.url,
				Metadata: meta,
			})
		} else {
			err = w.client.UpdateAttachment(ctx, attachmentID, AttachmentPatch{
				Title:    title,
				Subtitle: subtitle,
				Metadata: meta,
			})
		}
		if err != nil {
			w.log("linear: update attachment (%s): %v", st.key, err)
			return
		}
		lastStatus = st.key
		lastWrite = now
	}

	// Initial attachment. We pass a synthetic "now" only via the real clock;
	// force=true ensures the first write always happens.
	push(time.Now(), true)

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		changed := w.obs.StateChanged()
		select {
		case <-ctx.Done():
			// Terminal status on shutdown. Best-effort; force past the rate cap.
			push(time.Now(), true)
			return
		case <-ticker.C:
			push(time.Now(), false)
		case <-changed:
			push(time.Now(), false)
		}
	}
}

// status is a coarse, user-facing summary of agent state.
type status struct {
	key      string // stable identity used for change detection
	subtitle string // human-readable line shown on the attachment
}

// coarseStatus maps the monitor's (state, subState) strings to a coarse status.
// Sub-states that represent a block take priority over the top-level state.
func coarseStatus(state, sub string) status {
	switch sub {
	case "blocked_on_permission":
		return status{"blocked_permission", "Blocked: needs permission"}
	case "auth_error":
		return status{"blocked_auth", "Blocked: auth error"}
	case "server_error":
		return status{"blocked_server", "Blocked: server error"}
	case "usage_limit":
		return status{"blocked_usage", "Blocked: usage limit"}
	case "compacting":
		return status{"compacting", "Working: compacting context"}
	}

	switch state {
	case "initialized":
		return status{"starting", "Starting"}
	case "active":
		switch sub {
		case "thinking":
			return status{"working_thinking", "Working: thinking"}
		case "tool_use":
			return status{"working_tool", "Working: running a tool"}
		default:
			return status{"working", "Working"}
		}
	case "idle":
		return status{"idle", "Idle: awaiting input"}
	case "exited":
		return status{"finished", "Finished"}
	default:
		return status{"unknown", "Unknown"}
	}
}

// splitIdentifier parses a Linear issue identifier like "LIN-123" into its team
// key ("LIN") and issue number (123).
func splitIdentifier(identifier string) (teamKey string, number float64, err error) {
	id := strings.TrimSpace(identifier)
	i := strings.LastIndex(id, "-")
	if i <= 0 || i == len(id)-1 {
		return "", 0, fmt.Errorf("linear: invalid issue identifier %q (want TEAM-123)", identifier)
	}
	teamKey = id[:i]
	n, perr := strconv.Atoi(id[i+1:])
	if perr != nil {
		return "", 0, fmt.Errorf("linear: invalid issue number in %q", identifier)
	}
	return teamKey, float64(n), nil
}
