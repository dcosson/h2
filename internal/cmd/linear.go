package cmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
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
	"h2/internal/linearrelay"
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
	cmd.AddCommand(newLinearRelayCmd())
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
			if lc == nil {
				return fmt.Errorf("no 'linear' block in ~/.h2/config.yaml (see docs/linear-agent-setup.md)")
			}

			mode := "relay"
			if lc.Inbound != nil && lc.Inbound.Mode != "" {
				mode = lc.Inbound.Mode
			}

			role := "default"
			if lc.Agent != nil && lc.Agent.Role != "" {
				role = lc.Agent.Role
			}
			runner := &cmdAgentRunner{role: role}
			store := linearagent.NewFileStore(filepath.Join(config.ConfigDir(), "linear-sessions.json"))

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			errCh := make(chan error, 2)

			var source linearagent.Source
			var reporter linearagent.Reporter

			switch mode {
			case "relay":
				in := lc.Inbound
				if in == nil || in.PairingToken == "" {
					return fmt.Errorf("relay mode needs linear.inbound.pairing_token (get it by authorizing h2; see docs/linear-agent-setup.md)")
				}
				relayURL := in.RelayURL
				if relayURL == "" {
					relayURL = defaultRelayURL
				}
				rs := linearagent.NewRelaySource(relayURL, in.PairingToken)
				source = rs
				reporter = linearagent.NewRelayReporter(relayURL, in.PairingToken)
				go func() { errCh <- rs.Run(ctx) }()
				fmt.Fprintf(os.Stderr, "h2 linear agent connected via relay %s (role=%q)\n", relayURL, role)

			case "webhook":
				if lc.OAuthToken == "" {
					return fmt.Errorf("webhook mode needs linear.oauth_token")
				}
				path := "/linear/webhook"
				if lc.Inbound != nil && lc.Inbound.Path != "" {
					path = lc.Inbound.Path
				}
				if addr == "" && lc.Inbound != nil && lc.Inbound.Address != "" {
					addr = lc.Inbound.Address
				}
				if addr == "" {
					addr = ":4747"
				}
				secret := ""
				if lc.Inbound != nil {
					secret = lc.Inbound.Secret
				}
				ws := linearagent.NewWebhookSource(secret, path)
				ws.Debug = debug
				source = ws
				reporter = linear.NewOAuthClient(lc.OAuthToken)
				go func() { errCh <- ws.Serve(ctx, addr) }()
				fmt.Fprintf(os.Stderr, "h2 linear agent listening on %s%s (role=%q)\n", addr, path, role)

			default:
				return fmt.Errorf("unknown inbound mode %q (want 'relay' or 'webhook')", mode)
			}

			svc := linearagent.New(source, reporter, runner, store)
			go func() { errCh <- svc.Run(ctx) }()

			err = <-errCh
			cancel()
			if err != nil && err != context.Canceled {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "", "Webhook listen address for webhook mode (default :4747)")
	cmd.Flags().BoolVar(&debug, "debug", false, "Log raw inbound webhook payloads (webhook mode)")
	return cmd
}

// defaultRelayURL is the hosted relay used when relay_url is not configured.
const defaultRelayURL = "https://relay.h2.dev"

// newLinearRelayCmd runs the hosted relay server (operated by the h2 project,
// not end users).
func newLinearRelayCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "relay",
		Short: "Run the hosted Linear relay server",
		Long: `Run the relay that lets users plug h2 into Linear with one click.

The relay is the single public endpoint for a published Linear OAuth app: it
handles the OAuth install, holds each workspace's token, receives all
agent-session webhooks, and routes events to each user's local h2 daemon over an
outbound long-poll connection (so users need no inbound port or tunnel).

Config comes from environment variables (for container deploys) or, if those
are unset, the 'linear.relay' block in ~/.h2/config.yaml:

  H2_RELAY_BASE_URL        public base URL (for the OAuth redirect)   [required]
  H2_RELAY_CLIENT_ID       Linear OAuth app client id                 [required]
  H2_RELAY_CLIENT_SECRET   Linear OAuth app client secret             [required]
  H2_RELAY_WEBHOOK_SECRET  Linear webhook signing secret
  H2_RELAY_ADDR            listen address (default :8080)
  H2_RELAY_STATE_PATH      path to persist installs across restarts`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := loadRelayConfig()
			if err != nil {
				return err
			}
			if rc.ClientID == "" || rc.ClientSecret == "" {
				return fmt.Errorf("relay client_id and client_secret are required (env H2_RELAY_CLIENT_ID/SECRET or linear.relay.*)")
			}
			if rc.BaseURL == "" {
				return fmt.Errorf("relay base_url is required (env H2_RELAY_BASE_URL or linear.relay.base_url)")
			}
			if addr == "" {
				addr = rc.Address
			}
			if addr == "" {
				addr = ":8080"
			}

			srv := linearrelay.New(linearrelay.Config{
				BaseURL:       rc.BaseURL,
				ClientID:      rc.ClientID,
				ClientSecret:  rc.ClientSecret,
				WebhookSecret: rc.WebhookSecret,
				StatePath:     rc.StatePath,
			}, nil, nil)

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			httpSrv := &http.Server{Addr: addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
			go func() {
				<-ctx.Done()
				sd, c := context.WithTimeout(context.Background(), 5*time.Second)
				defer c()
				httpSrv.Shutdown(sd)
			}()
			fmt.Fprintf(os.Stderr, "h2 linear relay listening on %s (install URL: %s/oauth/authorize)\n", addr, rc.BaseURL)
			if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "", "Listen address (default :8080, H2_RELAY_ADDR, or linear.relay.address)")
	return cmd
}

// loadRelayConfig resolves relay config from environment variables first (for
// container deploys, where no h2 directory exists), falling back to the
// linear.relay block in the h2 config file.
func loadRelayConfig() (config.LinearRelayConfig, error) {
	if v := os.Getenv("H2_RELAY_CLIENT_ID"); v != "" {
		return config.LinearRelayConfig{
			Address:       os.Getenv("H2_RELAY_ADDR"),
			BaseURL:       os.Getenv("H2_RELAY_BASE_URL"),
			ClientID:      v,
			ClientSecret:  os.Getenv("H2_RELAY_CLIENT_SECRET"),
			WebhookSecret: os.Getenv("H2_RELAY_WEBHOOK_SECRET"),
			StatePath:     os.Getenv("H2_RELAY_STATE_PATH"),
		}, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return config.LinearRelayConfig{}, fmt.Errorf("no H2_RELAY_* env vars set and could not load config: %w", err)
	}
	if cfg.Linear == nil || cfg.Linear.Relay == nil {
		return config.LinearRelayConfig{}, fmt.Errorf("no relay config: set H2_RELAY_* env vars or a linear.relay block in config")
	}
	return *cfg.Linear.Relay, nil
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

// deliverPromptWhenReady waits until the agent is up AND idle (its TUI has
// finished booting and is ready for input) before delivering the prompt.
// Delivering during boot races the harness's startup screens and can be lost,
// so we gate on the idle state. Best-effort with a fallback send.
func deliverPromptWhenReady(name, prompt string) {
	if strings.TrimSpace(prompt) == "" {
		return
	}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if sockPath, err := socketdir.Find(name); err == nil {
			if info := queryAgent(sockPath); info != nil && info.State == "idle" {
				deliverToAgent(name, "linear", prompt)
				return
			}
		}
		time.Sleep(time.Second)
	}
	// Fallback: the agent never reported idle; try once anyway.
	deliverToAgent(name, "linear", prompt)
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
	if err := message.SendRequest(conn, &message.Request{Type: "send", Priority: "normal", From: from, Body: body}); err != nil {
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
