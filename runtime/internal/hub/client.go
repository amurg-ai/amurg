// Package hub manages the runtime's outbound WebSocket connection to the hub.
package hub

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
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
	// defaultSendBufferSize is the default number of messages buffered during disconnect.
	defaultSendBufferSize = 256
)

// bufferedMessage types that should be saved during disconnects.
var bufferableTypes = map[string]bool{
	protocol.TypeAgentOutput:       true,
	protocol.TypeTurnStarted:       true,
	protocol.TypeTurnCompleted:     true,
	protocol.TypePermissionRequest: true,
	protocol.TypeFileAvailable:     true,
	protocol.TypeSessionCreated:    true,
}

// MessageHandler processes messages received from the hub.
type MessageHandler func(env protocol.Envelope) error

// StateChangeFunc is called when the hub connection state changes.
type StateChangeFunc func(connected bool, reconnecting bool)

// Client manages the WebSocket connection from runtime to hub.
type Client struct {
	cfg     config.HubConfig
	rtID    string
	orgID   string // optional, defaults to "default" on hub side
	agents  []protocol.AgentRegistration
	handler MessageHandler
	logger  *slog.Logger

	mu            sync.Mutex
	conn          *websocket.Conn
	done          chan struct{}
	currentToken  string // latest token (updated via refresh)
	onStateChange StateChangeFunc

	// Send buffer: ring buffer for messages generated while disconnected.
	sendBuf     [][]byte
	sendBufHead int // next write position
	sendBufLen  int // number of valid entries
	sendBufCap  int
}

// NewClient creates a hub client.
func NewClient(cfg config.HubConfig, runtimeID, orgID string, agents []protocol.AgentRegistration, handler MessageHandler, logger *slog.Logger) *Client {
	bufCap := cfg.SendBufferSize
	if bufCap <= 0 {
		bufCap = defaultSendBufferSize
	}

	return &Client{
		cfg:          cfg,
		rtID:         runtimeID,
		orgID:        orgID,
		agents:       agents,
		handler:      handler,
		logger:       logger.With("component", "hub-client"),
		done:         make(chan struct{}),
		currentToken: cfg.Token,
		sendBuf:      make([][]byte, bufCap),
		sendBufCap:   bufCap,
	}
}

// SetStateChangeHandler sets a callback for connection state changes.
func (c *Client) SetStateChangeHandler(fn StateChangeFunc) {
	c.mu.Lock()
	c.onStateChange = fn
	c.mu.Unlock()
}

func (c *Client) notifyStateChange(connected, reconnecting bool) {
	c.mu.Lock()
	fn := c.onStateChange
	c.mu.Unlock()
	if fn != nil {
		fn(connected, reconnecting)
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

		err := c.connectOnce(ctx)
		if err != nil {
			c.logger.Warn("connection failed", "error", err)
			c.notifyStateChange(false, true)

			// Exponential backoff with ±25% jitter, capped at max.
			jitter := time.Duration(float64(delay) * (0.75 + rand.Float64()*0.5))
			c.logger.Info("reconnecting", "delay", jitter)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(jitter):
			}
			delay = min(delay*2, c.cfg.MaxReconnectDelay.Duration)
			continue
		}

		// Connection ended without error (clean disconnect); reset backoff.
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
	c.mu.Lock()
	dialToken := c.currentToken
	c.mu.Unlock()
	header.Set("Authorization", "Bearer "+dialToken)

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
		c.notifyStateChange(false, false)
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
		Agents:    c.agents,
	}

	if err := c.sendMessage(protocol.TypeRuntimeHello, "", hello); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}

	c.logger.Info("connected to hub", "url", c.cfg.URL)
	c.notifyStateChange(true, false)

	// Drain buffered messages that accumulated while disconnected.
	c.drainSendBuffer()

	// Close the connection when the context is canceled so ReadMessage unblocks.
	go func() {
		<-ctx.Done()
		_ = conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shutdown"))
		_ = conn.Close()
	}()

	// Read messages until disconnected.
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
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

// Send sends a protocol envelope to the hub. If disconnected and the message
// type is bufferable, it is saved to the send buffer for delivery on reconnect.
func (c *Client) Send(msgType, sessionID string, payload any) error {
	err := c.sendMessage(msgType, sessionID, payload)
	if err != nil && bufferableTypes[msgType] {
		c.bufferMessage(msgType, sessionID, payload)
	}
	return err
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

// bufferMessage stores a pre-marshaled message in the ring buffer.
// Called when Send fails for a bufferable message type.
func (c *Client) bufferMessage(msgType, sessionID string, payload any) {
	env := protocol.Envelope{
		Type:      msgType,
		SessionID: sessionID,
		Timestamp: time.Now(),
		Payload:   payload,
	}
	data, err := json.Marshal(env)
	if err != nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.sendBuf[c.sendBufHead] = data
	c.sendBufHead = (c.sendBufHead + 1) % c.sendBufCap
	if c.sendBufLen < c.sendBufCap {
		c.sendBufLen++
	}
	// When full, head overtakes tail — oldest messages are dropped.
}

// drainSendBuffer sends all buffered messages over the live connection.
// Must be called after connection is established and hello is sent.
func (c *Client) drainSendBuffer() {
	c.mu.Lock()
	if c.sendBufLen == 0 {
		c.mu.Unlock()
		return
	}

	// Compute start position (tail of ring buffer).
	start := (c.sendBufHead - c.sendBufLen + c.sendBufCap) % c.sendBufCap
	msgs := make([][]byte, c.sendBufLen)
	for i := range c.sendBufLen {
		idx := (start + i) % c.sendBufCap
		msgs[i] = c.sendBuf[idx]
		c.sendBuf[idx] = nil // free reference
	}
	c.sendBufLen = 0
	c.sendBufHead = 0
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		return
	}

	c.logger.Info("draining send buffer", "count", len(msgs))
	for _, data := range msgs {
		c.mu.Lock()
		err := conn.WriteMessage(websocket.TextMessage, data)
		c.mu.Unlock()
		if err != nil {
			c.logger.Warn("failed to drain buffered message", "error", err)
			return
		}
	}
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
