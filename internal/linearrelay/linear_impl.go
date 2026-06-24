package linearrelay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"h2/internal/linear"
)

// tokenURL is Linear's OAuth token endpoint.
const tokenURL = "https://api.linear.app/oauth/token"

// liveAuth talks to the real Linear OAuth endpoints.
type liveAuth struct {
	clientID     string
	clientSecret string
	http         *http.Client
}

func (a *liveAuth) client() *http.Client {
	if a.http != nil {
		return a.http
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (a *liveAuth) Exchange(ctx context.Context, code, redirectURI string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", a.clientID)
	form.Set("client_secret", a.clientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("oauth token http %d: %s", resp.StatusCode, string(data))
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("oauth token response had no access_token")
	}
	return out.AccessToken, nil
}

func (a *liveAuth) OrgID(ctx context.Context, token string) (string, error) {
	return linear.NewOAuthClient(token).Organization(ctx)
}

// livePoster posts activities to Linear with a workspace's token.
type livePoster struct{}

func (livePoster) Post(ctx context.Context, token, sessionID string, act linear.AgentActivity) error {
	return linear.NewOAuthClient(token).CreateAgentActivity(ctx, sessionID, act)
}
