// Package hub manages the runtime's outbound WebSocket connection to the hub.
package hub

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/amurg-ai/amurg/pkg/protocol"
	"github.com/amurg-ai/amurg/runtime/internal/config"
)

const (
	// pingInterval is how often WebSocket ping frames are sent.
	pingInterval = 30 * time.Second
	// pongWait is the maximum time to wait for a pong before considering the connection dead.
	pongWait = 60 * time.Second
)

// MessageHandler processes messages received from the hub.
type MessageHandler func(env protocol.Envelope) error

// Client manages the WebSocket connection from runtime to hub.
type Client struct {
	cfg     config.HubConfig
	rtID    string
	orgID   string // optional, defaults to "default" on hub side
	endpoints []protocol.EndpointRegistration
	handler MessageHandler
	logger  *slog.Logger

	mu           sync.Mutex
	conn         *websocket.Conn
	done         chan struct{}
	currentToken string // latest token (updated via refresh)
}

// NewClient creates a hub client.
func NewClient(cfg config.HubConfig, runtimeID, orgID string, endpoints []protocol.EndpointRegistration, handler MessageHandler, logger *slog.Logger) *Client {
	return &Client{
		cfg:          cfg,
		rtID:         runtimeID,
		orgID:        orgID,
		endpoints:    endpoints,
		handler:      handler,
		logger:       logger.With("component", "hub-client"),
		done:         make(chan struct{}),
		currentToken: cfg.Token,
	}
}

// Connect establishes the WebSocket connection to the hub and begins processing messages.
// It blocks until the context is canceled. Reconnects with exponential backoff on failure.
func (c *Client) Connect(ctx context.Context) error {
	delay := c.cfg.ReconnectInterval.Duration
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := c.connectOnce(ctx); err != nil {
			c.logger.Warn("connection failed", "error", err)

			// Exponential backoff: double delay on each failure, cap at max.
			c.logger.Info("reconnecting", "delay", delay)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
			delay = delay * 2
			if delay > c.cfg.MaxReconnectDelay.Duration {
				delay = c.cfg.MaxReconnectDelay.Duration
			}
			continue
		}

		// Successful connection ended (e.g. server closed cleanly); reset backoff.
		delay = c.cfg.ReconnectInterval.Duration
	}
}

func (c *Client) connectOnce(ctx context.Context) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	if c.cfg.TLSSkipVerify {
		c.logger.Warn("TLS certificate verification disabled — DO NOT use in production")
		dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.cfg.Token)

	conn, _, err := dialer.DialContext(ctx, c.cfg.URL, header)
	if err != nil {
		return fmt.Errorf("dial hub: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
		_ = conn.Close()
	}()

	// Set up WebSocket-level keepalive.
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		c.logger.Debug("pong received")
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	// Start ping goroutine — sends WebSocket ping frames to keep connection alive
	// through proxies (e.g. Cloudflare's ~100s idle timeout).
	pingDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.mu.Lock()
				err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
				c.mu.Unlock()
				if err != nil {
					c.logger.Debug("ping write failed", "error", err)
					return
				}
				c.logger.Debug("ping sent")
			case <-pingDone:
				return
			}
		}
	}()
	defer close(pingDone)

	// Send hello with latest token.
	c.mu.Lock()
	token := c.currentToken
	c.mu.Unlock()

	hello := protocol.RuntimeHello{
		RuntimeID: c.rtID,
		Token:     token,
		OrgID:     c.orgID,
		Endpoints: c.endpoints,
	}

	if err := c.sendMessage(protocol.TypeRuntimeHello, "", hello); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}

	c.logger.Info("connected to hub", "url", c.cfg.URL)

	// Read messages until disconnected.
	for {
		select {
		case <-ctx.Done():
			_ = conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shutdown"))
			return ctx.Err()
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read message: %w", err)
		}

		// Any message resets the read deadline.
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))

		var env protocol.Envelope
		if err := json.Unmarshal(msg, &env); err != nil {
			c.logger.Warn("invalid message from hub", "error", err)
			continue
		}

		// Handle token refresh internally.
		if env.Type == protocol.TypeRuntimeTokenRefresh {
			data, _ := json.Marshal(env.Payload)
			var refresh protocol.RuntimeTokenRefresh
			if err := json.Unmarshal(data, &refresh); err == nil && refresh.Token != "" {
				c.mu.Lock()
				c.currentToken = refresh.Token
				c.mu.Unlock()
				c.logger.Info("runtime token refreshed")
			}
			continue
		}

		if err := c.handler(env); err != nil {
			c.logger.Warn("handler error", "type", env.Type, "error", err)
		}
	}
}

// Send sends a protocol envelope to the hub.
func (c *Client) Send(msgType, sessionID string, payload any) error {
	return c.sendMessage(msgType, sessionID, payload)
}

func (c *Client) sendMessage(msgType, sessionID string, payload any) error {
	env := protocol.Envelope{
		Type:      msgType,
		SessionID: sessionID,
		Timestamp: time.Now(),
		Payload:   payload,
	}

	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return fmt.Errorf("not connected")
	}

	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// Close gracefully closes the connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
