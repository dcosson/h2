package linearagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"h2/internal/linear"
)

// RelaySource is a Source backed by the hosted relay. The local daemon dials
// out to the relay and long-polls for agent-session events, so it needs no
// inbound port, tunnel, or OAuth token. It reconnects with backoff and is the
// plug-and-play transport (mode: relay).
type RelaySource struct {
	baseURL string
	pair    string
	events  chan AgentSessionEvent
	http    *http.Client
}

// NewRelaySource creates a relay-backed Source. baseURL is the relay root (e.g.
// https://relay.h2.dev); pairingToken links this daemon to a workspace install.
func NewRelaySource(baseURL, pairingToken string) *RelaySource {
	return &RelaySource{
		baseURL: baseURL,
		pair:    pairingToken,
		events:  make(chan AgentSessionEvent, 64),
		// Slightly longer than the relay's poll timeout so a normal 204 isn't
		// treated as a client timeout.
		http: &http.Client{Timeout: 60 * time.Second},
	}
}

func (s *RelaySource) Events() <-chan AgentSessionEvent { return s.events }

// Run long-polls the relay until ctx is cancelled. Transient errors back off;
// 200 emits an event; 204 immediately re-polls.
func (s *RelaySource) Run(ctx context.Context) error {
	url := s.baseURL + "/poll?pair=" + s.pair
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	log.Printf("linear: relay source connecting to %s", s.baseURL)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ev, ok, err := s.pollOnce(ctx, url)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("linear: relay poll error (retry in %s): %v", backoff, err)
			if !sleep(ctx, backoff) {
				return ctx.Err()
			}
			backoff = min2(backoff*2, maxBackoff)
			continue
		}
		backoff = time.Second // healthy poll resets backoff
		if !ok {
			continue // 204 timeout, re-poll
		}
		if ev.IsAgentSession() {
			select {
			case s.events <- ev:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (s *RelaySource) pollOnce(ctx context.Context, url string) (AgentSessionEvent, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return AgentSessionEvent{}, false, err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return AgentSessionEvent{}, false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return AgentSessionEvent{}, false, nil
	case http.StatusOK:
		var ev AgentSessionEvent
		if err := json.NewDecoder(resp.Body).Decode(&ev); err != nil {
			return AgentSessionEvent{}, false, err
		}
		return ev, true, nil
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return AgentSessionEvent{}, false, fmt.Errorf("relay poll http %d: %s", resp.StatusCode, string(body))
	}
}

// RelayReporter posts activities to Linear through the relay (which holds the
// workspace's OAuth token). Implements Reporter.
type RelayReporter struct {
	baseURL string
	pair    string
	http    *http.Client
}

// NewRelayReporter creates a relay-backed Reporter.
func NewRelayReporter(baseURL, pairingToken string) *RelayReporter {
	return &RelayReporter{
		baseURL: baseURL,
		pair:    pairingToken,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (r *RelayReporter) CreateAgentActivity(ctx context.Context, sessionID string, a linear.AgentActivity) error {
	body, err := json.Marshal(map[string]any{"sessionId": sessionID, "activity": a})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/activity?pair="+r.pair, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("relay activity http %d: %s", resp.StatusCode, string(msg))
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func min2(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
