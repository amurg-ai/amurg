// Package api provides the HTTP API and middleware for the hub.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/amurg-ai/amurg/hub/internal/auth"
	"github.com/amurg-ai/amurg/hub/internal/config"
	"github.com/amurg-ai/amurg/hub/internal/router"
	"github.com/amurg-ai/amurg/hub/internal/store"
	"github.com/amurg-ai/amurg/pkg/protocol"
)

// Server is the HTTP API server.
type Server struct {
	store                 store.Store
	authProvider          auth.Provider
	loginProvider         auth.LoginProvider
	runtimeAuth           auth.RuntimeAuthProvider
	router                *router.Router
	logger                *slog.Logger
	mux                   *chi.Mux
	defaultEndpointAccess string // "all" or "none"
	startTime             time.Time
	maxBodyBytes          int64
	fileStoragePath       string // path for uploaded files
	maxFileBytes          int64  // max file upload size
	whisperURL            string // upstream Whisper WebSocket URL for /asr proxy
	loginRL               *rateLimiter
	rl                    *rateLimiter
}

// NewServer creates a new API server.
func NewServer(s store.Store, ap auth.Provider, lp auth.LoginProvider, ra auth.RuntimeAuthProvider, rt *router.Router, cfg *config.Config, logger *slog.Logger) *Server {
	srv := &Server{
		store:                 s,
		authProvider:          ap,
		loginProvider:         lp,
		runtimeAuth:           ra,
		router:                rt,
		logger:                logger.With("component", "api"),
		defaultEndpointAccess: cfg.Auth.DefaultEndpointAccess,
		startTime:             time.Now(),
		maxBodyBytes:          cfg.Server.MaxBodyBytes,
		fileStoragePath:       cfg.Server.FileStoragePath,
		maxFileBytes:          cfg.Server.MaxFileBytes,
		whisperURL:            cfg.Server.WhisperURL,
	}

	mux := chi.NewRouter()
	mux.Use(chimw.Recoverer)
	mux.Use(chimw.RealIP)
	mux.Use(securityHeadersMiddleware)
	mux.Use(makeCORSMiddleware(cfg.Server.AllowedOrigins))

	// Health check routes (unauthenticated)
	mux.Get("/healthz", srv.handleHealthz)
	mux.Get("/readyz", srv.handleReadyz)

	// Auth config endpoint (unauthenticated)
	mux.Get("/api/auth/config", srv.handleAuthConfig)

	// Login route only registered when using builtin auth.
	if lp != nil {
		srv.loginRL = newRateLimiter(5, 10)
		mux.With(loginIPRateLimitMiddleware(srv.loginRL)).Post("/api/auth/login", srv.handleLogin)
	}

	// WebSocket routes (auth handled inside)
	mux.Get("/ws/runtime", rt.HandleRuntimeWS)
	mux.Get("/ws/client", rt.HandleClientWS)

	// Voice config — tells the UI whether Whisper is available.
	mux.Get("/api/voice/config", srv.handleVoiceConfig)

	// Whisper ASR WebSocket proxy (auth via ?token= query param, same as /ws/client).
	if srv.whisperURL != "" {
		mux.Get("/asr", srv.handleASRProxy)
	}

	// Authenticated API routes
	srv.rl = newRateLimiter(cfg.RateLimit.RequestsPerSecond, cfg.RateLimit.Burst)
	mux.Group(func(r chi.Router) {
		r.Use(srv.authMiddleware)
		r.Use(rateLimitMiddleware(srv.rl))

		r.Get("/api/endpoints", srv.handleListEndpoints)
		r.Get("/api/sessions", srv.handleListSessions)
		r.Post("/api/sessions", srv.handleCreateSession)
		r.Get("/api/sessions/{sessionID}/messages", srv.handleGetMessages)
		r.Post("/api/sessions/{sessionID}/files", srv.handleUploadFile)
		r.Get("/api/files/{fileID}", srv.handleDownloadFile)
		r.Post("/api/sessions/{sessionID}/close", srv.handleCloseSession)
		r.Get("/api/me", srv.handleGetMe)

		// Admin routes
		r.Group(func(r chi.Router) {
			r.Use(srv.adminMiddleware)
			r.Get("/api/runtimes", srv.handleListRuntimes)
			r.Get("/api/users", srv.handleListUsers)
			// User management only available with builtin auth.
			if lp != nil {
				r.Post("/api/users", srv.handleCreateUser)
			}
			r.Post("/api/permissions", srv.handleGrantPermission)
			r.Delete("/api/permissions", srv.handleRevokePermission)
			r.Get("/api/users/{userID}/permissions", srv.handleListUserPermissions)
			r.Get("/api/admin/sessions", srv.handleAdminListSessions)
			r.Post("/api/admin/sessions/{sessionID}/close", srv.handleAdminCloseSession)
			r.Get("/api/admin/audit", srv.handleAdminListAuditEvents)
			r.Get("/api/admin/endpoints", srv.handleAdminListEndpoints)
			r.Get("/api/admin/endpoints/{endpointID}/config", srv.handleGetEndpointConfig)
			r.Put("/api/admin/endpoints/{endpointID}/config", srv.handleUpdateEndpointConfig)
		})
	})

	// Serve UI static files if configured.
	uiDir := cfg.Server.UIStaticDir
	if uiDir != "" {
		fileServer := http.FileServer(http.Dir(uiDir))
		mux.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Try serving the file, fall back to index.html for SPA routing.
			path := r.URL.Path
			if path != "/" && !strings.Contains(path, ".") {
				r.URL.Path = "/"
			}
			fileServer.ServeHTTP(w, r)
		}))
	}

	srv.mux = mux
	return srv
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// StartBackgroundTasks starts periodic cleanup tasks for rate limiters.
func (s *Server) StartBackgroundTasks(ctx context.Context) {
	if s.loginRL != nil {
		s.loginRL.StartCleanup(ctx, 5*time.Minute, 10*time.Minute)
	}
	if s.rl != nil {
		s.rl.StartCleanup(ctx, 5*time.Minute, 10*time.Minute)
	}
}

// --- Auth handlers ---

func (s *Server) handleAuthConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"provider": "builtin"})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Username) < 3 || len(req.Username) > 64 {
		writeError(w, http.StatusBadRequest, "username must be 3-64 characters")
		return
	}

	token, err := s.loginProvider.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		if err := s.store.LogAuditEvent(r.Context(), &store.AuditEvent{
			ID: uuid.New().String(), OrgID: "default", Action: "login.failed",
			Detail: json.RawMessage(fmt.Sprintf(`{"username":%q}`, req.Username)), CreatedAt: time.Now(),
		}); err != nil {
			s.logger.Warn("failed to log audit event", "action", "login.failed", "error", err)
		}
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// Look up user for audit event.
	user, _ := s.store.GetUser(r.Context(), "default", req.Username)
	userID := ""
	if user != nil {
		userID = user.ID
	}
	if err := s.store.LogAuditEvent(r.Context(), &store.AuditEvent{
		ID: uuid.New().String(), OrgID: "default", Action: "login.success", UserID: userID, CreatedAt: time.Now(),
	}); err != nil {
		s.logger.Warn("failed to log audit event", "action", "login.success", "error", err)
	}

	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (s *Server) handleGetMe(w http.ResponseWriter, r *http.Request) {
	identity := getIdentityFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{
		"id":       identity.UserID,
		"username": identity.Username,
		"role":     identity.Role,
	})
}

// --- Endpoint handlers ---

func (s *Server) handleListEndpoints(w http.ResponseWriter, r *http.Request) {
	identity := getIdentityFromContext(r.Context())

	endpoints, err := s.store.ListEndpoints(r.Context(), identity.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list endpoints")
		return
	}
	if endpoints == nil {
		endpoints = []store.Endpoint{}
	}

	// Admins see all; regular users filtered by permissions when access mode is "none".
	if identity.Role != "admin" && s.defaultEndpointAccess == "none" {
		permitted, err := s.store.ListUserEndpoints(r.Context(), identity.UserID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to check permissions")
			return
		}
		permSet := make(map[string]bool, len(permitted))
		for _, id := range permitted {
			permSet[id] = true
		}
		filtered := make([]store.Endpoint, 0)
		for _, ep := range endpoints {
			if permSet[ep.ID] {
				filtered = append(filtered, ep)
			}
		}
		endpoints = filtered
	}

	// Enrich with runtime online status.
	runtimes, _ := s.store.ListRuntimes(r.Context(), identity.OrgID)
	onlineSet := make(map[string]bool, len(runtimes))
	for _, rt := range runtimes {
		onlineSet[rt.ID] = rt.Online
	}

	type endpointResponse struct {
		store.Endpoint
		Online bool `json:"online"`
	}
	result := make([]endpointResponse, len(endpoints))
	for i, ep := range endpoints {
		result[i] = endpointResponse{Endpoint: ep, Online: onlineSet[ep.RuntimeID]}
	}

	writeJSON(w, http.StatusOK, result)
}

// --- Session handlers ---

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	identity := getIdentityFromContext(r.Context())
	sessions, err := s.store.ListSessionsByUser(r.Context(), identity.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list sessions")
		return
	}
	if sessions == nil {
		sessions = []store.Session{}
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
	identity := getIdentityFromContext(r.Context())

	var req struct {
		EndpointID string `json:"endpoint_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Check endpoint access when mode is "none".
	if identity.Role != "admin" && s.defaultEndpointAccess == "none" {
		hasAccess, err := s.store.HasEndpointAccess(r.Context(), identity.UserID, req.EndpointID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to check permissions")
			return
		}
		if !hasAccess {
			if err := s.store.LogAuditEvent(r.Context(), &store.AuditEvent{
				ID: uuid.New().String(), OrgID: identity.OrgID, Action: "session.create_denied",
				UserID: identity.UserID, EndpointID: req.EndpointID,
				Detail: json.RawMessage(`{"reason":"no_access"}`), CreatedAt: time.Now(),
			}); err != nil {
				s.logger.Warn("failed to log audit event", "action", "session.create_denied", "error", err)
			}
			writeError(w, http.StatusForbidden, "no access to this endpoint")
			return
		}
	}

	sess, err := s.router.CreateSession(r.Context(), identity.UserID, req.EndpointID)
	if err != nil {
		if strings.Contains(err.Error(), "max sessions") {
			if err := s.store.LogAuditEvent(r.Context(), &store.AuditEvent{
				ID: uuid.New().String(), OrgID: identity.OrgID, Action: "session.create_denied",
				UserID: identity.UserID, EndpointID: req.EndpointID,
				Detail: json.RawMessage(`{"reason":"max_sessions"}`), CreatedAt: time.Now(),
			}); err != nil {
				s.logger.Warn("failed to log audit event", "action", "session.create_denied", "error", err)
			}
			writeError(w, http.StatusTooManyRequests, "maximum sessions per user reached")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	writeJSON(w, http.StatusCreated, sess)
}

func (s *Server) handleGetMessages(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	identity := getIdentityFromContext(r.Context())

	// Verify session ownership.
	sess, err := s.store.GetSession(r.Context(), sessionID)
	if err != nil || sess == nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if sess.UserID != identity.UserID && identity.Role != "admin" {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}

	// Parse pagination params.
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}

	afterSeq := int64(0)
	if v := r.URL.Query().Get("after_seq"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			afterSeq = n
		}
	}

	messages, err := s.store.GetMessages(r.Context(), sessionID, afterSeq, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get messages")
		return
	}
	if messages == nil {
		messages = []store.Message{}
	}
	writeJSON(w, http.StatusOK, messages)
}

func (s *Server) handleCloseSession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	identity := getIdentityFromContext(r.Context())

	sess, err := s.store.GetSession(r.Context(), sessionID)
	if err != nil || sess == nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	// Verify ownership.
	if sess.UserID != identity.UserID {
		writeError(w, http.StatusForbidden, "not your session")
		return
	}
	if sess.State == "closed" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "already_closed"})
		return
	}
	if err := s.store.UpdateSessionState(r.Context(), sessionID, "closed"); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to close session")
		return
	}
	if err := s.store.LogAuditEvent(r.Context(), &store.AuditEvent{
		ID: uuid.New().String(), OrgID: identity.OrgID, Action: "session.close",
		UserID: identity.UserID, SessionID: sessionID, EndpointID: sess.EndpointID, CreatedAt: time.Now(),
	}); err != nil {
		s.logger.Warn("failed to log audit event", "action", "session.close", "error", err)
	}

	// Broadcast session.closed to subscribers.
	s.router.BroadcastSessionClosed(sessionID)

	writeJSON(w, http.StatusOK, map[string]string{"status": "closed"})
}

// --- Admin handlers ---

func (s *Server) handleListRuntimes(w http.ResponseWriter, r *http.Request) {
	identity := getIdentityFromContext(r.Context())
	runtimes, err := s.store.ListRuntimes(r.Context(), identity.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list runtimes")
		return
	}
	writeJSON(w, http.StatusOK, runtimes)
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	identity := getIdentityFromContext(r.Context())
	users, err := s.store.ListUsers(r.Context(), identity.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Username) < 3 || len(req.Username) > 64 {
		writeError(w, http.StatusBadRequest, "username must be 3-64 characters")
		return
	}
	if len(req.Password) < 8 || len(req.Password) > 128 {
		writeError(w, http.StatusBadRequest, "password must be 8-128 characters")
		return
	}

	user, err := s.loginProvider.Register(r.Context(), req.Username, req.Password, req.Role)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	user.PasswordHash = ""
	writeJSON(w, http.StatusCreated, user)
}

// --- Permission handlers (admin only) ---

func (s *Server) handleGrantPermission(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
	var req struct {
		UserID     string `json:"user_id"`
		EndpointID string `json:"endpoint_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.store.GrantEndpointAccess(r.Context(), req.UserID, req.EndpointID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to grant permission")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "granted"})
}

func (s *Server) handleRevokePermission(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
	var req struct {
		UserID     string `json:"user_id"`
		EndpointID string `json:"endpoint_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.store.RevokeEndpointAccess(r.Context(), req.UserID, req.EndpointID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to revoke permission")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (s *Server) handleListUserPermissions(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	endpoints, err := s.store.ListUserEndpoints(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list permissions")
		return
	}
	if endpoints == nil {
		endpoints = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"user_id": userID, "endpoint_ids": endpoints})
}

// --- Admin session/audit handlers ---

func (s *Server) handleAdminListSessions(w http.ResponseWriter, r *http.Request) {
	identity := getIdentityFromContext(r.Context())
	sessions, err := s.store.ListAllSessions(r.Context(), identity.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list sessions")
		return
	}
	if sessions == nil {
		sessions = []store.Session{}
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (s *Server) handleAdminCloseSession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	sess, err := s.store.GetSession(r.Context(), sessionID)
	if err != nil || sess == nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if sess.State == "closed" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "already_closed"})
		return
	}
	if err := s.store.UpdateSessionState(r.Context(), sessionID, "closed"); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to close session")
		return
	}
	identity := getIdentityFromContext(r.Context())
	if err := s.store.LogAuditEvent(r.Context(), &store.AuditEvent{
		ID: uuid.New().String(), OrgID: identity.OrgID, Action: "session.admin_close",
		UserID: identity.UserID, SessionID: sessionID, EndpointID: sess.EndpointID, CreatedAt: time.Now(),
	}); err != nil {
		s.logger.Warn("failed to log audit event", "action", "session.admin_close", "error", err)
	}
	s.router.BroadcastSessionClosed(sessionID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "closed"})
}

func (s *Server) handleAdminListAuditEvents(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	identity := getIdentityFromContext(r.Context())

	// Check for filter parameters.
	action := r.URL.Query().Get("action")
	sessionID := r.URL.Query().Get("session_id")
	endpointID := r.URL.Query().Get("endpoint_id")

	var events []store.AuditEvent
	var err error

	if action != "" || sessionID != "" || endpointID != "" {
		events, err = s.store.ListAuditEventsFiltered(r.Context(), identity.OrgID, store.AuditFilter{
			Action:     action,
			SessionID:  sessionID,
			EndpointID: endpointID,
			Limit:      limit,
			Offset:     offset,
		})
	} else {
		events, err = s.store.ListAuditEvents(r.Context(), identity.OrgID, limit, offset)
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list audit events")
		return
	}
	if events == nil {
		events = []store.AuditEvent{}
	}
	writeJSON(w, http.StatusOK, events)
}

// --- Health handlers ---

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"uptime": time.Since(s.startTime).Truncate(time.Second).String(),
	})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "not_ready",
			"error":  err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// --- Admin endpoint config handlers ---

// adminEndpointInfo extends endpoint data with runtime info and config override.
type adminEndpointInfo struct {
	ID             string                      `json:"id"`
	OrgID          string                      `json:"org_id"`
	RuntimeID      string                      `json:"runtime_id"`
	RuntimeName    string                      `json:"runtime_name"`
	RuntimeOnline  bool                        `json:"runtime_online"`
	Profile        string                      `json:"profile"`
	Name           string                      `json:"name"`
	Tags           json.RawMessage             `json:"tags"`
	Caps           json.RawMessage             `json:"caps"`
	Security       json.RawMessage             `json:"security"`
	ConfigOverride *store.EndpointConfigOverride `json:"config_override,omitempty"`
}

func (s *Server) handleAdminListEndpoints(w http.ResponseWriter, r *http.Request) {
	identity := getIdentityFromContext(r.Context())

	endpoints, err := s.store.ListEndpoints(r.Context(), identity.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list endpoints")
		return
	}

	runtimes, err := s.store.ListRuntimes(r.Context(), identity.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list runtimes")
		return
	}
	rtMap := make(map[string]store.Runtime, len(runtimes))
	for _, rt := range runtimes {
		rtMap[rt.ID] = rt
	}

	overrides, err := s.store.ListEndpointConfigOverrides(r.Context(), identity.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list overrides")
		return
	}
	overrideMap := make(map[string]store.EndpointConfigOverride, len(overrides))
	for _, o := range overrides {
		overrideMap[o.EndpointID] = o
	}

	result := make([]adminEndpointInfo, 0, len(endpoints))
	for _, ep := range endpoints {
		info := adminEndpointInfo{
			ID:        ep.ID,
			OrgID:     ep.OrgID,
			RuntimeID: ep.RuntimeID,
			Profile:   ep.Profile,
			Name:      ep.Name,
			Tags:      json.RawMessage(ep.Tags),
			Caps:      json.RawMessage(ep.Caps),
			Security:  json.RawMessage(ep.Security),
		}
		if rt, ok := rtMap[ep.RuntimeID]; ok {
			info.RuntimeName = rt.Name
			info.RuntimeOnline = rt.Online
		}
		if o, ok := overrideMap[ep.ID]; ok {
			info.ConfigOverride = &o
		}
		result = append(result, info)
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleGetEndpointConfig(w http.ResponseWriter, r *http.Request) {
	endpointID := chi.URLParam(r, "endpointID")

	override, err := s.store.GetEndpointConfigOverride(r.Context(), endpointID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get config override")
		return
	}
	if override == nil {
		writeJSON(w, http.StatusOK, map[string]any{"endpoint_id": endpointID, "override": nil})
		return
	}
	writeJSON(w, http.StatusOK, override)
}

func (s *Server) handleUpdateEndpointConfig(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
	endpointID := chi.URLParam(r, "endpointID")
	identity := getIdentityFromContext(r.Context())

	var req struct {
		Security *protocol.SecurityProfile `json:"security,omitempty"`
		Limits   *protocol.EndpointLimits  `json:"limits,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate permission_mode if set.
	if req.Security != nil && req.Security.PermissionMode != "" {
		switch req.Security.PermissionMode {
		case "skip", "strict", "auto":
			// valid
		default:
			writeError(w, http.StatusBadRequest, "permission_mode must be skip, strict, or auto")
			return
		}
	}

	// Verify endpoint exists.
	ep, err := s.store.GetEndpoint(r.Context(), endpointID)
	if err != nil || ep == nil {
		writeError(w, http.StatusNotFound, "endpoint not found")
		return
	}

	secJSON := "{}"
	if req.Security != nil {
		if b, err := json.Marshal(req.Security); err == nil {
			secJSON = string(b)
		}
	}
	limJSON := "{}"
	if req.Limits != nil {
		if b, err := json.Marshal(req.Limits); err == nil {
			limJSON = string(b)
		}
	}

	override := &store.EndpointConfigOverride{
		EndpointID: endpointID,
		OrgID:      ep.OrgID,
		Security:   secJSON,
		Limits:     limJSON,
		UpdatedBy:  identity.UserID,
		UpdatedAt:  time.Now(),
	}

	if err := s.store.UpsertEndpointConfigOverride(r.Context(), override); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save config override")
		return
	}

	// Push to runtime.
	pushed := s.router.PushEndpointConfigUpdate(endpointID, req.Security, req.Limits)

	// Audit log.
	if err := s.store.LogAuditEvent(r.Context(), &store.AuditEvent{
		ID: uuid.New().String(), OrgID: identity.OrgID, Action: "endpoint.config_update",
		UserID: identity.UserID, EndpointID: endpointID,
		Detail:    json.RawMessage(fmt.Sprintf(`{"pushed_to_runtime":%t}`, pushed)),
		CreatedAt: time.Now(),
	}); err != nil {
		s.logger.Warn("failed to log audit event", "action", "endpoint.config_update", "error", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":             "saved",
		"pushed_to_runtime":  pushed,
	})
}

// --- Voice / ASR handlers ---

func (s *Server) handleVoiceConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"whisper_available": s.whisperURL != "",
	})
}

var asrUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

const (
	// Max message from client: 64 KB covers ~2 seconds of 16kHz int16 PCM.
	asrMaxClientMessage = 64 * 1024
	// Max message from upstream Whisper: 16 KB (JSON transcription).
	asrMaxUpstreamMessage = 16 * 1024
	// Max concurrent ASR connections per user.
	asrMaxPerUser = 3
)

// asrConns tracks active ASR connections per user for rate limiting.
var (
	asrConnsMu sync.Mutex
	asrConns   = make(map[string]int) // userID → count
)

func (s *Server) handleASRProxy(w http.ResponseWriter, r *http.Request) {
	// Authenticate via ?token= query param (same pattern as /ws/client).
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		tokenStr = r.Header.Get("Authorization")
		if len(tokenStr) > 7 && strings.HasPrefix(tokenStr, "Bearer ") {
			tokenStr = tokenStr[7:]
		}
	}
	if tokenStr == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	identity, err := s.authProvider.ValidateToken(r.Context(), tokenStr)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Per-user connection limit.
	asrConnsMu.Lock()
	if asrConns[identity.UserID] >= asrMaxPerUser {
		asrConnsMu.Unlock()
		http.Error(w, "too many voice connections", http.StatusTooManyRequests)
		return
	}
	asrConns[identity.UserID]++
	asrConnsMu.Unlock()
	defer func() {
		asrConnsMu.Lock()
		asrConns[identity.UserID]--
		if asrConns[identity.UserID] <= 0 {
			delete(asrConns, identity.UserID)
		}
		asrConnsMu.Unlock()
	}()

	// Upgrade client connection.
	clientConn, err := asrUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Warn("asr proxy: client upgrade failed", "error", err)
		return
	}
	defer func() { _ = clientConn.Close() }()

	clientConn.SetReadLimit(asrMaxClientMessage)

	s.logger.Info("asr proxy: connected", "user", identity.Username)

	// Connect to upstream Whisper server.
	upstreamURL, err := url.Parse(s.whisperURL)
	if err != nil {
		s.logger.Warn("asr proxy: invalid whisper_url", "error", err)
		_ = clientConn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "bad upstream config"))
		return
	}

	upstreamConn, _, err := websocket.DefaultDialer.Dial(upstreamURL.String(), nil)
	if err != nil {
		s.logger.Warn("asr proxy: upstream dial failed", "error", err)
		_ = clientConn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "whisper server unavailable"))
		return
	}
	defer func() { _ = upstreamConn.Close() }()

	upstreamConn.SetReadLimit(asrMaxUpstreamMessage)

	// Bidirectional proxy.
	done := make(chan struct{}, 2)

	// Client → Upstream
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			msgType, data, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			if err := upstreamConn.WriteMessage(msgType, data); err != nil {
				return
			}
		}
	}()

	// Upstream → Client
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			msgType, data, err := upstreamConn.ReadMessage()
			if err != nil {
				return
			}
			if err := clientConn.WriteMessage(msgType, data); err != nil {
				return
			}
		}
	}()

	<-done
	s.logger.Info("asr proxy: disconnected", "user", identity.Username)
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
