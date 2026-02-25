package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/amurg-ai/amurg/hub/internal/auth"
	"github.com/amurg-ai/amurg/hub/internal/config"
	"github.com/amurg-ai/amurg/hub/internal/router"
	"github.com/amurg-ai/amurg/hub/internal/store"
)

func setupTestServer(t *testing.T) (*Server, *auth.Service, store.Store) {
	t.Helper()
	s, err := store.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	cfg := &config.Config{
		Server: config.ServerConfig{
			Addr:           ":0",
			AllowedOrigins: []string{"*"},
			MaxBodyBytes:   1024 * 1024,
		},
		Auth: config.AuthConfig{
			JWTSecret:             "test-secret-at-least-32-chars-long",
			JWTExpiry:             config.Duration{Duration: 1 * time.Hour},
			DefaultEndpointAccess: "all",
			RuntimeTokens:        []config.RuntimeTokenEntry{{RuntimeID: "rt-1", Token: "tok-1"}},
			RuntimeTokenLifetime:  config.Duration{Duration: 1 * time.Hour},
		},
		Session: config.SessionConfig{
			MaxPerUser:      20,
			MaxMessageBytes: 64 * 1024,
		},
		RateLimit: config.RateLimitConfig{
			RequestsPerSecond: 100,
			Burst:             200,
		},
	}

	authSvc := auth.NewService(s, cfg.Auth)
	rt := router.New(s, authSvc, authSvc, slog.Default(), router.Options{})
	srv := NewServer(s, authSvc, authSvc, authSvc, rt, cfg, slog.Default())
	return srv, authSvc, s
}

func createTestUserAndGetToken(t *testing.T, authSvc *auth.Service, s store.Store) string {
	t.Helper()
	ctx := context.Background()
	_, err := authSvc.Register(ctx, "testuser", "testpassword123", "user")
	if err != nil {
		t.Fatal(err)
	}
	token, err := authSvc.Login(ctx, "testuser", "testpassword123")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func createAdminAndGetToken(t *testing.T, authSvc *auth.Service) string {
	t.Helper()
	ctx := context.Background()
	_, err := authSvc.Register(ctx, "adminuser", "adminpassword123", "admin")
	if err != nil {
		t.Fatal(err)
	}
	token, err := authSvc.Login(ctx, "adminuser", "adminpassword123")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// seedEndpointAndRuntime inserts a runtime and endpoint into the store so that
// session creation and endpoint listing work properly.
func seedEndpointAndRuntime(t *testing.T, s store.Store) (runtimeID, endpointID string) {
	t.Helper()
	ctx := context.Background()
	runtimeID = "rt-test-" + uuid.New().String()[:8]
	endpointID = "ep-test-" + uuid.New().String()[:8]

	err := s.UpsertRuntime(ctx, &store.Runtime{
		ID:       runtimeID,
		OrgID:    "default",
		Name:     "test-runtime",
		Online:   true,
		LastSeen: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	err = s.UpsertEndpoint(ctx, &store.Endpoint{
		ID:        endpointID,
		OrgID:     "default",
		RuntimeID: runtimeID,
		Profile:   "default",
		Name:      "test-endpoint",
		Tags:      "{}",
		Caps:      "{}",
		Security:  "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	return runtimeID, endpointID
}

// parseJSONResponse decodes the JSON body of the response into the given target.
func parseJSONResponse(t *testing.T, w *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(w.Body).Decode(target); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
}

// --- Tests ---

func TestHealthz(t *testing.T) {
	srv, _, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp map[string]string
	parseJSONResponse(t, w, &resp)

	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got %q", resp["status"])
	}
	if _, ok := resp["uptime"]; !ok {
		t.Error("expected uptime field in response")
	}
}

func TestReadyz(t *testing.T) {
	srv, _, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp map[string]string
	parseJSONResponse(t, w, &resp)

	if resp["status"] != "ready" {
		t.Errorf("expected status ready, got %q", resp["status"])
	}
}

func TestLoginSuccess(t *testing.T) {
	srv, authSvc, _ := setupTestServer(t)

	// Register a user first.
	ctx := context.Background()
	_, err := authSvc.Register(ctx, "loginuser", "loginpassword123", "user")
	if err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]string{
		"username": "loginuser",
		"password": "loginpassword123",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	parseJSONResponse(t, w, &resp)

	if resp["token"] == "" {
		t.Error("expected non-empty token in response")
	}
}

func TestLoginInvalidCredentials(t *testing.T) {
	srv, authSvc, _ := setupTestServer(t)

	// Register a user.
	ctx := context.Background()
	_, err := authSvc.Register(ctx, "loginuser2", "loginpassword123", "user")
	if err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]string{
		"username": "loginuser2",
		"password": "wrongpassword",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", w.Code)
	}

	var resp map[string]string
	parseJSONResponse(t, w, &resp)

	if resp["error"] != "invalid credentials" {
		t.Errorf("expected 'invalid credentials' error, got %q", resp["error"])
	}
}

func TestLoginUsernameValidation(t *testing.T) {
	srv, _, _ := setupTestServer(t)

	tests := []struct {
		name     string
		username string
		wantCode int
	}{
		{"too short", "ab", http.StatusBadRequest},
		{"too long", string(make([]byte, 65)), http.StatusBadRequest},
		{"valid length", "abc", http.StatusUnauthorized}, // valid username format but user doesn't exist
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{
				"username": tc.username,
				"password": "somepassword123",
			})
			req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			srv.mux.ServeHTTP(w, req)

			if w.Code != tc.wantCode {
				t.Errorf("username %q: expected status %d, got %d; body: %s",
					tc.username, tc.wantCode, w.Code, w.Body.String())
			}
		})
	}
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	srv, authSvc, s := setupTestServer(t)
	token := createTestUserAndGetToken(t, authSvc, s)

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	parseJSONResponse(t, w, &resp)

	if resp["username"] != "testuser" {
		t.Errorf("expected username 'testuser', got %q", resp["username"])
	}
	if resp["role"] != "user" {
		t.Errorf("expected role 'user', got %q", resp["role"])
	}
}

func TestAuthMiddleware_NoToken(t *testing.T) {
	srv, _, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", w.Code)
	}

	var resp map[string]string
	parseJSONResponse(t, w, &resp)

	if resp["error"] == "" {
		t.Error("expected non-empty error message")
	}
}

func TestAuthMiddleware_ExpiredToken(t *testing.T) {
	// Create a server with a very short JWT expiry so we can produce an expired token.
	s, err := store.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	cfg := &config.Config{
		Server: config.ServerConfig{
			Addr:           ":0",
			AllowedOrigins: []string{"*"},
			MaxBodyBytes:   1024 * 1024,
		},
		Auth: config.AuthConfig{
			JWTSecret:             "test-secret-at-least-32-chars-long",
			JWTExpiry:             config.Duration{Duration: 1 * time.Millisecond}, // extremely short
			DefaultEndpointAccess: "all",
			RuntimeTokens:        []config.RuntimeTokenEntry{{RuntimeID: "rt-1", Token: "tok-1"}},
			RuntimeTokenLifetime:  config.Duration{Duration: 1 * time.Hour},
		},
		Session: config.SessionConfig{
			MaxPerUser:      20,
			MaxMessageBytes: 64 * 1024,
		},
		RateLimit: config.RateLimitConfig{
			RequestsPerSecond: 100,
			Burst:             200,
		},
	}

	authSvc := auth.NewService(s, cfg.Auth)
	rt := router.New(s, authSvc, authSvc, slog.Default(), router.Options{})
	srv := NewServer(s, authSvc, authSvc, authSvc, rt, cfg, slog.Default())

	ctx := context.Background()
	_, err = authSvc.Register(ctx, "expuser", "exppassword123", "user")
	if err != nil {
		t.Fatal(err)
	}
	token, err := authSvc.Login(ctx, "expuser", "exppassword123")
	if err != nil {
		t.Fatal(err)
	}

	// Wait for the token to expire.
	time.Sleep(10 * time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401 for expired token, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestAdminMiddleware(t *testing.T) {
	srv, authSvc, s := setupTestServer(t)
	token := createTestUserAndGetToken(t, authSvc, s)

	// Try accessing an admin-only route (GET /api/users) with a non-admin token.
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403 for non-admin, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	parseJSONResponse(t, w, &resp)

	if resp["error"] != "admin access required" {
		t.Errorf("expected 'admin access required', got %q", resp["error"])
	}
}

func TestAdminMiddleware_AdminAllowed(t *testing.T) {
	srv, authSvc, _ := setupTestServer(t)
	adminToken := createAdminAndGetToken(t, authSvc)

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 for admin, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestListEndpoints(t *testing.T) {
	srv, authSvc, s := setupTestServer(t)
	token := createTestUserAndGetToken(t, authSvc, s)

	req := httptest.NewRequest(http.MethodGet, "/api/endpoints", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var endpoints []store.Endpoint
	parseJSONResponse(t, w, &endpoints)

	if len(endpoints) != 0 {
		t.Errorf("expected empty array, got %d endpoints", len(endpoints))
	}

	// Verify the response body is a JSON array, not null.
	body := w.Body.String()
	if body == "null\n" || body == "null" {
		t.Error("expected [] but got null")
	}
}

func TestCreateSession(t *testing.T) {
	srv, authSvc, s := setupTestServer(t)
	token := createTestUserAndGetToken(t, authSvc, s)
	_, endpointID := seedEndpointAndRuntime(t, s)

	body, _ := json.Marshal(map[string]string{
		"endpoint_id": endpointID,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d; body: %s", w.Code, w.Body.String())
	}

	var sess store.Session
	parseJSONResponse(t, w, &sess)

	if sess.ID == "" {
		t.Error("expected non-empty session ID")
	}
	if sess.EndpointID != endpointID {
		t.Errorf("expected endpoint_id %q, got %q", endpointID, sess.EndpointID)
	}
	if sess.State != "creating" {
		t.Errorf("expected state 'creating', got %q", sess.State)
	}
}

func TestGetMessages(t *testing.T) {
	srv, authSvc, s := setupTestServer(t)
	token := createTestUserAndGetToken(t, authSvc, s)
	_, endpointID := seedEndpointAndRuntime(t, s)

	// Look up the user to get the user ID.
	ctx := context.Background()
	user, _ := s.GetUser(ctx, "default", "testuser")

	// Create a session directly in the store.
	sessID := uuid.New().String()
	err := s.CreateSession(ctx, &store.Session{
		ID:         sessID,
		OrgID:      "default",
		UserID:     user.ID,
		EndpointID: endpointID,
		RuntimeID:  "rt-1",
		Profile:    "default",
		State:      "active",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Append a message.
	_, err = s.AppendMessage(ctx, &store.Message{
		ID:        uuid.New().String(),
		SessionID: sessID,
		Seq:       1,
		Direction: "user",
		Channel:   "stdin",
		Content:   "hello agent",
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessID+"/messages", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var messages []store.Message
	parseJSONResponse(t, w, &messages)

	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0].Content != "hello agent" {
		t.Errorf("expected content 'hello agent', got %q", messages[0].Content)
	}
}

func TestGetMessages_OtherUserSession(t *testing.T) {
	srv, authSvc, s := setupTestServer(t)

	// Create the first user (the session owner).
	ctx := context.Background()
	owner, err := authSvc.Register(ctx, "owner", "ownerpassword123", "user")
	if err != nil {
		t.Fatal(err)
	}

	// Create the second user (the intruder).
	_, err = authSvc.Register(ctx, "intruder", "intruderpassword123", "user")
	if err != nil {
		t.Fatal(err)
	}
	intruderToken, err := authSvc.Login(ctx, "intruder", "intruderpassword123")
	if err != nil {
		t.Fatal(err)
	}

	_, endpointID := seedEndpointAndRuntime(t, s)

	// Create a session owned by the first user.
	sessID := uuid.New().String()
	err = s.CreateSession(ctx, &store.Session{
		ID:         sessID,
		OrgID:      "default",
		UserID:     owner.ID,
		EndpointID: endpointID,
		RuntimeID:  "rt-1",
		Profile:    "default",
		State:      "active",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Second user tries to read messages from the first user's session.
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessID+"/messages", nil)
	req.Header.Set("Authorization", "Bearer "+intruderToken)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	parseJSONResponse(t, w, &resp)

	if resp["error"] != "access denied" {
		t.Errorf("expected 'access denied', got %q", resp["error"])
	}
}

func TestRateLimiting(t *testing.T) {
	// Create a server with a very low rate limit.
	s, err := store.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	cfg := &config.Config{
		Server: config.ServerConfig{
			Addr:           ":0",
			AllowedOrigins: []string{"*"},
			MaxBodyBytes:   1024 * 1024,
		},
		Auth: config.AuthConfig{
			JWTSecret:             "test-secret-at-least-32-chars-long",
			JWTExpiry:             config.Duration{Duration: 1 * time.Hour},
			DefaultEndpointAccess: "all",
			RuntimeTokens:        []config.RuntimeTokenEntry{{RuntimeID: "rt-1", Token: "tok-1"}},
			RuntimeTokenLifetime:  config.Duration{Duration: 1 * time.Hour},
		},
		Session: config.SessionConfig{
			MaxPerUser:      20,
			MaxMessageBytes: 64 * 1024,
		},
		RateLimit: config.RateLimitConfig{
			RequestsPerSecond: 1,
			Burst:             3, // allow 3 requests, then throttle
		},
	}

	authSvc := auth.NewService(s, cfg.Auth)
	rt := router.New(s, authSvc, authSvc, slog.Default(), router.Options{})
	srv := NewServer(s, authSvc, authSvc, authSvc, rt, cfg, slog.Default())

	ctx := context.Background()
	_, err = authSvc.Register(ctx, "ratelimituser", "ratelimitpassword123", "user")
	if err != nil {
		t.Fatal(err)
	}
	token, err := authSvc.Login(ctx, "ratelimituser", "ratelimitpassword123")
	if err != nil {
		t.Fatal(err)
	}

	got429 := false
	// Send enough requests to exhaust the burst. The bucket starts full (3 tokens),
	// so we need to exceed that.
	for i := 0; i < 20; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/endpoints", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, req)

		if w.Code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}

	if !got429 {
		t.Error("expected to receive a 429 Too Many Requests response, but never got one")
	}
}

func TestListSessions_Empty(t *testing.T) {
	srv, authSvc, s := setupTestServer(t)
	token := createTestUserAndGetToken(t, authSvc, s)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var sessions []store.Session
	parseJSONResponse(t, w, &sessions)

	if len(sessions) != 0 {
		t.Errorf("expected empty array, got %d sessions", len(sessions))
	}
}

func TestGetMessages_SessionNotFound(t *testing.T) {
	srv, authSvc, s := setupTestServer(t)
	token := createTestUserAndGetToken(t, authSvc, s)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/nonexistent-session/messages", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestCreateSession_InvalidBody(t *testing.T) {
	srv, authSvc, s := setupTestServer(t)
	token := createTestUserAndGetToken(t, authSvc, s)

	req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewReader([]byte("not json")))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestCORS_Preflight(t *testing.T) {
	srv, _, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodOptions, "/api/endpoints", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected status 204 for OPTIONS, got %d", w.Code)
	}

	allowOrigin := w.Header().Get("Access-Control-Allow-Origin")
	if allowOrigin != "*" {
		t.Errorf("expected CORS allow-origin '*', got %q", allowOrigin)
	}
}

func TestCreateUser_AdminOnly(t *testing.T) {
	srv, authSvc, _ := setupTestServer(t)
	adminToken := createAdminAndGetToken(t, authSvc)

	body, _ := json.Marshal(map[string]string{
		"username": "newuser",
		"password": "newpassword123",
		"role":     "user",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/users", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d; body: %s", w.Code, w.Body.String())
	}

	var user store.User
	parseJSONResponse(t, w, &user)

	if user.Username != "newuser" {
		t.Errorf("expected username 'newuser', got %q", user.Username)
	}
	if user.PasswordHash != "" {
		t.Error("password hash should be stripped from response")
	}
}

func TestGetMessages_AdminCanAccessAnySession(t *testing.T) {
	srv, authSvc, s := setupTestServer(t)

	ctx := context.Background()
	// Create a regular user and a session.
	owner, err := authSvc.Register(ctx, "sesowner", "sesownerpassword123", "user")
	if err != nil {
		t.Fatal(err)
	}

	_, endpointID := seedEndpointAndRuntime(t, s)

	sessID := uuid.New().String()
	err = s.CreateSession(ctx, &store.Session{
		ID:         sessID,
		OrgID:      "default",
		UserID:     owner.ID,
		EndpointID: endpointID,
		RuntimeID:  "rt-1",
		Profile:    "default",
		State:      "active",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Admin should be able to access any session's messages.
	adminToken := createAdminAndGetToken(t, authSvc)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/sessions/%s/messages", sessID), nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected admin to get 200, got %d; body: %s", w.Code, w.Body.String())
	}
}
