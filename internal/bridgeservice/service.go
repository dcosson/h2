package bridgeservice

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"h2/internal/bridge"
	"h2/internal/session/message"
	"h2/internal/socketdir"
)

// Service manages bridge instances and routes messages between external
// platforms (Telegram, macOS notifications) and h2 agent sessions.
type Service struct {
	bridges    []bridge.Bridge
	concierge  string // session name, empty if --no-concierge
	socketDir  string // ~/.h2/sockets/
	user       string // "from" field for inbound messages
	lastSender string // tracks last agent who sent outbound
	cancel     context.CancelFunc

	// Status tracking.
	startTime        time.Time
	lastActivityTime time.Time
	messagesSent     int64
	messagesReceived int64

	mu sync.Mutex
}

// New creates a bridge service.
func New(bridges []bridge.Bridge, concierge, socketDir, user string) *Service {
	return &Service{
		bridges:   bridges,
		concierge: concierge,
		socketDir: socketDir,
		user:      user,
		startTime: time.Now(),
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

	// Clean up stale socket.
	if _, err := os.Stat(sockPath); err == nil {
		os.Remove(sockPath)
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

	// Block until context is done.
	<-ctx.Done()

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
		s.handleOutbound(req.From, req.Body)
		message.SendResponse(conn, &message.Response{OK: true})
	case "status":
		message.SendResponse(conn, &message.Response{
			OK:     true,
			Bridge: s.buildBridgeInfo(),
		})
	case "stop":
		message.SendResponse(conn, &message.Response{OK: true})
		s.cancel()
	default:
		message.SendResponse(conn, &message.Response{
			Error: "bridge only handles 'send', 'status', and 'stop' requests",
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

// handleOutbound sends a message from an agent to all Sender bridges.
// Messages from non-concierge agents are tagged with [agent-name] so that
// replies can be routed back to the correct agent.
func (s *Service) handleOutbound(from, body string) {
	s.mu.Lock()
	s.lastSender = from
	s.messagesSent++
	s.lastActivityTime = time.Now()
	s.mu.Unlock()

	// Tag messages from non-concierge agents so reply routing works.
	tagged := body
	if from != "" && from != s.concierge {
		tagged = bridge.FormatAgentTag(from, body)
	}

	ctx := context.Background()
	for _, b := range s.bridges {
		if sender, ok := b.(bridge.Sender); ok {
			if err := sender.Send(ctx, tagged); err != nil {
				log.Printf("bridge: send via %s: %v", b.Name(), err)
			}
		}
	}
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

// runTypingLoop periodically checks the concierge agent's state and sends
// typing indicators to all TypingIndicator bridges while the agent is active.
func (s *Service) runTypingLoop(ctx context.Context) {
	ticker := time.NewTicker(typingTickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			target := s.resolveDefaultTarget()
			if target == "" {
				continue
			}
			state, err := s.queryAgentState(target)
			if err != nil {
				continue // agent not running yet, ignore
			}
			if state != "active" {
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
	if s.concierge != "" {
		return s.concierge
	}

	s.mu.Lock()
	last := s.lastSender
	s.mu.Unlock()
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
