package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/amurg-ai/amurg/hub/internal/config"
	"github.com/amurg-ai/amurg/hub/internal/store"
)

func newTestAuthService(t *testing.T) (*Service, store.Store) {
	t.Helper()
	s, err := store.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	cfg := config.AuthConfig{
		JWTSecret: "test-secret-at-least-32-chars-long",
		JWTExpiry: config.Duration{Duration: 1 * time.Hour},
		RuntimeTokens: []config.RuntimeTokenEntry{
			{RuntimeID: "rt-1", Token: "token-1"},
		},
		RuntimeTokenSecret:   "test-hmac-secret-for-rotation",
		RuntimeTokenLifetime: config.Duration{Duration: 1 * time.Hour},
	}

	svc := NewService(s, cfg)
	return svc, s
}

func TestBootstrap(t *testing.T) {
	svc, s := newTestAuthService(t)
	ctx := context.Background()

	admin := &config.InitialAdmin{
		Username: "admin",
		Password: "admin-password",
	}

	// First bootstrap should create the admin user
	if err := svc.BootstrapAdmin(ctx, admin); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	// Verify user was created
	user, err := s.GetUser(ctx, "default", "admin")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user == nil {
		t.Fatal("admin user not created")
	}
	if user.Role != "admin" {
		t.Errorf("Role: got %q, want %q", user.Role, "admin")
	}
	if user.Username != "admin" {
		t.Errorf("Username: got %q, want %q", user.Username, "admin")
	}

	// Second bootstrap should be idempotent (no error, no duplicate)
	if err := svc.BootstrapAdmin(ctx, admin); err != nil {
		t.Fatalf("Bootstrap (idempotent): %v", err)
	}

	users, err := s.ListUsers(ctx, "default")
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("expected 1 user after double bootstrap, got %d", len(users))
	}

	// Bootstrap with nil should be a no-op
	if err := svc.BootstrapAdmin(ctx, nil); err != nil {
		t.Fatalf("BootstrapAdmin(nil): %v", err)
	}
}

func TestLoginSuccess(t *testing.T) {
	svc, _ := newTestAuthService(t)
	ctx := context.Background()

	// Register a user first
	_, err := svc.Register(ctx, "alice", "secret123", "user")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Login
	token, err := svc.Login(ctx, "alice", "secret123")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if token == "" {
		t.Fatal("Login returned empty token")
	}

	// Token should be a valid JWT (three dot-separated parts)
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Errorf("expected JWT with 3 parts, got %d", len(parts))
	}
}

func TestLoginWrongPassword(t *testing.T) {
	svc, _ := newTestAuthService(t)
	ctx := context.Background()

	_, err := svc.Register(ctx, "alice", "secret123", "user")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, err = svc.Login(ctx, "alice", "wrong-password")
	if err == nil {
		t.Fatal("expected error for wrong password, got nil")
	}
	if err != ErrInvalidCredentials {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestLoginNonexistentUser(t *testing.T) {
	svc, _ := newTestAuthService(t)
	ctx := context.Background()

	_, err := svc.Login(ctx, "nobody", "password")
	if err == nil {
		t.Fatal("expected error for nonexistent user, got nil")
	}
	if err != ErrInvalidCredentials {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestValidateToken(t *testing.T) {
	svc, _ := newTestAuthService(t)
	ctx := context.Background()

	user, err := svc.Register(ctx, "alice", "secret123", "user")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	token, err := svc.Login(ctx, "alice", "secret123")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	identity, err := svc.ValidateToken(ctx, token)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}

	if identity.UserID != user.ID {
		t.Errorf("UserID: got %q, want %q", identity.UserID, user.ID)
	}
	if identity.Username != "alice" {
		t.Errorf("Username: got %q, want %q", identity.Username, "alice")
	}
	if identity.Role != "user" {
		t.Errorf("Role: got %q, want %q", identity.Role, "user")
	}
	if identity.OrgID != "default" {
		t.Errorf("OrgID: got %q, want %q", identity.OrgID, "default")
	}
}

func TestExpiredToken(t *testing.T) {
	s, err := store.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	// Create a service with a very short (already past) expiry
	cfg := config.AuthConfig{
		JWTSecret: "test-secret-at-least-32-chars-long",
		JWTExpiry: config.Duration{Duration: -1 * time.Hour}, // expired 1h ago
		RuntimeTokens: []config.RuntimeTokenEntry{
			{RuntimeID: "rt-1", Token: "token-1"},
		},
		RuntimeTokenSecret:   "test-hmac-secret-for-rotation",
		RuntimeTokenLifetime: config.Duration{Duration: 1 * time.Hour},
	}

	svc := NewService(s, cfg)
	ctx := context.Background()

	_, err = svc.Register(ctx, "alice", "secret123", "user")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	token, err := svc.Login(ctx, "alice", "secret123")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	_, err = svc.ValidateToken(ctx, token)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
	if err != ErrUnauthorized {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestValidateRuntimeToken(t *testing.T) {
	svc, _ := newTestAuthService(t)

	// Valid token
	if !svc.ValidateRuntimeToken("rt-1", "token-1") {
		t.Error("expected valid runtime token to return true")
	}

	// Wrong token
	if svc.ValidateRuntimeToken("rt-1", "wrong-token") {
		t.Error("expected wrong token to return false")
	}

	// Unknown runtime ID
	if svc.ValidateRuntimeToken("rt-unknown", "token-1") {
		t.Error("expected unknown runtime ID to return false")
	}
}

func TestGenerateAndValidateTimeLimitedToken(t *testing.T) {
	svc, _ := newTestAuthService(t)

	token := svc.GenerateRuntimeToken("my-runtime")
	if token == "" {
		t.Fatal("GenerateRuntimeToken returned empty string")
	}

	// Token should have 3 colon-separated parts
	parts := strings.SplitN(token, ":", 3)
	if len(parts) != 3 {
		t.Fatalf("expected 3 colon-separated parts, got %d", len(parts))
	}

	runtimeID, err := svc.ValidateTimeLimitedToken(token)
	if err != nil {
		t.Fatalf("ValidateTimeLimitedToken: %v", err)
	}
	if runtimeID != "my-runtime" {
		t.Errorf("runtimeID: got %q, want %q", runtimeID, "my-runtime")
	}
}

func TestTimeLimitedTokenExpired(t *testing.T) {
	s, err := store.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	cfg := config.AuthConfig{
		JWTSecret:            "test-secret-at-least-32-chars-long",
		JWTExpiry:            config.Duration{Duration: 1 * time.Hour},
		RuntimeTokenSecret:   "test-hmac-secret-for-rotation",
		RuntimeTokenLifetime: config.Duration{Duration: 1 * time.Millisecond},
	}

	svc := NewService(s, cfg)

	token := svc.GenerateRuntimeToken("my-runtime")

	// Sleep long enough for the token to expire
	time.Sleep(10 * time.Millisecond)

	_, err = svc.ValidateTimeLimitedToken(token)
	if err == nil {
		t.Fatal("expected error for expired time-limited token, got nil")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("expected expiry error, got: %v", err)
	}
}

func TestTimeLimitedTokenBadSignature(t *testing.T) {
	svc, _ := newTestAuthService(t)

	token := svc.GenerateRuntimeToken("my-runtime")

	// Tamper with the signature by replacing the last character
	tampered := token[:len(token)-1] + "X"

	_, err := svc.ValidateTimeLimitedToken(tampered)
	if err == nil {
		t.Fatal("expected error for tampered token, got nil")
	}
	if !strings.Contains(err.Error(), "signature") {
		t.Errorf("expected signature error, got: %v", err)
	}
}

func TestRegisterDuplicate(t *testing.T) {
	svc, _ := newTestAuthService(t)
	ctx := context.Background()

	_, err := svc.Register(ctx, "alice", "secret123", "user")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, err = svc.Register(ctx, "alice", "other-password", "user")
	if err == nil {
		t.Fatal("expected error for duplicate registration, got nil")
	}
	if err != ErrUserExists {
		t.Errorf("expected ErrUserExists, got %v", err)
	}
}
