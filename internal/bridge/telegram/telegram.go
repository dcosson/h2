package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"h2/internal/bridge"
)

const (
	initialBackoff = 1 * time.Second
	maxBackoff     = 60 * time.Second
)

// Telegram implements bridge.Bridge, bridge.Sender, and bridge.Receiver
// using the Telegram Bot API. Standard library only — no external Telegram SDK.
type Telegram struct {
	Token           string
	ChatID          int64
	AllowedCommands []string

	// BaseURL overrides the Telegram API base for testing.
	// If empty, defaults to "https://api.telegram.org".
	BaseURL string

	client http.Client
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	offset int64
}

func (t *Telegram) Name() string { return "telegram" }

func (t *Telegram) Close() error {
	t.Stop()
	return nil
}

func (t *Telegram) apiURL(method string) string {
	base := t.BaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	return fmt.Sprintf("%s/bot%s/%s", base, t.Token, method)
}

// Send posts a text message to the configured chat.
func (t *Telegram) Send(ctx context.Context, text string) error {
	resp, err := t.client.PostForm(t.apiURL("sendMessage"), url.Values{
		"chat_id": {strconv.FormatInt(t.ChatID, 10)},
		"text":    {text},
	})
	if err != nil {
		return fmt.Errorf("telegram send: %w", err)
	}
	defer resp.Body.Close()

	var result apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("telegram send: decode response: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("telegram send: API error: %s", result.Description)
	}
	return nil
}

// Start begins long-polling for incoming messages. It spawns a goroutine
// that polls getUpdates and calls handler for each message from the
// configured ChatID.
func (t *Telegram) Start(ctx context.Context, handler bridge.InboundHandler) error {
	ctx, cancel := context.WithCancel(ctx)
	t.mu.Lock()
	t.cancel = cancel
	t.mu.Unlock()

	t.wg.Add(1)
	go t.poll(ctx, handler)
	return nil
}

// Stop cancels the polling goroutine and waits for it to exit.
func (t *Telegram) Stop() {
	t.mu.Lock()
	cancel := t.cancel
	t.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	t.wg.Wait()
}

func (t *Telegram) poll(ctx context.Context, handler bridge.InboundHandler) {
	defer t.wg.Done()

	backoff := initialBackoff

	for {
		if ctx.Err() != nil {
			return
		}

		updates, err := t.getUpdates(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		backoff = initialBackoff

		for _, u := range updates {
			if u.UpdateID >= t.offset {
				t.offset = u.UpdateID + 1
			}
			if u.Message == nil || u.Message.Chat.ID != t.ChatID {
				continue
			}
			// Check for slash commands before agent routing.
			cmd, args := bridge.ParseSlashCommand(u.Message.Text, t.AllowedCommands)
			if cmd != "" {
				log.Printf("bridge: telegram: executing command /%s %s", cmd, args)
				go t.execAndReply(ctx, cmd, args)
				continue
			}
			agent, body := bridge.ParseAgentPrefix(u.Message.Text)
			// If no explicit prefix, check reply-to message for agent tag.
			if agent == "" && u.Message.ReplyToMessage != nil {
				agent = bridge.ParseAgentTag(u.Message.ReplyToMessage.Text)
			}
			handler(agent, body)
		}
	}
}

func (t *Telegram) execAndReply(ctx context.Context, cmd, args string) {
	result := bridge.ExecCommand(cmd, args)
	tagged := fmt.Sprintf("[%s result]\n%s", cmd, result)
	if err := t.Send(ctx, tagged); err != nil {
		log.Printf("bridge: telegram: send command result: %v", err)
	}
}

func (t *Telegram) getUpdates(ctx context.Context) ([]update, error) {
	params := url.Values{
		"offset":  {strconv.FormatInt(t.offset, 10)},
		"timeout": {"30"},
	}

	req, err := http.NewRequestWithContext(ctx, "GET", t.apiURL("getUpdates")+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result getUpdatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("getUpdates: API error: %s", result.Description)
	}
	return result.Result, nil
}

// SendTyping sends a "typing" chat action to the configured chat.
// The indicator is shown for ~5 seconds by Telegram.
func (t *Telegram) SendTyping(ctx context.Context) error {
	resp, err := t.client.PostForm(t.apiURL("sendChatAction"), url.Values{
		"chat_id": {strconv.FormatInt(t.ChatID, 10)},
		"action":  {"typing"},
	})
	if err != nil {
		return fmt.Errorf("telegram sendChatAction: %w", err)
	}
	defer resp.Body.Close()

	var result apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("telegram sendChatAction: decode response: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("telegram sendChatAction: API error: %s", result.Description)
	}
	return nil
}

// Unexported types for JSON parsing.

type apiResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description,omitempty"`
}

type getUpdatesResponse struct {
	OK          bool     `json:"ok"`
	Description string   `json:"description,omitempty"`
	Result      []update `json:"result"`
}

type update struct {
	UpdateID int64    `json:"update_id"`
	Message  *message `json:"message,omitempty"`
}

type message struct {
	Text           string   `json:"text"`
	Chat           chat     `json:"chat"`
	ReplyToMessage *message `json:"reply_to_message,omitempty"`
}

type chat struct {
	ID int64 `json:"id"`
}
