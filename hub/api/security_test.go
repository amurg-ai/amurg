package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amurg-ai/amurg/hub/auth"
	"github.com/amurg-ai/amurg/hub/config"
	"github.com/amurg-ai/amurg/hub/router"
	"github.com/amurg-ai/amurg/hub/store"
	"github.com/google/uuid"
)

// securityTestEnv holds everything needed for security tests.
type securityTestEnv struct {
	srv         *Server
	adminToken  string
	userToken   string
	store       store.Store
	agentID     string
	adminUser   *store.User
	regularUser *store.User
}

// setupSecurityTest creates a server with both an admin and a regular user,
// plus a seeded agent/runtime.
func setupSecurityTest(t *testing.T) *securityTestEnv {
	t.Helper()
	st, err := store.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := &config.Config{
		Server: config.ServerConfig{
			Addr:           ":0",
			AllowedOrigins: []string{"*"},
			MaxBodyBytes:   1024 * 1024,
		},
		Auth: config.AuthConfig{
			JWTSecret:            "test-secret-at-least-32-chars-long",
			JWTExpiry:            config.Duration{Duration: 1 * time.Hour},
			DefaultAgentAccess:   "all",
			RuntimeTokens:        []config.RuntimeTokenEntry{{RuntimeID: "rt-1", Token: "tok-1"}},
			RuntimeTokenLifetime: config.Duration{Duration: 1 * time.Hour},
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

	authSvc := auth.NewService(st, cfg.Auth)
	rt := router.New(st, authSvc, authSvc, slog.Default(), router.Options{})
	srv := NewServer(st, authSvc, authSvc, authSvc, rt, cfg, ServerOptions{}, slog.Default())

	suffix := uuid.New().String()[:6]
	ctx := context.Background()

	adminUser, err := authSvc.Register(ctx, "secadmin-"+suffix, "adminpassword123", "admin")
	if err != nil {
		t.Fatal(err)
	}
	adminToken, err := authSvc.Login(ctx, "secadmin-"+suffix, "adminpassword123")
	if err != nil {
		t.Fatal(err)
	}

	regularUser, err := authSvc.Register(ctx, "secuser-"+suffix, "userpassword1234", "user")
	if err != nil {
		t.Fatal(err)
	}
	userToken, err := authSvc.Login(ctx, "secuser-"+suffix, "userpassword1234")
	if err != nil {
		t.Fatal(err)
	}

	runtimeID := "rt-sec-" + uuid.New().String()[:8]
	agentID := "ag-sec-" + uuid.New().String()[:8]
	_ = st.UpsertRuntime(ctx, &store.Runtime{
		ID: runtimeID, OrgID: "default", Name: "sec-runtime", Online: true, LastSeen: time.Now(),
	})
	_ = st.UpsertAgent(ctx, &store.Agent{
		ID: agentID, OrgID: "default", RuntimeID: runtimeID, Profile: "default",
		Name: "sec-agent", Tags: "{}", Caps: "{}", Security: "{}",
	})

	return &securityTestEnv{
		srv: srv, adminToken: adminToken, userToken: userToken,
		store: st, agentID: agentID, adminUser: adminUser, regularUser: regularUser,
	}
}

// --- Bugs 1-9: RBAC on admin routes ---

func TestRBAC_AdminRoutesBlockNonAdmin(t *testing.T) {
	env := setupSecurityTest(t)

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/users"},
		{http.MethodGet, "/api/runtimes"},
		{http.MethodPost, "/api/permissions"},
		{http.MethodDelete, "/api/permissions"},
		{http.MethodGet, "/api/users/some-id/permissions"},
		{http.MethodGet, "/api/admin/sessions"},
		{http.MethodPost, "/api/admin/sessions/some-id/close"},
		{http.MethodGet, "/api/admin/audit"},
		{http.MethodGet, "/api/admin/agents"},
		{http.MethodGet, "/api/admin/agents/some-id/config"},
		{http.MethodPut, "/api/admin/agents/some-id/config"},
		{http.MethodPost, "/api/runtime/register/approve"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			var body *bytes.Reader
			if ep.method == http.MethodPost || ep.method == http.MethodPut || ep.method == http.MethodDelete {
				body = bytes.NewReader([]byte("{}"))
			} else {
				body = bytes.NewReader(nil)
			}
			req := httptest.NewRequest(ep.method, ep.path, body)
			req.Header.Set("Authorization", "Bearer "+env.userToken)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			env.srv.mux.ServeHTTP(w, req)

			if w.Code != http.StatusForbidden {
				t.Errorf("expected 403 for non-admin on %s %s, got %d; body: %s",
					ep.method, ep.path, w.Code, w.Body.String())
			}
		})
	}
}

func TestRBAC_AdminRoutesAllowAdmin(t *testing.T) {
	env := setupSecurityTest(t)

	for _, path := range []string{"/api/users", "/api/runtimes", "/api/admin/sessions", "/api/admin/audit", "/api/admin/agents"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer "+env.adminToken)
			w := httptest.NewRecorder()
			env.srv.mux.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("expected 200 for admin on %s, got %d; body: %s", path, w.Code, w.Body.String())
			}
		})
	}
}

// Bug 2: Privilege escalation via user creation.
func TestRBAC_NonAdminCannotCreateUser(t *testing.T) {
	env := setupSecurityTest(t)

	body, _ := json.Marshal(map[string]string{
		"username": "escalated", "password": "escalatedpass123", "role": "admin",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/users", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+env.userToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d; body: %s", w.Code, w.Body.String())
	}
}

// Bug 3: Permission self-grant.
func TestRBAC_NonAdminCannotGrantPermissions(t *testing.T) {
	env := setupSecurityTest(t)

	body, _ := json.Marshal(map[string]string{"user_id": "self", "agent_id": env.agentID})
	req := httptest.NewRequest(http.MethodPost, "/api/permissions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+env.userToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d; body: %s", w.Code, w.Body.String())
	}
}

// Bug 4: Agent config modification by non-admin.
func TestRBAC_NonAdminCannotModifyAgentConfig(t *testing.T) {
	env := setupSecurityTest(t)

	body, _ := json.Marshal(map[string]any{"security": map[string]string{"permission_mode": "skip"}})
	req := httptest.NewRequest(http.MethodPut, "/api/admin/agents/"+env.agentID+"/config", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+env.userToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d; body: %s", w.Code, w.Body.String())
	}
}

// Bug 5: Session close for any user via admin endpoint.
func TestRBAC_NonAdminCannotUseAdminCloseSession(t *testing.T) {
	env := setupSecurityTest(t)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/sessions/some-session/close", nil)
	req.Header.Set("Authorization", "Bearer "+env.userToken)
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d; body: %s", w.Code, w.Body.String())
	}
}

// Bug 6: Session resume IDOR.
func TestSessionResumeIDOR(t *testing.T) {
	env := setupSecurityTest(t)
	ctx := context.Background()

	// Create a session owned by the regular user.
	sessA := uuid.New().String()
	_ = env.store.CreateSession(ctx, &store.Session{
		ID: sessA, OrgID: "default", UserID: env.regularUser.ID, AgentID: env.agentID,
		RuntimeID: "rt-1", Profile: "default", State: "active",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})

	// Create user B via the same store's auth service.
	authCfg := config.AuthConfig{
		JWTSecret:          "test-secret-at-least-32-chars-long",
		JWTExpiry:          config.Duration{Duration: 1 * time.Hour},
		DefaultAgentAccess: "all", RuntimeTokenLifetime: config.Duration{Duration: 1 * time.Hour},
	}
	authSvcB := auth.NewService(env.store, authCfg)
	suffix := uuid.New().String()[:6]
	_, _ = authSvcB.Register(ctx, "userB-"+suffix, "userBpassword123", "user")
	tokenB, _ := authSvcB.Login(ctx, "userB-"+suffix, "userBpassword123")

	// User B tries to resume User A's session.
	body, _ := json.Marshal(map[string]string{
		"agent_id": env.agentID, "resume_session_id": sessA,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tokenB)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for resume IDOR, got %d; body: %s", w.Code, w.Body.String())
	}
}

// Bug 7: Runtime registration approval by non-admin.
func TestRBAC_NonAdminCannotApproveRegistration(t *testing.T) {
	env := setupSecurityTest(t)
	ctx := context.Background()

	_ = env.store.CreateDeviceCode(ctx, &store.DeviceCode{
		ID: uuid.New().String(), UserCode: "TEST-" + uuid.New().String()[:4],
		PollingToken: "poll-tok-" + uuid.New().String()[:6],
		OrgID:        "default", Status: "pending",
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(5 * time.Minute),
	})

	body, _ := json.Marshal(map[string]string{"user_code": "TEST-CODE", "runtime_name": "evil-runtime"})
	req := httptest.NewRequest(http.MethodPost, "/api/runtime/register/approve", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+env.userToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d; body: %s", w.Code, w.Body.String())
	}
}

// Bug 8: Audit log exposure to non-admin.
func TestRBAC_NonAdminCannotReadAuditLogs(t *testing.T) {
	env := setupSecurityTest(t)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/audit", nil)
	req.Header.Set("Authorization", "Bearer "+env.userToken)
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d; body: %s", w.Code, w.Body.String())
	}
}

// Bug 9: User enumeration by non-admin.
func TestRBAC_NonAdminCannotListUsers(t *testing.T) {
	env := setupSecurityTest(t)

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.Header.Set("Authorization", "Bearer "+env.userToken)
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d; body: %s", w.Code, w.Body.String())
	}
}

// --- Bug 10: Host header injection ---

func TestHostHeaderInjection(t *testing.T) {
	env := setupSecurityTest(t)

	tests := []struct {
		name string
		host string
	}{
		{"contains angle brackets", "evil.com/<script>alert(1)</script>"},
		{"contains newline", "evil.com\r\nInjected-Header: true"},
		{"empty host", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/runtime/register", nil)
			req.Host = tc.host
			w := httptest.NewRecorder()
			env.srv.mux.ServeHTTP(w, req)

			if w.Code == http.StatusOK {
				var resp map[string]any
				if err := json.NewDecoder(w.Body).Decode(&resp); err == nil {
					if url, ok := resp["verification_url"].(string); ok {
						if len(url) > 0 && (url[0] == '<' || url[0] == '\r') {
							t.Errorf("verification_url contains injected content: %q", url)
						}
					}
				}
			}
		})
	}
}

// --- Bug 11: X-Forwarded-Proto injection ---

func TestXForwardedProtoInjection(t *testing.T) {
	env := setupSecurityTest(t)

	// javascript: scheme must not appear in verification_url.
	req := httptest.NewRequest(http.MethodPost, "/api/runtime/register", nil)
	req.Host = "localhost:8090"
	req.Header.Set("X-Forwarded-Proto", "javascript")
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		var resp map[string]any
		if err := json.NewDecoder(w.Body).Decode(&resp); err == nil {
			if url, ok := resp["verification_url"].(string); ok {
				if len(url) >= 11 && url[:11] == "javascript:" {
					t.Errorf("verification_url uses javascript: scheme: %q", url)
				}
			}
		}
	}

	// Valid schemes should work.
	for _, scheme := range []string{"http", "https"} {
		req := httptest.NewRequest(http.MethodPost, "/api/runtime/register", nil)
		req.Host = "localhost:8090"
		req.Header.Set("X-Forwarded-Proto", scheme)
		w := httptest.NewRecorder()
		env.srv.mux.ServeHTTP(w, req)

		if w.Code == http.StatusOK {
			var resp map[string]any
			_ = json.NewDecoder(w.Body).Decode(&resp)
			url, _ := resp["verification_url"].(string)
			if len(url) < len(scheme)+3 || url[:len(scheme)+3] != scheme+"://" {
				t.Errorf("expected %s:// prefix, got %q", scheme, url)
			}
		}
	}
}

func TestRuntimeRegister_NonLoopbackHostRequiresBaseURL(t *testing.T) {
	env := setupSecurityTest(t)

	req := httptest.NewRequest(http.MethodPost, "/api/runtime/register", nil)
	req.Host = "evil.example"
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for non-loopback host without base_url, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestRuntimeRegister_LoopbackHostAllowedWithoutBaseURL(t *testing.T) {
	env := setupSecurityTest(t)

	req := httptest.NewRequest(http.MethodPost, "/api/runtime/register", nil)
	req.Host = "localhost:8090"
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for loopback host without base_url, got %d; body: %s", w.Code, w.Body.String())
	}
}

// --- Bug 12: Admin can read messages for any session ---

func TestAdminCanReadOtherUsersMessages(t *testing.T) {
	env := setupSecurityTest(t)
	ctx := context.Background()

	sessID := uuid.New().String()
	_ = env.store.CreateSession(ctx, &store.Session{
		ID: sessID, OrgID: "default", UserID: env.regularUser.ID, AgentID: env.agentID,
		RuntimeID: "rt-1", Profile: "default", State: "active",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	_, _ = env.store.AppendMessage(ctx, &store.Message{
		ID: uuid.New().String(), SessionID: sessID, Seq: 1,
		Direction: "user", Channel: "stdin", Content: "secret message", CreatedAt: time.Now(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessID+"/messages", nil)
	req.Header.Set("Authorization", "Bearer "+env.adminToken)
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected admin to read other user's messages (200), got %d; body: %s", w.Code, w.Body.String())
	}

	var messages []store.Message
	if err := json.NewDecoder(w.Body).Decode(&messages); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Content != "secret message" {
		t.Errorf("expected 1 message with 'secret message', got %d messages", len(messages))
	}
}

// --- Bug 13: Admin can close any session ---

func TestAdminCanCloseOtherUsersSession(t *testing.T) {
	env := setupSecurityTest(t)
	ctx := context.Background()

	sessID := uuid.New().String()
	_ = env.store.CreateSession(ctx, &store.Session{
		ID: sessID, OrgID: "default", UserID: env.regularUser.ID, AgentID: env.agentID,
		RuntimeID: "rt-1", Profile: "default", State: "active",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessID+"/close", nil)
	req.Header.Set("Authorization", "Bearer "+env.adminToken)
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected admin to close other user's session (200), got %d; body: %s", w.Code, w.Body.String())
	}
}

// --- Bug 14: Invalid agent_id returns 201 with null body ---

func TestCreateSession_InvalidAgentID(t *testing.T) {
	env := setupSecurityTest(t)

	body, _ := json.Marshal(map[string]string{"agent_id": "nonexistent-agent"})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+env.userToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	if w.Code == http.StatusCreated {
		t.Fatalf("expected non-201 for invalid agent_id, got 201; body: %s", w.Body.String())
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid agent_id, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestCreateSession_EmptyAgentID(t *testing.T) {
	env := setupSecurityTest(t)

	body, _ := json.Marshal(map[string]string{"agent_id": ""})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+env.userToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty agent_id, got %d; body: %s", w.Code, w.Body.String())
	}
}

// --- Bug 15: Device code poll: expired vs non-existent ---

func TestDeviceCodePoll_NonExistentToken(t *testing.T) {
	env := setupSecurityTest(t)

	body, _ := json.Marshal(map[string]string{"polling_token": "does-not-exist"})
	req := httptest.NewRequest(http.MethodPost, "/api/runtime/register/poll", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-existent polling token, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestDeviceCodePoll_ExpiredToken(t *testing.T) {
	env := setupSecurityTest(t)
	ctx := context.Background()

	_ = env.store.CreateDeviceCode(ctx, &store.DeviceCode{
		ID: uuid.New().String(), UserCode: "EXPD-" + uuid.New().String()[:4],
		PollingToken: "expired-poll-" + uuid.New().String()[:6],
		OrgID:        "default", Status: "pending",
		CreatedAt: time.Now().Add(-10 * time.Minute), ExpiresAt: time.Now().Add(-5 * time.Minute),
	})

	body, _ := json.Marshal(map[string]string{"polling_token": "expired-poll-" + uuid.New().String()[:6]})
	req := httptest.NewRequest(http.MethodPost, "/api/runtime/register/poll", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	// A non-existent token should return 404 — proving it's distinguishable from "expired".
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for random token (proves it's different from expired), got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestDeviceCodePoll_ExpiredVsNonExistent(t *testing.T) {
	env := setupSecurityTest(t)
	ctx := context.Background()

	pollToken := "poll-expired-" + uuid.New().String()[:8]
	_ = env.store.CreateDeviceCode(ctx, &store.DeviceCode{
		ID: uuid.New().String(), UserCode: "EXP2-" + uuid.New().String()[:4],
		PollingToken: pollToken, OrgID: "default", Status: "pending",
		CreatedAt: time.Now().Add(-10 * time.Minute), ExpiresAt: time.Now().Add(-5 * time.Minute),
	})

	// Expired token should return 200 with status "expired".
	body, _ := json.Marshal(map[string]string{"polling_token": pollToken})
	req := httptest.NewRequest(http.MethodPost, "/api/runtime/register/poll", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for expired token, got %d; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "expired" {
		t.Errorf("expected status 'expired', got %q", resp["status"])
	}

	// Non-existent token should return 404.
	body2, _ := json.Marshal(map[string]string{"polling_token": "totally-fake"})
	req2 := httptest.NewRequest(http.MethodPost, "/api/runtime/register/poll", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w2, req2)

	if w2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-existent token, got %d; body: %s", w2.Code, w2.Body.String())
	}
}

// --- Bug 16: Register and poll share rate limiter ---

func TestDeviceCodeRateLimiters_Independent(t *testing.T) {
	st, err := store.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := &config.Config{
		Server:    config.ServerConfig{Addr: ":0", AllowedOrigins: []string{"*"}, MaxBodyBytes: 1024 * 1024},
		Auth:      config.AuthConfig{JWTSecret: "test-secret-at-least-32-chars-long", JWTExpiry: config.Duration{Duration: 1 * time.Hour}, DefaultAgentAccess: "all", RuntimeTokenLifetime: config.Duration{Duration: 1 * time.Hour}},
		Session:   config.SessionConfig{MaxPerUser: 20, MaxMessageBytes: 64 * 1024},
		RateLimit: config.RateLimitConfig{RequestsPerSecond: 100, Burst: 200},
	}

	authSvc := auth.NewService(st, cfg.Auth)
	rt := router.New(st, authSvc, authSvc, slog.Default(), router.Options{})
	srv := NewServer(st, authSvc, authSvc, authSvc, rt, cfg, ServerOptions{}, slog.Default())

	if srv.deviceCodeRL == srv.deviceCodePollRL {
		t.Fatal("register and poll share the same rate limiter instance")
	}
	if srv.deviceCodePollRL == nil {
		t.Fatal("poll rate limiter is nil")
	}
}

// --- Bug 17: Empty JSON body to agent config endpoint clears settings ---

func TestAgentConfig_EmptyBodyRejected(t *testing.T) {
	env := setupSecurityTest(t)

	req := httptest.NewRequest(http.MethodPut, "/api/admin/agents/"+env.agentID+"/config", bytes.NewReader([]byte("{}")))
	req.Header.Set("Authorization", "Bearer "+env.adminToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty config body, got %d; body: %s", w.Code, w.Body.String())
	}
}

// --- Native resume handles should be accepted directly ---

func TestCreateSession_NativeResumeHandleAccepted(t *testing.T) {
	env := setupSecurityTest(t)

	body, _ := json.Marshal(map[string]string{
		"agent_id": env.agentID, "resume_session_id": "native-session-123",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+env.userToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for native resume handle, got %d; body: %s", w.Code, w.Body.String())
	}
}

// --- Bug 19: Wrong HTTP methods return 200 with HTML (SPA fallback) ---

func TestSPAFallback_WrongMethodReturns405(t *testing.T) {
	st, err := store.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	tmpDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmpDir, "index.html"), []byte("<html></html>"), 0644)

	cfg := &config.Config{
		Server:    config.ServerConfig{Addr: ":0", AllowedOrigins: []string{"*"}, MaxBodyBytes: 1024 * 1024, UIStaticDir: tmpDir},
		Auth:      config.AuthConfig{JWTSecret: "test-secret-at-least-32-chars-long", JWTExpiry: config.Duration{Duration: 1 * time.Hour}, DefaultAgentAccess: "all", RuntimeTokenLifetime: config.Duration{Duration: 1 * time.Hour}},
		Session:   config.SessionConfig{MaxPerUser: 20, MaxMessageBytes: 64 * 1024},
		RateLimit: config.RateLimitConfig{RequestsPerSecond: 100, Burst: 200},
	}

	authSvc := auth.NewService(st, cfg.Auth)
	rt := router.New(st, authSvc, authSvc, slog.Default(), router.Options{})
	srv := NewServer(st, authSvc, authSvc, authSvc, rt, cfg, ServerOptions{}, slog.Default())

	// POST to a non-API path should return 405.
	req := httptest.NewRequest(http.MethodPost, "/some-page", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for POST to SPA path, got %d; body: %s", w.Code, w.Body.String())
	}

	// DELETE to a non-API path.
	req2 := httptest.NewRequest(http.MethodDelete, "/settings", nil)
	w2 := httptest.NewRecorder()
	srv.mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for DELETE to SPA path, got %d; body: %s", w2.Code, w2.Body.String())
	}

	// GET should still serve the SPA.
	req3 := httptest.NewRequest(http.MethodGet, "/some-page", nil)
	w3 := httptest.NewRecorder()
	srv.mux.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("expected 200 for GET to SPA path, got %d", w3.Code)
	}
}

func TestSPAFallback_APIPathReturns404(t *testing.T) {
	st, err := store.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	tmpDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmpDir, "index.html"), []byte("<html></html>"), 0644)

	cfg := &config.Config{
		Server:    config.ServerConfig{Addr: ":0", AllowedOrigins: []string{"*"}, MaxBodyBytes: 1024 * 1024, UIStaticDir: tmpDir},
		Auth:      config.AuthConfig{JWTSecret: "test-secret-at-least-32-chars-long", JWTExpiry: config.Duration{Duration: 1 * time.Hour}, DefaultAgentAccess: "all", RuntimeTokenLifetime: config.Duration{Duration: 1 * time.Hour}},
		Session:   config.SessionConfig{MaxPerUser: 20, MaxMessageBytes: 64 * 1024},
		RateLimit: config.RateLimitConfig{RequestsPerSecond: 100, Burst: 200},
	}

	authSvc := auth.NewService(st, cfg.Auth)
	rt := router.New(st, authSvc, authSvc, slog.Default(), router.Options{})
	srv := NewServer(st, authSvc, authSvc, authSvc, rt, cfg, ServerOptions{}, slog.Default())

	req := httptest.NewRequest(http.MethodGet, "/api/nonexistent", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for GET /api/nonexistent, got %d; body: %s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected application/json content-type for API 404, got %q", ct)
	}
}

// --- Bug 20: Literal JSON null accepted as valid request body, returns 201 ---

func TestCreateSession_NullBody(t *testing.T) {
	env := setupSecurityTest(t)

	req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewReader([]byte("null")))
	req.Header.Set("Authorization", "Bearer "+env.userToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	if w.Code == http.StatusCreated {
		t.Fatalf("expected non-201 for null body, got 201; body: %s", w.Body.String())
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for null body, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestSecurityHeaders_MicrophoneAllowed(t *testing.T) {
	env := setupSecurityTest(t)
	req := httptest.NewRequest("GET", "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+env.userToken)
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, req)

	pp := w.Header().Get("Permissions-Policy")
	if pp == "" {
		t.Fatal("Permissions-Policy header missing")
	}
	if !strings.Contains(pp, "microphone=(self)") {
		t.Errorf("Permissions-Policy must allow microphone=(self) for voice input, got: %s", pp)
	}
}
