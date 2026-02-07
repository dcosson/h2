package bridgeservice

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"

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
	mu         sync.Mutex
}

// New creates a bridge service.
func New(bridges []bridge.Bridge, concierge, socketDir, user string) *Service {
	return &Service{
		bridges:   bridges,
		concierge: concierge,
		socketDir: socketDir,
		user:      user,
	}
}

// Run starts all receiver bridges and the bridge socket listener.
// It blocks until ctx is cancelled.
func (s *Service) Run(ctx context.Context) error {
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
	default:
		message.SendResponse(conn, &message.Response{
			Error: "bridge only handles 'send' requests",
		})
	}
}

// handleInbound routes a message from an external platform to an agent.
func (s *Service) handleInbound(targetAgent, body string) {
	log.Printf("bridge: inbound message (target=%q, body=%q)", targetAgent, body)
	target := targetAgent
	if target == "" {
		target = s.resolveDefaultTarget()
	}
	if target == "" {
		log.Printf("bridge: no target agent for inbound message, dropping")
		return
	}
	log.Printf("bridge: routing inbound to %s", target)
	if err := s.sendToAgent(target, s.user, body); err != nil {
		log.Printf("bridge: send to agent %s: %v", target, err)
	}
}

// handleOutbound sends a message from an agent to all Sender bridges.
func (s *Service) handleOutbound(from, body string) {
	s.mu.Lock()
	s.lastSender = from
	s.mu.Unlock()

	ctx := context.Background()
	for _, b := range s.bridges {
		if sender, ok := b.(bridge.Sender); ok {
			if err := sender.Send(ctx, body); err != nil {
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
