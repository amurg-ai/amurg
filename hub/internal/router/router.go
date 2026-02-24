// Package router manages WebSocket connections for both runtimes and UI clients,
// and routes messages between them.
package router

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/amurg-ai/amurg/hub/internal/auth"
	"github.com/amurg-ai/amurg/hub/internal/store"
	"github.com/amurg-ai/amurg/pkg/protocol"
)

// makeUpgrader creates a WebSocket upgrader with origin checking.
func makeUpgrader(allowedOrigins []string) websocket.Upgrader {
	allowAll := len(allowedOrigins) == 0 || (len(allowedOrigins) == 1 && allowedOrigins[0] == "*")
	originSet := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		originSet[o] = true
	}

	return websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			if allowAll {
				return true
			}
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true // non-browser clients
			}
			return originSet[origin]
		},
	}
}

// Router manages all WebSocket connections and message routing.
type Router struct {
	store        store.Store
	authProvider auth.Provider
	runtimeAuth  auth.RuntimeAuthProvider
	logger       *slog.Logger
	upgrader     websocket.Upgrader

	turnBased  bool // enforce turn-based messaging
	maxPerUser int  // max active sessions per user (0 = unlimited)

	maxClientMessageSize  int64 // max WebSocket message size from clients
	maxRuntimeMessageSize int64 // max WebSocket message size from runtimes
	maxContentBytes       int64 // max message content size

	permissionTimeout time.Duration
	pendingPerms      map[string]*pendingPermission
	fileStoragePath   string
	maxFileBytes      int64

	mu                    sync.RWMutex
	runtimes              map[string]*runtimeConn  // runtime_id -> conn
	clients               map[string]*clientConn   // conn_id -> conn
	subscribers           map[string]map[string]*clientConn // session_id -> conn_id -> conn
	turnStartTimes        map[string]time.Time // session_id -> turn start time
	clientsByUser         map[string]int
	maxClientConnsPerUser int
}

type pendingPermission struct {
	sessionID string
	requestID string
	runtimeID string
	timer     *time.Timer
}

type runtimeConn struct {
	id        string
	orgID     string
	conn      *websocket.Conn
	mu        sync.Mutex
	endpoints map[string]protocol.EndpointRegistration
}

type clientConn struct {
	id          string
	userID      string
	username    string
	role        string
	orgID       string
	conn        *websocket.Conn
	mu          sync.Mutex
	msgTokens   float64
	msgLastTime time.Time
}

// Options configures the Router.
type Options struct {
	TurnBased             bool
	MaxPerUser            int
	AllowedOrigins        []string // for WebSocket origin check
	MaxClientMsgBytes     int64    // max WebSocket message size from clients (default 64KB)
	MaxRuntimeMsgBytes    int64    // max WebSocket message size from runtimes (default 1MB)
	PermissionTimeout     time.Duration
	FileStoragePath       string // path to store files
	MaxFileBytes          int64  // max file size in bytes
	MaxClientConnsPerUser int
}

// New creates a new Router.
func New(s store.Store, ap auth.Provider, ra auth.RuntimeAuthProvider, logger *slog.Logger, opts Options) *Router {
	clientLimit := opts.MaxClientMsgBytes
	if clientLimit == 0 {
		clientLimit = 64 * 1024 // 64KB default
	}
	runtimeLimit := opts.MaxRuntimeMsgBytes
	if runtimeLimit == 0 {
		runtimeLimit = 1024 * 1024 // 1MB default
	}
	// Ensure runtime limit can accommodate base64-encoded files.
	if opts.MaxFileBytes > 0 {
		fileLimit := int64(float64(opts.MaxFileBytes)*1.4) + 4096
		if fileLimit > runtimeLimit {
			runtimeLimit = fileLimit
		}
	}
	permTimeout := opts.PermissionTimeout
	if permTimeout == 0 {
		permTimeout = 60 * time.Second
	}

	maxConnsPerUser := opts.MaxClientConnsPerUser
	if maxConnsPerUser == 0 {
		maxConnsPerUser = 10
	}

	return &Router{
		store:                 s,
		authProvider:          ap,
		runtimeAuth:           ra,
		logger:                logger.With("component", "router"),
		upgrader:              makeUpgrader(opts.AllowedOrigins),
		turnBased:             opts.TurnBased,
		maxPerUser:            opts.MaxPerUser,
		maxClientMessageSize:  clientLimit,
		maxRuntimeMessageSize: runtimeLimit,
		maxContentBytes:       clientLimit,
		permissionTimeout:     permTimeout,
		pendingPerms:          make(map[string]*pendingPermission),
		fileStoragePath:       opts.FileStoragePath,
		maxFileBytes:          opts.MaxFileBytes,
		runtimes:              make(map[string]*runtimeConn),
		clients:               make(map[string]*clientConn),
		subscribers:           make(map[string]map[string]*clientConn),
		turnStartTimes:        make(map[string]time.Time),
		clientsByUser:         make(map[string]int),
		maxClientConnsPerUser: maxConnsPerUser,
	}
}

// HandleRuntimeWS handles WebSocket connections from runtimes.
func (r *Router) HandleRuntimeWS(w http.ResponseWriter, req *http.Request) {
	conn, err := r.upgrader.Upgrade(w, req, nil)
	if err != nil {
		r.logger.Warn("runtime websocket upgrade failed", "error", err)
		return
	}
	defer func() { _ = conn.Close() }()

	// Set read limit for runtime connections.
	conn.SetReadLimit(r.maxRuntimeMessageSize)

	// Read the hello message.
	_, msg, err := conn.ReadMessage()
	if err != nil {
		r.logger.Warn("runtime hello read failed", "error", err)
		return
	}

	var env protocol.Envelope
	if err := json.Unmarshal(msg, &env); err != nil {
		r.logger.Warn("runtime hello parse failed", "error", err)
		return
	}

	if env.Type != protocol.TypeRuntimeHello {
		r.logger.Warn("expected runtime.hello, got", "type", env.Type)
		return
	}

	data, _ := json.Marshal(env.Payload)
	var hello protocol.RuntimeHello
	if err := json.Unmarshal(data, &hello); err != nil {
		r.logger.Warn("runtime hello unmarshal failed", "error", err)
		return
	}

	// Validate runtime token: try time-limited HMAC first, then static.
	tokenValid := false
	if r.runtimeAuth != nil && r.runtimeAuth.RuntimeTokenSecret() != "" {
		runtimeID, err := r.runtimeAuth.ValidateTimeLimitedToken(hello.Token)
		if err == nil && runtimeID == hello.RuntimeID {
			tokenValid = true
		}
	}
	if !tokenValid {
		// Fall back to static token validation.
		if r.runtimeAuth == nil || !r.runtimeAuth.ValidateRuntimeToken(hello.RuntimeID, hello.Token) {
			r.sendToConn(conn, protocol.TypeHelloAck, "", protocol.HelloAck{
				OK:    false,
				Error: "invalid runtime credentials",
			})
			return
		}
	}

	// Determine org_id: use hello.OrgID if set, default to "default".
	orgID := hello.OrgID
	if orgID == "" {
		orgID = "default"
	}

	// Register runtime.
	rtConn := &runtimeConn{
		id:        hello.RuntimeID,
		orgID:     orgID,
		conn:      conn,
		endpoints: make(map[string]protocol.EndpointRegistration),
	}
	for _, ep := range hello.Endpoints {
		rtConn.endpoints[ep.ID] = ep
	}

	r.mu.Lock()
	if existing, ok := r.runtimes[hello.RuntimeID]; ok {
		r.logger.Warn("runtime reconnect: closing previous connection", "runtime_id", hello.RuntimeID)
		_ = existing.conn.Close()
	}
	r.runtimes[hello.RuntimeID] = rtConn
	r.mu.Unlock()

	// Update store.
	ctx := context.Background()
	if err := r.store.UpsertRuntime(ctx, &store.Runtime{
		ID:       hello.RuntimeID,
		OrgID:    orgID,
		Name:     hello.RuntimeID,
		Online:   true,
		LastSeen: time.Now(),
	}); err != nil {
		r.logger.Warn("failed to upsert runtime", "runtime_id", hello.RuntimeID, "error", err)
	}

	// Register endpoints in store.
	if err := r.store.DeleteEndpointsByRuntime(ctx, hello.RuntimeID); err != nil {
		r.logger.Warn("failed to delete endpoints by runtime", "runtime_id", hello.RuntimeID, "error", err)
	}
	for _, ep := range hello.Endpoints {
		capsJSON, _ := json.Marshal(ep.Caps)
		tagsJSON, _ := json.Marshal(ep.Tags)
		secJSON := "{}"
		if ep.Security != nil {
			if b, err := json.Marshal(ep.Security); err == nil {
				secJSON = string(b)
			}
		}
		if err := r.store.UpsertEndpoint(ctx, &store.Endpoint{
			ID:        ep.ID,
			OrgID:     orgID,
			RuntimeID: hello.RuntimeID,
			Profile:   ep.Profile,
			Name:      ep.Name,
			Tags:      string(tagsJSON),
			Caps:      string(capsJSON),
			Security:  secJSON,
		}); err != nil {
			r.logger.Warn("failed to upsert endpoint", "endpoint_id", ep.ID, "error", err)
		}
	}

	// Send ack.
	r.sendToConn(conn, protocol.TypeHelloAck, "", protocol.HelloAck{OK: true})

	// Push any stored config overrides to the runtime on reconnect.
	for _, ep := range hello.Endpoints {
		override, err := r.store.GetEndpointConfigOverride(ctx, ep.ID)
		if err != nil {
			r.logger.Warn("failed to load config override on reconnect", "endpoint_id", ep.ID, "error", err)
			continue
		}
		if override != nil {
			var sec *protocol.SecurityProfile
			if override.Security != "" && override.Security != "{}" {
				sec = &protocol.SecurityProfile{}
				if err := json.Unmarshal([]byte(override.Security), sec); err != nil {
					r.logger.Warn("failed to unmarshal security override", "endpoint_id", ep.ID, "error", err)
				}
			}
			var lim *protocol.EndpointLimits
			if override.Limits != "" && override.Limits != "{}" {
				lim = &protocol.EndpointLimits{}
				if err := json.Unmarshal([]byte(override.Limits), lim); err != nil {
					r.logger.Warn("failed to unmarshal limits override", "endpoint_id", ep.ID, "error", err)
				}
			}
			r.sendToConn(conn, protocol.TypeEndpointConfigUpdate, "", protocol.EndpointConfigUpdate{
				EndpointID: ep.ID,
				Security:   sec,
				Limits:     lim,
			})
			r.logger.Info("pushed config override on reconnect", "endpoint_id", ep.ID, "runtime_id", hello.RuntimeID)
		}
	}

	r.logger.Info("runtime connected", "runtime_id", hello.RuntimeID, "endpoints", len(hello.Endpoints))

	if err := r.store.LogAuditEvent(ctx, &store.AuditEvent{
		ID: uuid.New().String(), OrgID: orgID, Action: "runtime.connect", RuntimeID: hello.RuntimeID, CreatedAt: time.Now(),
	}); err != nil {
		r.logger.Warn("failed to log audit event", "action", "runtime.connect", "error", err)
	}

	// Schedule token refresh if using time-limited tokens.
	var refreshCancel context.CancelFunc
	if r.runtimeAuth != nil && r.runtimeAuth.RuntimeTokenSecret() != "" {
		var refreshCtx context.Context
		refreshCtx, refreshCancel = context.WithCancel(ctx)
		go r.scheduleTokenRefresh(refreshCtx, hello.RuntimeID, rtConn)
	}

	// Read messages from runtime.
	defer func() {
		if refreshCancel != nil {
			refreshCancel()
		}
		r.mu.Lock()
		delete(r.runtimes, hello.RuntimeID)
		r.mu.Unlock()
		r.store.SetRuntimeOnline(ctx, hello.RuntimeID, false)
		r.store.LogAuditEvent(ctx, &store.AuditEvent{
			ID: uuid.New().String(), OrgID: orgID, Action: "runtime.disconnect", RuntimeID: hello.RuntimeID, CreatedAt: time.Now(),
		})
		r.logger.Info("runtime disconnected", "runtime_id", hello.RuntimeID)
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			r.logger.Debug("runtime read error", "runtime_id", hello.RuntimeID, "error", err)
			return
		}

		var env protocol.Envelope
		if err := json.Unmarshal(msg, &env); err != nil {
			r.logger.Warn("invalid message from runtime", "runtime_id", hello.RuntimeID, "error", err)
			continue
		}

		r.handleRuntimeMessage(hello.RuntimeID, env)
	}
}

// HandleClientWS handles WebSocket connections from UI clients.
func (r *Router) HandleClientWS(w http.ResponseWriter, req *http.Request) {
	// Extract JWT from query param or Authorization header.
	// Security note: JWT in query parameter is required for WebSocket connections since
	// browsers cannot set custom headers during the WebSocket handshake. Ensure server
	// access logs are configured to exclude query parameters to prevent token leakage.
	tokenStr := req.URL.Query().Get("token")
	if tokenStr == "" {
		tokenStr = req.Header.Get("Authorization")
		if len(tokenStr) > 7 && tokenStr[:7] == "Bearer " {
			tokenStr = tokenStr[7:]
		}
	}

	identity, err := r.authProvider.ValidateToken(req.Context(), tokenStr)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := r.upgrader.Upgrade(w, req, nil)
	if err != nil {
		r.logger.Warn("client websocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	connID := uuid.New().String()
	cc := &clientConn{
		id:       connID,
		userID:   identity.UserID,
		username: identity.Username,
		role:     identity.Role,
		orgID:    identity.OrgID,
		conn:     conn,
	}

	r.mu.Lock()
	if r.clientsByUser[identity.UserID] >= r.maxClientConnsPerUser {
		r.mu.Unlock()
		r.logger.Warn("too many WebSocket connections for user", "user", identity.Username, "limit", r.maxClientConnsPerUser)
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "too many connections"))
		return
	}
	r.clientsByUser[identity.UserID]++
	r.clients[connID] = cc
	r.mu.Unlock()

	// Set read limit for client connections.
	conn.SetReadLimit(r.maxClientMessageSize)

	r.logger.Info("client connected", "user", identity.Username, "conn_id", connID)

	defer func() {
		r.mu.Lock()
		delete(r.clients, connID)
		r.clientsByUser[cc.userID]--
		if r.clientsByUser[cc.userID] <= 0 {
			delete(r.clientsByUser, cc.userID)
		}
		// Remove from all subscriptions.
		for sessID, subs := range r.subscribers {
			delete(subs, connID)
			if len(subs) == 0 {
				delete(r.subscribers, sessID)
			}
		}
		r.mu.Unlock()
		r.logger.Info("client disconnected", "user", identity.Username, "conn_id", connID)
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			r.logger.Debug("client read error", "conn_id", connID, "error", err)
			return
		}

		if !cc.allowMessage() {
			r.logger.Debug("client message rate limited", "conn_id", connID)
			continue
		}

		var env protocol.Envelope
		if err := json.Unmarshal(msg, &env); err != nil {
			r.logger.Warn("invalid message from client", "conn_id", connID, "error", err)
			continue
		}

		r.handleClientMessage(cc, env)
	}
}

func (r *Router) handleRuntimeMessage(runtimeID string, env protocol.Envelope) {
	switch env.Type {
	case protocol.TypeSessionCreated:
		data, _ := json.Marshal(env.Payload)
		var resp protocol.SessionCreated
		json.Unmarshal(data, &resp)

		if resp.OK {
			ctx := context.Background()
			if resp.NativeHandle != "" {
				r.store.SetSessionNativeHandle(ctx, resp.SessionID, resp.NativeHandle)
			}
		}

	case protocol.TypeAgentOutput:
		data, _ := json.Marshal(env.Payload)
		var output protocol.AgentOutput
		json.Unmarshal(data, &output)

		// Verify session ownership.
		ctx := context.Background()
		sess, err := r.store.GetSession(ctx, output.SessionID)
		if err != nil || sess == nil || sess.RuntimeID != runtimeID {
			r.logger.Warn("agent.output from wrong runtime", "session_id", output.SessionID, "runtime_id", runtimeID)
			return
		}

		// Check message content size.
		if int64(len(output.Content)) > r.maxRuntimeMessageSize {
			r.logger.Warn("agent output exceeds maximum size", "session_id", output.SessionID)
			return
		}

		// Persist message with atomic seq assignment.
		seq, err := r.store.AppendMessage(ctx, &store.Message{
			ID:        uuid.New().String(),
			SessionID: output.SessionID,
			Seq:       0, // assigned atomically by store
			Direction: "agent",
			Channel:   output.Channel,
			Content:   output.Content,
			CreatedAt: time.Now(),
		})
		if err != nil {
			r.logger.Warn("failed to persist agent output", "error", err)
			return
		}
		output.Seq = seq

		// Forward to subscribed clients.
		r.broadcastToSession(output.SessionID, protocol.TypeAgentOutput, output)

	case protocol.TypeTurnStarted:
		data, _ := json.Marshal(env.Payload)
		var ts protocol.TurnStarted
		json.Unmarshal(data, &ts)
		r.broadcastToSession(ts.SessionID, protocol.TypeTurnStarted, ts)

		ctx := context.Background()
		r.store.UpdateSessionState(ctx, ts.SessionID, "responding")

		r.mu.Lock()
		r.turnStartTimes[ts.SessionID] = time.Now()
		r.mu.Unlock()

	case protocol.TypeTurnCompleted:
		data, _ := json.Marshal(env.Payload)
		var tc protocol.TurnCompleted
		json.Unmarshal(data, &tc)
		r.broadcastToSession(tc.SessionID, protocol.TypeTurnCompleted, tc)

		ctx := context.Background()
		r.store.UpdateSessionState(ctx, tc.SessionID, "active")

		r.mu.Lock()
		startTime, hasTiming := r.turnStartTimes[tc.SessionID]
		if hasTiming {
			delete(r.turnStartTimes, tc.SessionID)
		}
		r.mu.Unlock()

		sess, _ := r.store.GetSession(ctx, tc.SessionID)
		endpointID := ""
		orgID := ""
		if sess != nil {
			endpointID = sess.EndpointID
			orgID = sess.OrgID
		}

		var detailJSON json.RawMessage
		if hasTiming {
			durationMs := time.Since(startTime).Milliseconds()
			if tc.ExitCode != nil {
				detailJSON = json.RawMessage(fmt.Sprintf(`{"duration_ms":%d,"exit_code":%d}`, durationMs, *tc.ExitCode))
			} else {
				detailJSON = json.RawMessage(fmt.Sprintf(`{"duration_ms":%d}`, durationMs))
			}
		}

		r.store.LogAuditEvent(ctx, &store.AuditEvent{
			ID: uuid.New().String(), OrgID: orgID, Action: "turn.completed",
			SessionID: tc.SessionID, EndpointID: endpointID, Detail: detailJSON, CreatedAt: time.Now(),
		})

	case protocol.TypeStopAck:
		data, _ := json.Marshal(env.Payload)
		var ack protocol.StopAck
		json.Unmarshal(data, &ack)
		r.broadcastToSession(ack.SessionID, protocol.TypeStopAck, ack)

	case protocol.TypePermissionRequest:
		data, _ := json.Marshal(env.Payload)
		var req protocol.PermissionRequest
		json.Unmarshal(data, &req)

		// Track pending permission.
		r.mu.Lock()
		pp := &pendingPermission{
			sessionID: req.SessionID,
			requestID: req.RequestID,
			runtimeID: runtimeID,
		}
		pp.timer = time.AfterFunc(r.permissionTimeout, func() {
			r.handlePermissionTimeout(req.RequestID)
		})
		r.pendingPerms[req.RequestID] = pp
		r.mu.Unlock()

		// Audit log.
		ctx := context.Background()
		sess, _ := r.store.GetSession(ctx, req.SessionID)
		permOrgID := ""
		permEndpointID := ""
		if sess != nil {
			permOrgID = sess.OrgID
			permEndpointID = sess.EndpointID
		}
		r.store.LogAuditEvent(ctx, &store.AuditEvent{
			ID: uuid.New().String(), OrgID: permOrgID, Action: "permission.requested",
			SessionID: req.SessionID, EndpointID: permEndpointID,
			Detail:    json.RawMessage(fmt.Sprintf(`{"tool":%q,"resource":%q,"request_id":%q}`, req.Tool, req.Resource, req.RequestID)),
			CreatedAt: time.Now(),
		})

		// Relay to subscribed UI clients.
		r.broadcastToSession(req.SessionID, protocol.TypePermissionRequest, req)

	case protocol.TypeFileAvailable:
		data, _ := json.Marshal(env.Payload)
		var fileMsg protocol.FileAvailable
		json.Unmarshal(data, &fileMsg)

		// Sanitize path components to prevent path traversal.
		safeSessionID := filepath.Base(fileMsg.SessionID)
		safeFileID := filepath.Base(fileMsg.Metadata.FileID)
		safeName := filepath.Base(fileMsg.Metadata.Name)

		if safeSessionID == "." || safeSessionID == ".." || safeFileID == "." || safeFileID == ".." || safeName == "." || safeName == ".." {
			r.logger.Warn("path traversal attempt in file.available", "session_id", fileMsg.SessionID)
			return
		}

		// Verify session ownership.
		sess, err := r.store.GetSession(context.Background(), fileMsg.SessionID)
		if err != nil || sess == nil || sess.RuntimeID != runtimeID {
			r.logger.Warn("file.available from wrong runtime", "session_id", fileMsg.SessionID, "runtime_id", runtimeID)
			return
		}

		// Decode base64 content.
		fileData, err := base64.StdEncoding.DecodeString(fileMsg.Data)
		if err != nil {
			r.logger.Warn("failed to decode file data", "session_id", fileMsg.SessionID, "error", err)
			return
		}

		// Check file size limit.
		if int64(len(fileData)) > r.maxFileBytes {
			r.logger.Warn("file exceeds maximum size", "session_id", fileMsg.SessionID, "size", len(fileData), "max", r.maxFileBytes)
			return
		}

		// Save to hub disk.
		dir := filepath.Join(r.fileStoragePath, safeSessionID, safeFileID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			r.logger.Warn("failed to create file directory", "error", err)
			return
		}
		filePath := filepath.Join(dir, safeName)

		// Verify the resolved path stays within the file storage directory.
		absPath, err := filepath.Abs(filePath)
		if err != nil || !strings.HasPrefix(absPath, filepath.Clean(r.fileStoragePath)+string(os.PathSeparator)) {
			r.logger.Warn("path traversal blocked in file.available", "path", filePath)
			return
		}

		if err := os.WriteFile(filePath, fileData, 0o644); err != nil {
			r.logger.Warn("failed to write file", "error", err)
			return
		}

		// Persist file message.
		ctx := context.Background()
		metaJSON, _ := json.Marshal(map[string]any{
			"file_id":   fileMsg.Metadata.FileID,
			"name":      fileMsg.Metadata.Name,
			"mime_type": fileMsg.Metadata.MimeType,
			"size":      fileMsg.Metadata.Size,
			"direction": "download",
		})
		seq, err := r.store.AppendMessage(ctx, &store.Message{
			ID:        uuid.New().String(),
			SessionID: fileMsg.SessionID,
			Seq:       0,
			Direction: "agent",
			Channel:   "file",
			Content:   string(metaJSON),
			CreatedAt: time.Now(),
		})
		if err != nil {
			r.logger.Warn("failed to persist file message", "error", err)
			return
		}

		// Broadcast to subscribed clients as agent.output with channel="file".
		r.broadcastToSession(fileMsg.SessionID, protocol.TypeAgentOutput, protocol.AgentOutput{
			SessionID: fileMsg.SessionID,
			Seq:       seq,
			Channel:   "file",
			Content:   string(metaJSON),
		})

	case protocol.TypeEndpointConfigAck:
		data, _ := json.Marshal(env.Payload)
		var ack protocol.EndpointConfigAck
		json.Unmarshal(data, &ack)
		if ack.OK {
			r.logger.Info("endpoint config update acknowledged", "endpoint_id", ack.EndpointID, "runtime", runtimeID)
		} else {
			r.logger.Warn("endpoint config update rejected", "endpoint_id", ack.EndpointID, "runtime", runtimeID, "error", ack.Error)
		}

	case protocol.TypePong:
		// Heartbeat response, nothing to do.

	default:
		r.logger.Warn("unknown runtime message type", "type", env.Type, "runtime", runtimeID)
	}
}

func (r *Router) handleClientMessage(cc *clientConn, env protocol.Envelope) {
	switch env.Type {
	case protocol.TypeUserMessage:
		data, _ := json.Marshal(env.Payload)
		var msg protocol.UserMessage
		json.Unmarshal(data, &msg)

		ctx := context.Background()

		// Check idempotency.
		if exists, _ := r.store.MessageExists(ctx, msg.SessionID, msg.MessageID); exists {
			r.logger.Debug("duplicate message, skipping", "message_id", msg.MessageID)
			return
		}

		// Get session to find the runtime.
		sess, err := r.store.GetSession(ctx, msg.SessionID)
		if err != nil || sess == nil {
			r.sendToClient(cc, protocol.TypeErrorResponse, "", protocol.ErrorResponse{
				Code: "session_not_found", Message: "session not found",
			})
			return
		}

		// Verify user owns the session.
		if sess.UserID != cc.userID {
			r.sendToClient(cc, protocol.TypeErrorResponse, "", protocol.ErrorResponse{
				Code: "forbidden", Message: "not your session",
			})
			return
		}

		// Turn gating: reject if session is responding and turn-based mode is on.
		if r.turnBased && sess.State == "responding" {
			r.sendToClient(cc, protocol.TypeErrorResponse, msg.SessionID, protocol.ErrorResponse{
				Code: "turn_in_progress", Message: "wait for the current turn to complete",
			})
			return
		}

		// Check message content size.
		if int64(len(msg.Content)) > r.maxContentBytes {
			r.sendToClient(cc, protocol.TypeErrorResponse, msg.SessionID, protocol.ErrorResponse{
				Code: "message_too_large", Message: "message exceeds maximum size",
			})
			return
		}

		// Persist user message with atomic seq assignment.
		_, err = r.store.AppendMessage(ctx, &store.Message{
			ID:        msg.MessageID,
			SessionID: msg.SessionID,
			Seq:       0, // assigned atomically by store
			Direction: "user",
			Channel:   "stdin",
			Content:   msg.Content,
			CreatedAt: time.Now(),
		})
		if err != nil {
			r.sendToClient(cc, protocol.TypeErrorResponse, msg.SessionID, protocol.ErrorResponse{
				Code: "persist_failed", Message: "failed to persist message",
			})
			return
		}

		r.store.LogAuditEvent(ctx, &store.AuditEvent{
			ID: uuid.New().String(), OrgID: cc.orgID, Action: "message.sent", UserID: cc.userID,
			SessionID: msg.SessionID, EndpointID: sess.EndpointID, CreatedAt: time.Now(),
		})

		// Forward to runtime.
		r.sendToRuntime(sess.RuntimeID, protocol.TypeUserMessage, msg.SessionID, msg)

	case protocol.TypeClientSubscribe:
		data, _ := json.Marshal(env.Payload)
		var sub protocol.ClientSubscribe
		json.Unmarshal(data, &sub)

		// Verify session ownership before subscribing.
		ctx := context.Background()
		sess, err := r.store.GetSession(ctx, sub.SessionID)
		if err != nil || sess == nil {
			r.sendToClient(cc, protocol.TypeErrorResponse, sub.SessionID, protocol.ErrorResponse{
				Code: "session_not_found", Message: "session not found",
			})
			return
		}
		if sess.UserID != cc.userID && cc.role != "admin" {
			r.sendToClient(cc, protocol.TypeErrorResponse, sub.SessionID, protocol.ErrorResponse{
				Code: "forbidden", Message: "not your session",
			})
			return
		}

		r.mu.Lock()
		if r.subscribers[sub.SessionID] == nil {
			r.subscribers[sub.SessionID] = make(map[string]*clientConn)
		}
		r.subscribers[sub.SessionID][cc.id] = cc
		r.mu.Unlock()

		// Send missed messages (reuse ctx from ownership check above).
		messages, _ := r.store.GetMessages(ctx, sub.SessionID, sub.AfterSeq, 1000)
		if len(messages) > 0 {
			stored := make([]protocol.StoredMessage, len(messages))
			for i, m := range messages {
				stored[i] = protocol.StoredMessage{
					ID:        m.ID,
					SessionID: m.SessionID,
					Seq:       m.Seq,
					Direction: m.Direction,
					Channel:   m.Channel,
					Content:   m.Content,
					Timestamp: m.CreatedAt,
				}
			}
			r.sendToClient(cc, protocol.TypeHistoryResponse, sub.SessionID, protocol.HistoryResponse{
				SessionID: sub.SessionID,
				Messages:  stored,
			})
		}

	case protocol.TypeClientUnsubscribe:
		data, _ := json.Marshal(env.Payload)
		var unsub protocol.ClientUnsubscribe
		json.Unmarshal(data, &unsub)

		r.mu.Lock()
		if subs, ok := r.subscribers[unsub.SessionID]; ok {
			delete(subs, cc.id)
			if len(subs) == 0 {
				delete(r.subscribers, unsub.SessionID)
			}
		}
		r.mu.Unlock()

	case protocol.TypeStopRequest:
		data, _ := json.Marshal(env.Payload)
		var req protocol.StopRequest
		json.Unmarshal(data, &req)

		ctx := context.Background()
		sess, _ := r.store.GetSession(ctx, req.SessionID)
		if sess != nil && sess.UserID == cc.userID {
			r.sendToRuntime(sess.RuntimeID, protocol.TypeStopRequest, req.SessionID, req)
			r.store.LogAuditEvent(ctx, &store.AuditEvent{
				ID: uuid.New().String(), OrgID: cc.orgID, Action: "session.stop", UserID: cc.userID,
				SessionID: req.SessionID, EndpointID: sess.EndpointID, CreatedAt: time.Now(),
			})
		}

	case protocol.TypePermissionResponse:
		data, _ := json.Marshal(env.Payload)
		var resp protocol.PermissionResponse
		json.Unmarshal(data, &resp)

		ctx := context.Background()

		// Verify session ownership.
		sess, _ := r.store.GetSession(ctx, resp.SessionID)
		if sess == nil || (sess.UserID != cc.userID && cc.role != "admin") {
			r.sendToClient(cc, protocol.TypeErrorResponse, resp.SessionID, protocol.ErrorResponse{
				Code: "forbidden", Message: "not your session",
			})
			return
		}

		// Look up and clean up pending permission.
		r.mu.Lock()
		pp, ok := r.pendingPerms[resp.RequestID]
		if ok {
			pp.timer.Stop()
			delete(r.pendingPerms, resp.RequestID)
		}
		r.mu.Unlock()

		if !ok {
			return // already timed out or not found
		}

		// Audit log.
		action := "permission.denied"
		if resp.Approved {
			action = "permission.granted"
		}
		r.store.LogAuditEvent(ctx, &store.AuditEvent{
			ID: uuid.New().String(), OrgID: cc.orgID, Action: action,
			UserID: cc.userID, SessionID: resp.SessionID, EndpointID: sess.EndpointID,
			Detail:    json.RawMessage(fmt.Sprintf(`{"request_id":%q,"approved":%t}`, resp.RequestID, resp.Approved)),
			CreatedAt: time.Now(),
		})

		// Relay to runtime.
		r.sendToRuntime(pp.runtimeID, protocol.TypePermissionResponse, resp.SessionID, resp)

	default:
		r.logger.Warn("unknown client message type", "type", env.Type, "user", cc.username)
	}
}

func (cc *clientConn) allowMessage() bool {
	const rate = 30.0  // messages per second
	const burst = 50.0 // max burst

	now := time.Now()
	cc.mu.Lock()
	defer cc.mu.Unlock()

	if cc.msgLastTime.IsZero() {
		cc.msgTokens = burst
		cc.msgLastTime = now
	}

	elapsed := now.Sub(cc.msgLastTime).Seconds()
	cc.msgTokens += elapsed * rate
	if cc.msgTokens > burst {
		cc.msgTokens = burst
	}
	cc.msgLastTime = now

	if cc.msgTokens < 1 {
		return false
	}
	cc.msgTokens--
	return true
}

// CreateSession creates a new session and sends the create request to the runtime.
func (r *Router) CreateSession(ctx context.Context, userID, endpointID string) (*store.Session, error) {
	ep, err := r.store.GetEndpoint(ctx, endpointID)
	if err != nil || ep == nil {
		return nil, err
	}

	// Enforce max sessions per user.
	if r.maxPerUser > 0 {
		count, err := r.store.CountActiveSessionsByUser(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("count sessions: %w", err)
		}
		if count >= r.maxPerUser {
			return nil, fmt.Errorf("max sessions per user reached (%d)", r.maxPerUser)
		}
	}

	sess := &store.Session{
		ID:         uuid.New().String(),
		OrgID:      ep.OrgID,
		UserID:     userID,
		EndpointID: endpointID,
		RuntimeID:  ep.RuntimeID,
		Profile:    ep.Profile,
		State:      "creating",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	if err := r.store.CreateSession(ctx, sess); err != nil {
		return nil, err
	}

	// Send create request to runtime.
	r.sendToRuntime(ep.RuntimeID, protocol.TypeSessionCreate, sess.ID, protocol.SessionCreate{
		SessionID:  sess.ID,
		EndpointID: endpointID,
		UserID:     userID,
	})

	r.store.LogAuditEvent(ctx, &store.AuditEvent{
		ID: uuid.New().String(), OrgID: ep.OrgID, Action: "session.create", UserID: userID,
		RuntimeID: ep.RuntimeID, SessionID: sess.ID, EndpointID: endpointID, CreatedAt: time.Now(),
	})

	return sess, nil
}

// broadcastToSession sends a message to all clients subscribed to a session.
func (r *Router) broadcastToSession(sessionID, msgType string, payload any) {
	r.mu.RLock()
	subs := r.subscribers[sessionID]
	clients := make([]*clientConn, 0, len(subs))
	for _, cc := range subs {
		clients = append(clients, cc)
	}
	r.mu.RUnlock()

	for _, cc := range clients {
		r.sendToClient(cc, msgType, sessionID, payload)
	}
}

func (r *Router) sendToRuntime(runtimeID, msgType, sessionID string, payload any) {
	r.mu.RLock()
	rt, ok := r.runtimes[runtimeID]
	r.mu.RUnlock()

	if !ok {
		r.logger.Warn("runtime not connected", "runtime_id", runtimeID)
		return
	}

	env := protocol.Envelope{
		Type:      msgType,
		SessionID: sessionID,
		Timestamp: time.Now(),
		Payload:   payload,
	}

	data, err := json.Marshal(env)
	if err != nil {
		r.logger.Warn("marshal error", "error", err)
		return
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()
	if err := rt.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		r.logger.Warn("send to runtime failed", "runtime_id", runtimeID, "error", err)
	}
}

func (r *Router) sendToClient(cc *clientConn, msgType, sessionID string, payload any) {
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

	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.conn.WriteMessage(websocket.TextMessage, data)
}

// StartIdleReaper starts a background goroutine that closes sessions idle longer than timeout.
// profileTimeouts provides per-profile overrides; a zero duration disables idle close for that profile.
func (r *Router) StartIdleReaper(ctx context.Context, defaultTimeout time.Duration, profileTimeouts map[string]time.Duration) {
	if defaultTimeout <= 0 && len(profileTimeouts) == 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Pass empty orgID to list active sessions across all orgs.
				sessions, err := r.store.ListActiveSessions(ctx, "")
				if err != nil {
					r.logger.Warn("idle reaper: list sessions failed", "error", err)
					continue
				}
				now := time.Now()
				for _, sess := range sessions {
					timeout := defaultTimeout
					if pt, ok := profileTimeouts[sess.Profile]; ok {
						timeout = pt
					}
					if timeout <= 0 {
						continue // disabled for this profile (or no default)
					}
					cutoff := now.Add(-timeout)
					if sess.UpdatedAt.Before(cutoff) {
						r.store.UpdateSessionState(ctx, sess.ID, "closed")
						r.store.LogAuditEvent(ctx, &store.AuditEvent{
							ID: uuid.New().String(), Action: "session.idle_close",
							OrgID: sess.OrgID, SessionID: sess.ID, UserID: sess.UserID,
							EndpointID: sess.EndpointID, CreatedAt: time.Now(),
						})
						r.broadcastToSession(sess.ID, protocol.TypeSessionClosed, map[string]string{
							"session_id": sess.ID,
						})
						r.logger.Info("idle reaper: closed session", "session_id", sess.ID, "profile", sess.Profile, "timeout", timeout)
					}
				}
			}
		}
	}()
}

// scheduleTokenRefresh periodically sends a new token to the runtime at 80% of the token lifetime.
func (r *Router) scheduleTokenRefresh(ctx context.Context, runtimeID string, rt *runtimeConn) {
	lifetime := r.runtimeAuth.RuntimeTokenLifetime()
	if lifetime <= 0 {
		return
	}
	refreshInterval := time.Duration(float64(lifetime) * 0.8)

	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			newToken := r.runtimeAuth.GenerateRuntimeToken(runtimeID)
			env := protocol.Envelope{
				Type:      protocol.TypeRuntimeTokenRefresh,
				Timestamp: time.Now(),
				Payload:   protocol.RuntimeTokenRefresh{Token: newToken},
			}
			data, err := json.Marshal(env)
			if err != nil {
				r.logger.Warn("failed to marshal token refresh", "error", err)
				continue
			}
			rt.mu.Lock()
			err = rt.conn.WriteMessage(websocket.TextMessage, data)
			rt.mu.Unlock()
			if err != nil {
				r.logger.Warn("failed to send token refresh", "runtime_id", runtimeID, "error", err)
				return
			}
			r.logger.Debug("token refresh sent", "runtime_id", runtimeID)
		}
	}
}

func (r *Router) handlePermissionTimeout(requestID string) {
	r.mu.Lock()
	pp, ok := r.pendingPerms[requestID]
	if !ok {
		r.mu.Unlock()
		return
	}
	delete(r.pendingPerms, requestID)
	r.mu.Unlock()

	ctx := context.Background()
	sess, _ := r.store.GetSession(ctx, pp.sessionID)
	orgID := ""
	endpointID := ""
	if sess != nil {
		orgID = sess.OrgID
		endpointID = sess.EndpointID
	}

	r.store.LogAuditEvent(ctx, &store.AuditEvent{
		ID: uuid.New().String(), OrgID: orgID, Action: "permission.timeout",
		SessionID: pp.sessionID, EndpointID: endpointID,
		Detail:    json.RawMessage(fmt.Sprintf(`{"request_id":%q}`, requestID)),
		CreatedAt: time.Now(),
	})

	// Send denied response to runtime.
	denied := protocol.PermissionResponse{
		SessionID: pp.sessionID,
		RequestID: requestID,
		Approved:  false,
	}
	r.sendToRuntime(pp.runtimeID, protocol.TypePermissionResponse, pp.sessionID, denied)

	// Broadcast to UI clients too.
	r.broadcastToSession(pp.sessionID, protocol.TypePermissionResponse, denied)
}

// SendFileToRuntime sends a file upload message to the runtime handling a session.
func (r *Router) SendFileToRuntime(runtimeID, sessionID string, upload protocol.FileUpload) {
	r.sendToRuntime(runtimeID, protocol.TypeFileUpload, sessionID, upload)
}

// BroadcastFileMessage broadcasts a file message to all subscribers of a session.
func (r *Router) BroadcastFileMessage(sessionID string, seq int64, content string) {
	r.broadcastToSession(sessionID, protocol.TypeAgentOutput, protocol.AgentOutput{
		SessionID: sessionID,
		Seq:       seq,
		Channel:   "file",
		Content:   content,
	})
}

// BroadcastSessionClosed notifies all subscribers that a session has been closed.
func (r *Router) BroadcastSessionClosed(sessionID string) {
	r.broadcastToSession(sessionID, protocol.TypeSessionClosed, map[string]string{
		"session_id": sessionID,
	})
}

// PushEndpointConfigUpdate sends a config update to the runtime that owns the endpoint.
// Returns true if the runtime was online and the message was sent.
func (r *Router) PushEndpointConfigUpdate(endpointID string, security *protocol.SecurityProfile, limits *protocol.EndpointLimits) bool {
	r.mu.RLock()
	var target *runtimeConn
	for _, rt := range r.runtimes {
		if _, ok := rt.endpoints[endpointID]; ok {
			target = rt
			break
		}
	}
	r.mu.RUnlock()

	if target == nil {
		return false
	}

	env := protocol.Envelope{
		Type:      protocol.TypeEndpointConfigUpdate,
		Timestamp: time.Now(),
		Payload: protocol.EndpointConfigUpdate{
			EndpointID: endpointID,
			Security:   security,
			Limits:     limits,
		},
	}

	data, err := json.Marshal(env)
	if err != nil {
		r.logger.Warn("marshal config update failed", "error", err)
		return false
	}

	target.mu.Lock()
	defer target.mu.Unlock()
	if err := target.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		r.logger.Warn("send config update failed", "endpoint_id", endpointID, "error", err)
		return false
	}
	return true
}

func (r *Router) sendToConn(conn *websocket.Conn, msgType, sessionID string, payload any) {
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

	conn.WriteMessage(websocket.TextMessage, data)
}
