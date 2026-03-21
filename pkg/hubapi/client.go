package hubapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Session mirrors the hub's session list payload.
type Session struct {
	ID           string    `json:"id"`
	OrgID        string    `json:"org_id"`
	UserID       string    `json:"user_id"`
	AgentID      string    `json:"agent_id"`
	RuntimeID    string    `json:"runtime_id"`
	Profile      string    `json:"profile"`
	State        string    `json:"state"`
	NativeHandle string    `json:"native_handle,omitempty"`
	ResumedFrom  string    `json:"resumed_from,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	AgentName    string    `json:"agent_name,omitempty"`
	MessageCount int       `json:"message_count"`
}

// Client is a small HTTP client for the hub API.
type Client struct {
	baseURL    string
	httpClient *http.Client
	token      string
}

// New creates a hub API client rooted at baseURL.
func New(baseURL string, httpClient *http.Client) (*Client, error) {
	normalized, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		baseURL:    normalized,
		httpClient: httpClient,
	}, nil
}

// NormalizeBaseURL accepts either an HTTP(S) base URL or an Amurg WebSocket URL
// such as wss://host/ws/runtime and returns the HTTP(S) base URL.
func NormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("hub URL is required")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse hub URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("hub URL must include scheme and host")
	}

	switch u.Scheme {
	case "wss":
		u.Scheme = "https"
	case "ws":
		u.Scheme = "http"
	case "https", "http":
	default:
		return "", fmt.Errorf("unsupported hub URL scheme %q", u.Scheme)
	}

	switch {
	case strings.HasSuffix(u.Path, "/ws/runtime"):
		u.Path = strings.TrimSuffix(u.Path, "/ws/runtime")
	case strings.HasSuffix(u.Path, "/ws/client"):
		u.Path = strings.TrimSuffix(u.Path, "/ws/client")
	}

	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""

	return u.String(), nil
}

// SetToken updates the bearer token used by authenticated requests.
func (c *Client) SetToken(token string) {
	c.token = strings.TrimSpace(token)
}

// Login exchanges username/password for a JWT using the builtin auth endpoint.
func (c *Client) Login(ctx context.Context, username, password string) (string, error) {
	var resp struct {
		Token string `json:"token"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/auth/login", map[string]string{
		"username": username,
		"password": password,
	}, false, &resp); err != nil {
		return "", err
	}
	if resp.Token == "" {
		return "", fmt.Errorf("hub login returned an empty token")
	}
	return resp.Token, nil
}

// ListSessions returns sessions visible to the current user.
func (c *Client) ListSessions(ctx context.Context) ([]Session, error) {
	var sessions []Session
	if err := c.do(ctx, http.MethodGet, "/api/sessions", nil, true, &sessions); err != nil {
		return nil, err
	}
	if sessions == nil {
		sessions = []Session{}
	}
	return sessions, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, auth bool, out any) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		if c.token == "" {
			return fmt.Errorf("missing bearer token")
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := readAPIError(resp.Body)
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("hub API %s %s failed: %s (HTTP %d)", method, path, msg, resp.StatusCode)
	}

	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func readAPIError(r io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(r, 4096))
	if err != nil {
		return ""
	}

	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &body); err == nil {
		switch {
		case body.Error != "":
			return body.Error
		case body.Message != "":
			return body.Message
		}
	}
	return strings.TrimSpace(string(data))
}
