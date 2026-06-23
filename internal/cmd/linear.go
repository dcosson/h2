package cmd

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"h2/internal/config"
	"h2/internal/linear"
	"h2/internal/linearagent"
	"h2/internal/session"
	"h2/internal/session/message"
	"h2/internal/socketdir"
	"h2/internal/tmpl"
)

func newLinearCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "linear",
		Short: "Linear agent integration",
		Long:  "Run h2 as a Linear agent: delegate or @mention issues to h2 and it spawns agents to work them.",
	}
	cmd.AddCommand(newLinearServeCmd())
	return cmd
}

func newLinearServeCmd() *cobra.Command {
	var addr string
	var debug bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the Linear agent webhook receiver",
		Long: `Run the inbound webhook receiver for the Linear agent integration.

h2 acknowledges each delegated/@mentioned issue, spawns an agent to work it,
and reports progress back to the Linear agent session.

Requires a 'linear' block in ~/.h2/config.yaml with an oauth_token and an
inbound webhook secret. For dev, expose this receiver to Linear with a tunnel
(e.g. ngrok/Hookdeck) and point your OAuth app's webhook at it.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			lc := cfg.Linear
			if lc == nil || lc.OAuthToken == "" {
				return fmt.Errorf("linear.oauth_token not configured in ~/.h2/config.yaml")
			}

			mode := "webhook"
			path := "/linear/webhook"
			secret := ""
			if lc.Inbound != nil {
				if lc.Inbound.Mode != "" {
					mode = lc.Inbound.Mode
				}
				if lc.Inbound.Path != "" {
					path = lc.Inbound.Path
				}
				secret = lc.Inbound.Secret
				if addr == "" && lc.Inbound.Address != "" {
					addr = lc.Inbound.Address
				}
			}
			if mode != "webhook" {
				return fmt.Errorf("inbound mode %q not supported yet (only 'webhook')", mode)
			}
			if addr == "" {
				addr = ":4747"
			}

			role := "default"
			if lc.Agent != nil && lc.Agent.Role != "" {
				role = lc.Agent.Role
			}

			reporter := linear.NewOAuthClient(lc.OAuthToken)
			source := linearagent.NewWebhookSource(secret, path)
			source.Debug = debug
			runner := &cmdAgentRunner{role: role}
			store := linearagent.NewFileStore(filepath.Join(config.ConfigDir(), "linear-sessions.json"))
			svc := linearagent.New(source, reporter, runner, store)

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			errCh := make(chan error, 2)
			go func() { errCh <- source.Serve(ctx, addr) }()
			go func() { errCh <- svc.Run(ctx) }()

			fmt.Fprintf(os.Stderr, "h2 linear agent listening on %s%s (role=%q)\n", addr, path, role)
			err = <-errCh
			cancel()
			if err != nil && err != context.Canceled {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "", "Webhook listen address (overrides config; default :4747)")
	cmd.Flags().BoolVar(&debug, "debug", false, "Log raw inbound webhook payloads (for confirming Linear's payload shape)")
	return cmd
}

// cmdAgentRunner is the real AgentRunner: it spawns an h2 agent for a delegated
// issue, delivers the issue prompt, and reports completion by polling the
// agent's state over its socket.
//
// MVP limitation: the agent's terminal output is not captured, so Result()
// returns a pointer back to the agent rather than the agent's verbatim final
// message. Verbatim result capture is a later phase (it needs the agent to
// emit an explicit outbound message, the same way bridge replies work).
type cmdAgentRunner struct {
	role string
}

func (r *cmdAgentRunner) Spawn(ctx context.Context, req linearagent.SpawnRequest) (linearagent.AgentHandle, error) {
	name := agentNameForIssue(req.Issue.Identifier)

	rootDir, _ := config.RootDir()
	tctx := &tmpl.Context{
		AgentName: name,
		RoleName:  r.role,
		H2Dir:     config.ConfigDir(),
		H2RootDir: rootDir,
	}
	role, err := config.LoadRoleRenderedWithFuncs(r.role, tctx, config.NameStubFuncs)
	if err != nil {
		return nil, fmt.Errorf("load role %q: %w", r.role, err)
	}

	// Spawn detached, linked to the issue (records the identifier on the
	// session for correlation).
	if err := setupAndForkAgent(name, role, true, "", 0, nil, req.Issue.Identifier); err != nil {
		return nil, fmt.Errorf("spawn agent: %w", err)
	}

	// Deliver the issue context as the agent's task once its socket is up.
	go deliverPromptWhenReady(name, req.Prompt)

	h := &cmdAgentHandle{
		name:     name,
		issueRef: req.Issue.Identifier,
		done:     make(chan struct{}),
		acts:     make(chan linear.AgentActivity, 16),
	}
	go h.watch(ctx)
	return h, nil
}

// DeliverTo routes a follow-up prompt to an already-running agent by name.
func (r *cmdAgentRunner) DeliverTo(_ context.Context, agentName, text string) error {
	return deliverToAgent(agentName, "linear", text)
}

// agentNameForIssue derives a stable, lowercase agent name from an issue
// identifier (e.g. "LIN-9" -> "lin-9"), falling back to a generated name.
func agentNameForIssue(identifier string) string {
	n := strings.ToLower(strings.TrimSpace(identifier))
	if n == "" {
		return session.GenerateName()
	}
	// Avoid collisions with an already-running agent of the same name.
	existing := getExistingAgentNames()
	for _, e := range existing {
		if e == n {
			return n + "-" + session.GenerateName()
		}
	}
	return n
}

// deliverPromptWhenReady waits for the agent socket to come up and delivers the
// prompt as a normal message. Best-effort.
func deliverPromptWhenReady(name, prompt string) {
	if strings.TrimSpace(prompt) == "" {
		return
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if err := deliverToAgent(name, "linear", prompt); err == nil {
			return
		}
		time.Sleep(time.Second)
	}
}

// deliverToAgent sends a message to a running agent over its socket.
func deliverToAgent(name, from, body string) error {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	sockPath, err := socketdir.Find(name)
	if err != nil {
		return err
	}
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := message.SendRequest(conn, &message.Request{Type: "send", From: from, Body: body}); err != nil {
		return err
	}
	message.ReadResponse(conn)
	return nil
}

// cmdAgentHandle tracks a spawned agent, streams its progress as activities,
// and signals completion.
type cmdAgentHandle struct {
	name     string
	issueRef string
	done     chan struct{}
	acts     chan linear.AgentActivity
	mu       sync.Mutex
	result   string
}

func (h *cmdAgentHandle) Name() string                            { return h.name }
func (h *cmdAgentHandle) Done() <-chan struct{}                   { return h.done }
func (h *cmdAgentHandle) Activities() <-chan linear.AgentActivity { return h.acts }

func (h *cmdAgentHandle) Result() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.result
}

// Deliver routes a follow-up prompt to this running agent.
func (h *cmdAgentHandle) Deliver(_ context.Context, text string) error {
	return deliverToAgent(h.name, "linear", text)
}

// watch polls the agent's state, emits an activity on each coarse-status change,
// and closes the streams when the agent's turn appears complete: it has been
// active and then settled into idle, or it exited.
func (h *cmdAgentHandle) watch(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	seenActive := false
	idleStreak := 0
	lastStatus := ""
	for {
		select {
		case <-ctx.Done():
			h.finish("Agent stopped before completion.")
			return
		case <-ticker.C:
			info := h.query()
			if info == nil {
				// Socket gone => agent exited/cleaned up.
				h.finish(h.summary())
				return
			}
			// Emit an activity when the coarse status changes.
			if a, key, ok := activityForState(info.State, info.SubState); ok && key != lastStatus {
				h.emit(a)
				lastStatus = key
			}
			switch info.State {
			case "active":
				seenActive = true
				idleStreak = 0
			case "idle":
				if seenActive {
					idleStreak++
					// ~3 consecutive idle polls (~9s) => turn complete.
					if idleStreak >= 3 {
						h.finish(h.summary())
						return
					}
				}
			case "exited":
				h.finish(h.summary())
				return
			}
		}
	}
}

// activityForState maps an agent (state, subState) to a streamed activity. The
// returned key is used to suppress duplicates while the status is unchanged.
func activityForState(state, sub string) (linear.AgentActivity, string, bool) {
	switch sub {
	case "blocked_on_permission":
		return linear.AgentActivity{
			Type: linear.ActivityElicitation,
			Body: "I need approval to proceed. Approve the pending permission request in the agent (`h2 attach`).",
		}, "blocked_permission", true
	case "tool_use":
		return linear.AgentActivity{Type: linear.ActivityAction, Action: "Running a tool"}, "tool", true
	case "thinking":
		return linear.AgentActivity{Type: linear.ActivityThought, Body: "Thinking…"}, "thinking", true
	case "compacting":
		return linear.AgentActivity{Type: linear.ActivityThought, Body: "Compacting context…"}, "compacting", true
	}
	if state == "active" {
		return linear.AgentActivity{Type: linear.ActivityThought, Body: "Working…"}, "working", true
	}
	return linear.AgentActivity{}, "", false
}

func (h *cmdAgentHandle) emit(a linear.AgentActivity) {
	select {
	case h.acts <- a:
	default: // never block the watcher on a slow reporter
	}
}

func (h *cmdAgentHandle) query() *message.AgentInfo {
	sockPath, err := socketdir.Find(h.name)
	if err != nil {
		return nil
	}
	return queryAgent(sockPath)
}

func (h *cmdAgentHandle) summary() string {
	return fmt.Sprintf("Agent %q finished working on %s. Run `h2 attach %s` to review.",
		h.name, h.issueRef, h.name)
}

func (h *cmdAgentHandle) finish(result string) {
	h.mu.Lock()
	if h.result == "" {
		h.result = result
	}
	h.mu.Unlock()
	select {
	case <-h.done:
	default:
		close(h.acts)
		close(h.done)
	}
}
