package bridgeservice

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"h2/internal/bridge"
	"h2/internal/session/message"
	"h2/internal/socketdir"
)

// Service manages bridge instances and routes messages between external
// platforms (Telegram, macOS notifications) and h2 agent sessions.
type Service struct {
	bridges         []bridge.Bridge
	concierge       string   // session name, empty if --no-concierge; guarded by mu
	socketDir       string   // ~/.h2/sockets/
	user            string   // "from" field for inbound messages
	lastSender      string   // tracks last agent who sent outbound
	lastRoutedAgent string   // tracks last agent an inbound message was delivered to
	allowedCommands []string // slash commands allowed on this bridge
	cancel          context.CancelFunc

	// Status tracking.
	startTime        time.Time
	lastActivityTime time.Time
	messagesSent     int64
	messagesReceived int64

	mu sync.Mutex
}

// New creates a bridge service.
func New(bridges []bridge.Bridge, concierge, socketDir, user string, allowedCommands []string) *Service {
	return &Service{
		bridges:         bridges,
		concierge:       concierge,
		socketDir:       socketDir,
		user:            user,
		allowedCommands: allowedCommands,
		startTime:       time.Now(),
	}
}

// Run starts all receiver bridges and the bridge socket listener.
// It blocks until ctx is cancelled.
func (s *Service) Run(ctx context.Context) error {
	ctx, s.cancel = context.WithCancel(ctx)
	log.Printf("bridge: starting for user %q, concierge=%q, %d bridges", s.user, s.concierge, len(s.bridges))
	for _, b := range s.bridges {
		log.Printf("bridge: loaded %s", b.Name())
	}

	// Create socket directory.
	if err := os.MkdirAll(s.socketDir, 0o700); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}

	// Start receivers before creating the socket, so the socket's existence
	// signals that everything is ready.
	for _, b := range s.bridges {
		if r, ok := b.(bridge.Receiver); ok {
			if err := r.Start(ctx, s.handleInbound); err != nil {
				return fmt.Errorf("start receiver %s: %w", b.Name(), err)
			}
		}
	}

	sockPath := filepath.Join(s.socketDir, socketdir.Format(socketdir.TypeBridge, s.user))

	if err := socketdir.ProbeSocket(sockPath, fmt.Sprintf("bridge for user %q", s.user)); err != nil {
		return err
	}

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("listen on bridge socket: %w", err)
	}
	defer func() {
		ln.Close()
		os.Remove(sockPath)
	}()

	go s.acceptLoop(ln)

	// Start typing indicator loop.
	go s.runTypingLoop(ctx)

	// Send startup status message.
	s.sendStartupMessage(ctx)

	// Block until context is done.
	<-ctx.Done()

	// Send shutdown message before cleanup.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	s.sendBridgeStatus(shutdownCtx, "Bridge is shutting down.")

	// Stop receivers.
	for _, b := range s.bridges {
		if r, ok := b.(bridge.Receiver); ok {
			r.Stop()
		}
	}

	// Close all bridges.
	for _, b := range s.bridges {
		b.Close()
	}

	return nil
}

func (s *Service) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *Service) handleConn(conn net.Conn) {
	defer conn.Close()

	req, err := message.ReadRequest(conn)
	if err != nil {
		return
	}

	switch req.Type {
	case "send":
		if err := s.sendOutbound(req.From, req.Body); err != nil {
			message.SendResponse(conn, &message.Response{Error: err.Error()})
		} else {
			message.SendResponse(conn, &message.Response{OK: true})
		}
	case "status":
		message.SendResponse(conn, &message.Response{
			OK:     true,
			Bridge: s.buildBridgeInfo(),
		})
	case "set-concierge":
		resp := s.handleSetConcierge(req.Body)
		message.SendResponse(conn, resp)
	case "remove-concierge":
		resp := s.handleRemoveConcierge()
		message.SendResponse(conn, resp)
	case "stop":
		message.SendResponse(conn, &message.Response{OK: true})
		s.cancel()
	default:
		message.SendResponse(conn, &message.Response{
			Error: "bridge only handles 'send', 'status', 'stop', 'set-concierge', and 'remove-concierge' requests",
		})
	}
}

// handleInbound routes a message from an external platform to an agent.
func (s *Service) handleInbound(targetAgent, body string) {
	log.Printf("bridge: inbound message (target=%q, body=%q)", targetAgent, body)
	s.mu.Lock()
	s.messagesReceived++
	s.lastActivityTime = time.Now()
	s.mu.Unlock()
	target := targetAgent
	if target == "" {
		target = s.resolveDefaultTarget()
	}
	if target == "" {
		log.Printf("bridge: no target agent for inbound message, no agents available")
		s.replyError("No agents are running, unable to deliver message.")
		return
	}
	log.Printf("bridge: routing inbound to %s", target)
	if err := s.sendToAgent(target, s.user, body); err != nil {
		log.Printf("bridge: send to agent %s: %v", target, err)
		s.replyError(fmt.Sprintf("%s agent is not running, unable to deliver message.", target))
	} else {
		s.mu.Lock()
		s.lastRoutedAgent = target
		s.mu.Unlock()
	}
}

// replyError sends an error message back to all Sender bridges.
func (s *Service) replyError(msg string) {
	ctx := context.Background()
	for _, b := range s.bridges {
		if sender, ok := b.(bridge.Sender); ok {
			if err := sender.Send(ctx, msg); err != nil {
				log.Printf("bridge: send error reply via %s: %v", b.Name(), err)
			}
		}
	}
}

// sendBridgeStatus sends a status message tagged with [bridge <user>] to all
// Sender bridges. Callers pass just the body text without the tag prefix.
func (s *Service) sendBridgeStatus(ctx context.Context, text string) {
	tagged := bridge.FormatAgentTag("bridge "+s.user, text)
	for _, b := range s.bridges {
		if sender, ok := b.(bridge.Sender); ok {
			if err := sender.Send(ctx, tagged); err != nil {
				log.Printf("bridge: send status via %s: %v", b.Name(), err)
			}
		}
	}
}

// handleSetConcierge sets or replaces the concierge agent.
func (s *Service) handleSetConcierge(agentName string) *message.Response {
	if agentName == "" {
		return &message.Response{Error: "agent name is required"}
	}

	// Probe the agent socket — non-fatal if unreachable since the agent might start later.
	sockPath := filepath.Join(s.socketDir, socketdir.Format(socketdir.TypeAgent, agentName))
	if conn, err := net.DialTimeout("unix", sockPath, 2*time.Second); err != nil {
		log.Printf("bridge: set-concierge: agent %s not reachable (will set anyway): %v", agentName, err)
	} else {
		conn.Close()
	}

	s.mu.Lock()
	old := s.concierge
	s.concierge = agentName
	s.lastRoutedAgent = "" // reset stale typing target
	s.mu.Unlock()

	// Send status message.
	ctx := context.Background()
	var statusMsg string
	if old == "" {
		statusMsg = fmt.Sprintf("Concierge added. %s", conciergeRouting(agentName))
	} else {
		statusMsg = fmt.Sprintf("Concierge changed. %s", conciergeRouting(agentName))
	}
	s.sendBridgeStatus(ctx, statusMsg)

	return &message.Response{OK: true, OldConcierge: old}
}

// handleRemoveConcierge clears the concierge agent.
func (s *Service) handleRemoveConcierge() *message.Response {
	s.mu.Lock()
	old := s.concierge
	s.concierge = ""
	s.mu.Unlock()

	if old == "" {
		return &message.Response{Error: "no concierge is set"}
	}

	ctx := context.Background()
	firstAgent := s.firstAvailableAgent()
	msg := fmt.Sprintf("Concierge removed. %s", noConciergeRouting(firstAgent))
	s.sendBridgeStatus(ctx, msg)

	return &message.Response{OK: true}
}

// sendOutbound sends a message from an agent to all Sender bridges.
// Messages from non-concierge agents are tagged with [agent-name] so that
// replies can be routed back to the correct agent.
// Returns an error if any bridge fails to deliver the message.
func (s *Service) sendOutbound(from, body string) error {
	s.mu.Lock()
	s.lastSender = from
	s.messagesSent++
	s.lastActivityTime = time.Now()
	concierge := s.concierge
	s.mu.Unlock()

	// Tag messages from non-concierge agents so reply routing works.
	tagged := body
	if from != "" && from != concierge {
		tagged = bridge.FormatAgentTag(from, body)
	}

	ctx := context.Background()
	var errs []string
	for _, b := range s.bridges {
		if sender, ok := b.(bridge.Sender); ok {
			if err := sender.Send(ctx, tagged); err != nil {
				log.Printf("bridge: send via %s: %v", b.Name(), err)
				errs = append(errs, fmt.Sprintf("%s: %v", b.Name(), err))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("send failed: %s", strings.Join(errs, "; "))
	}
	return nil
}

// sendToAgent connects to an agent's socket and sends a message.
func (s *Service) sendToAgent(name, from, body string) error {
	sockPath := filepath.Join(s.socketDir, socketdir.Format(socketdir.TypeAgent, name))
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", name, err)
	}
	defer conn.Close()

	if err := message.SendRequest(conn, &message.Request{
		Type:     "send",
		Priority: "normal",
		From:     from,
		Body:     body,
	}); err != nil {
		return fmt.Errorf("send request: %w", err)
	}

	resp, err := message.ReadResponse(conn)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("agent error: %s", resp.Error)
	}
	return nil
}

// typingTickInterval is the interval between typing indicator refreshes.
// Telegram's typing indicator lasts ~5s, so 4s keeps it alive.
var typingTickInterval = 4 * time.Second

// runTypingLoop periodically checks agent state and sends typing indicators
// to all TypingIndicator bridges while the target agent is active. It also
// monitors concierge liveness and sends a status message if the concierge stops.
func (s *Service) runTypingLoop(ctx context.Context) {
	ticker := time.NewTicker(typingTickInterval)
	defer ticker.Stop()

	conciergeWasAlive := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			concierge := s.concierge
			s.mu.Unlock()

			// Track concierge liveness.
			if concierge != "" {
				_, err := s.queryAgentState(concierge)
				if err != nil {
					if conciergeWasAlive {
						conciergeWasAlive = false
						s.handleConciergeDown(ctx, concierge)
					}
				} else {
					conciergeWasAlive = true
				}
			} else {
				conciergeWasAlive = false
			}

			// Typing indicator: check lastRoutedAgent first, then fallback.
			s.mu.Lock()
			typingTarget := s.lastRoutedAgent
			s.mu.Unlock()
			if typingTarget == "" {
				typingTarget = s.resolveDefaultTarget()
			}
			if typingTarget == "" {
				continue
			}
			state, err := s.queryAgentState(typingTarget)
			if err != nil || state != "active" {
				continue
			}
			for _, b := range s.bridges {
				if ti, ok := b.(bridge.TypingIndicator); ok {
					if err := ti.SendTyping(ctx); err != nil {
						log.Printf("bridge: typing indicator via %s: %v", b.Name(), err)
					}
				}
			}
		}
	}
}

// queryAgentState connects to an agent's socket and returns its state string.
func (s *Service) queryAgentState(name string) (string, error) {
	sockPath := filepath.Join(s.socketDir, socketdir.Format(socketdir.TypeAgent, name))
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	if err := message.SendRequest(conn, &message.Request{Type: "status"}); err != nil {
		return "", err
	}

	resp, err := message.ReadResponse(conn)
	if err != nil {
		return "", err
	}
	if !resp.OK || resp.Agent == nil {
		return "", fmt.Errorf("bad status response")
	}
	return resp.Agent.State, nil
}

// buildBridgeInfo constructs a BridgeInfo snapshot for status responses.
func (s *Service) buildBridgeInfo() *message.BridgeInfo {
	s.mu.Lock()
	sent := s.messagesSent
	received := s.messagesReceived
	lastActivity := s.lastActivityTime
	s.mu.Unlock()

	var channels []string
	for _, b := range s.bridges {
		channels = append(channels, b.Name())
	}

	uptime := time.Since(s.startTime).Round(time.Second).String()

	var lastActivityStr string
	if !lastActivity.IsZero() {
		lastActivityStr = time.Since(lastActivity).Round(time.Second).String()
	}

	return &message.BridgeInfo{
		Name:             s.user,
		Channels:         channels,
		Uptime:           uptime,
		MessagesSent:     sent,
		MessagesReceived: received,
		LastActivity:     lastActivityStr,
	}
}

// resolveDefaultTarget returns the agent to route un-addressed inbound messages to.
func (s *Service) resolveDefaultTarget() string {
	s.mu.Lock()
	concierge := s.concierge
	last := s.lastSender
	s.mu.Unlock()

	if concierge != "" {
		return concierge
	}
	if last != "" {
		return last
	}

	// Fall back to first agent socket.
	agents, _ := socketdir.ListByTypeIn(s.socketDir, socketdir.TypeAgent)
	if len(agents) > 0 {
		return agents[0].Name
	}
	return ""
}

// firstAvailableAgent returns the name of the first agent socket in the
// socket directory, or empty string if none exist.
func (s *Service) firstAvailableAgent() string {
	agents, _ := socketdir.ListByTypeIn(s.socketDir, socketdir.TypeAgent)
	if len(agents) > 0 {
		return agents[0].Name
	}
	return ""
}

// handleConciergeDown clears the concierge and sends a status message when the
// concierge agent is detected as stopped.
func (s *Service) handleConciergeDown(ctx context.Context, agentName string) {
	s.mu.Lock()
	s.concierge = ""
	s.lastRoutedAgent = "" // reset so typing indicator doesn't track stale target
	s.mu.Unlock()

	firstAgent := s.firstAvailableAgent()
	msg := fmt.Sprintf("Concierge agent %s stopped. %s",
		agentName, noConciergeRouting(firstAgent))
	s.sendBridgeStatus(ctx, msg)
}

// sendStartupMessage sends the bridge startup status message to all Sender bridges.
func (s *Service) sendStartupMessage(ctx context.Context) {
	s.mu.Lock()
	concierge := s.concierge
	s.mu.Unlock()

	var routing string
	if concierge != "" {
		routing = conciergeRouting(concierge)
	} else {
		firstAgent := s.firstAvailableAgent()
		if firstAgent == "" {
			msg := fmt.Sprintf("Bridge is up and running, but no agents are running to message. "+
				"Create agents with h2 run. %s", allowedCommandsHint(s.allowedCommands))
			s.sendBridgeStatus(ctx, msg)
			return
		}
		routing = noConciergeRouting(firstAgent)
	}

	msg := fmt.Sprintf("Bridge is up and running. %s %s %s",
		routing, directMessagingHint(), allowedCommandsHint(s.allowedCommands))
	s.sendBridgeStatus(ctx, msg)
}
