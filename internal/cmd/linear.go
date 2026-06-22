package cmd

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
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
			runner := &cmdAgentRunner{role: role}
			svc := linearagent.New(source, reporter, runner)

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

	h := &cmdAgentHandle{name: name, issueRef: req.Issue.Identifier, done: make(chan struct{})}
	go h.watch(ctx)
	return h, nil
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
		if sockPath, err := socketdir.Find(name); err == nil {
			if conn, err := net.Dial("unix", sockPath); err == nil {
				req := &message.Request{Type: "send", From: "linear", Body: prompt}
				if err := message.SendRequest(conn, req); err == nil {
					message.ReadResponse(conn)
					conn.Close()
					return
				}
				conn.Close()
			}
		}
		time.Sleep(time.Second)
	}
}

// cmdAgentHandle tracks a spawned agent and signals completion.
type cmdAgentHandle struct {
	name     string
	issueRef string
	done     chan struct{}
	mu       sync.Mutex
	result   string
}

func (h *cmdAgentHandle) Done() <-chan struct{} { return h.done }

func (h *cmdAgentHandle) Result() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.result
}

// watch polls the agent's state and closes done when the agent's turn appears
// complete: it has been active and then settled into idle, or it exited.
func (h *cmdAgentHandle) watch(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	seenActive := false
	idleStreak := 0
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
		close(h.done)
	}
}
