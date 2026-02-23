package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestLoadConfig(t *testing.T) {
	configJSON := `{
		"server": {
			"addr": ":8080",
			"allowed_origins": ["http://localhost:3000"]
		},
		"auth": {
			"jwt_secret": "my-super-secret-jwt-key-at-least-32",
			"jwt_expiry": "2h",
			"runtime_tokens": [
				{"runtime_id": "rt-1", "token": "tok-1", "name": "Runtime One"}
			],
			"runtime_token_secret": "hmac-secret",
			"runtime_token_lifetime": "30m",
			"initial_admin": {
				"username": "admin",
				"password": "admin123"
			},
			"default_endpoint_access": "none"
		},
		"storage": {
			"driver": "sqlite",
			"dsn": "test.db",
			"retention": "72h"
		},
		"session": {
			"max_per_user": 5,
			"idle_timeout": "10m",
			"turn_based": true,
			"replay_buffer": 50,
			"max_message_bytes": 32768
		},
		"logging": {
			"level": "debug",
			"format": "text"
		},
		"rate_limit": {
			"requests_per_second": 20,
			"burst": 40
		}
	}`

	path := writeTempConfig(t, configJSON)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Server
	if cfg.Server.Addr != ":8080" {
		t.Errorf("Server.Addr: got %q, want %q", cfg.Server.Addr, ":8080")
	}
	if len(cfg.Server.AllowedOrigins) != 1 || cfg.Server.AllowedOrigins[0] != "http://localhost:3000" {
		t.Errorf("Server.AllowedOrigins: got %v, want [http://localhost:3000]", cfg.Server.AllowedOrigins)
	}

	// Auth
	if cfg.Auth.JWTSecret != "my-super-secret-jwt-key-at-least-32" {
		t.Errorf("Auth.JWTSecret: got %q", cfg.Auth.JWTSecret)
	}
	if cfg.Auth.JWTExpiry.Duration != 2*time.Hour {
		t.Errorf("Auth.JWTExpiry: got %v, want 2h", cfg.Auth.JWTExpiry.Duration)
	}
	if len(cfg.Auth.RuntimeTokens) != 1 {
		t.Fatalf("Auth.RuntimeTokens: got %d, want 1", len(cfg.Auth.RuntimeTokens))
	}
	if cfg.Auth.RuntimeTokens[0].RuntimeID != "rt-1" {
		t.Errorf("RuntimeTokens[0].RuntimeID: got %q", cfg.Auth.RuntimeTokens[0].RuntimeID)
	}
	if cfg.Auth.RuntimeTokens[0].Token != "tok-1" {
		t.Errorf("RuntimeTokens[0].Token: got %q", cfg.Auth.RuntimeTokens[0].Token)
	}
	if cfg.Auth.RuntimeTokenSecret != "hmac-secret" {
		t.Errorf("Auth.RuntimeTokenSecret: got %q", cfg.Auth.RuntimeTokenSecret)
	}
	if cfg.Auth.RuntimeTokenLifetime.Duration != 30*time.Minute {
		t.Errorf("Auth.RuntimeTokenLifetime: got %v, want 30m", cfg.Auth.RuntimeTokenLifetime.Duration)
	}
	if cfg.Auth.InitialAdmin == nil {
		t.Fatal("Auth.InitialAdmin is nil")
	}
	if cfg.Auth.InitialAdmin.Username != "admin" {
		t.Errorf("InitialAdmin.Username: got %q", cfg.Auth.InitialAdmin.Username)
	}
	if cfg.Auth.DefaultEndpointAccess != "none" {
		t.Errorf("Auth.DefaultEndpointAccess: got %q, want %q", cfg.Auth.DefaultEndpointAccess, "none")
	}

	// Storage
	if cfg.Storage.Driver != "sqlite" {
		t.Errorf("Storage.Driver: got %q, want %q", cfg.Storage.Driver, "sqlite")
	}
	if cfg.Storage.DSN != "test.db" {
		t.Errorf("Storage.DSN: got %q, want %q", cfg.Storage.DSN, "test.db")
	}
	if cfg.Storage.Retention.Duration != 72*time.Hour {
		t.Errorf("Storage.Retention: got %v, want 72h", cfg.Storage.Retention.Duration)
	}

	// Session
	if cfg.Session.MaxPerUser != 5 {
		t.Errorf("Session.MaxPerUser: got %d, want 5", cfg.Session.MaxPerUser)
	}
	if cfg.Session.IdleTimeout.Duration != 10*time.Minute {
		t.Errorf("Session.IdleTimeout: got %v, want 10m", cfg.Session.IdleTimeout.Duration)
	}
	if !cfg.Session.TurnBased {
		t.Error("Session.TurnBased: got false, want true")
	}
	if cfg.Session.ReplayBuffer != 50 {
		t.Errorf("Session.ReplayBuffer: got %d, want 50", cfg.Session.ReplayBuffer)
	}
	if cfg.Session.MaxMessageBytes != 32768 {
		t.Errorf("Session.MaxMessageBytes: got %d, want 32768", cfg.Session.MaxMessageBytes)
	}

	// Logging
	if cfg.Logging.Level != "debug" {
		t.Errorf("Logging.Level: got %q, want %q", cfg.Logging.Level, "debug")
	}
	if cfg.Logging.Format != "text" {
		t.Errorf("Logging.Format: got %q, want %q", cfg.Logging.Format, "text")
	}

	// Rate limit
	if cfg.RateLimit.RequestsPerSecond != 20 {
		t.Errorf("RateLimit.RequestsPerSecond: got %f, want 20", cfg.RateLimit.RequestsPerSecond)
	}
	if cfg.RateLimit.Burst != 40 {
		t.Errorf("RateLimit.Burst: got %d, want 40", cfg.RateLimit.Burst)
	}
}

func TestValidateRequired(t *testing.T) {
	// Missing server.addr
	noAddr := `{
		"server": {},
		"auth": {"jwt_secret": "some-secret-value-long-enough"}
	}`
	path := writeTempConfig(t, noAddr)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing server.addr, got nil")
	}

	// Missing auth.jwt_secret
	noSecret := `{
		"server": {"addr": ":8080"},
		"auth": {}
	}`
	path = writeTempConfig(t, noSecret)
	_, err = Load(path)
	if err == nil {
		t.Fatal("expected error for missing auth.jwt_secret, got nil")
	}
}

func TestApplyDefaults(t *testing.T) {
	// Minimal valid config -- only required fields
	minimal := `{
		"server": {"addr": ":8080"},
		"auth": {"jwt_secret": "my-secret-key-for-testing-purposes"}
	}`

	path := writeTempConfig(t, minimal)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Check all defaults
	if cfg.Auth.JWTExpiry.Duration != 24*time.Hour {
		t.Errorf("default JWTExpiry: got %v, want 24h", cfg.Auth.JWTExpiry.Duration)
	}
	if cfg.Storage.Driver != "sqlite" {
		t.Errorf("default Storage.Driver: got %q, want %q", cfg.Storage.Driver, "sqlite")
	}
	if cfg.Storage.DSN != "amurg.db" {
		t.Errorf("default Storage.DSN: got %q, want %q", cfg.Storage.DSN, "amurg.db")
	}
	if cfg.Storage.Retention.Duration != 30*24*time.Hour {
		t.Errorf("default Storage.Retention: got %v, want 720h", cfg.Storage.Retention.Duration)
	}
	if cfg.Session.MaxPerUser != 20 {
		t.Errorf("default Session.MaxPerUser: got %d, want 20", cfg.Session.MaxPerUser)
	}
	if cfg.Session.IdleTimeout.Duration != 30*time.Minute {
		t.Errorf("default Session.IdleTimeout: got %v, want 30m", cfg.Session.IdleTimeout.Duration)
	}
	if cfg.Session.ReplayBuffer != 100 {
		t.Errorf("default Session.ReplayBuffer: got %d, want 100", cfg.Session.ReplayBuffer)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("default Logging.Level: got %q, want %q", cfg.Logging.Level, "info")
	}
	if cfg.Logging.Format != "json" {
		t.Errorf("default Logging.Format: got %q, want %q", cfg.Logging.Format, "json")
	}
	if cfg.Auth.DefaultEndpointAccess != "all" {
		t.Errorf("default DefaultEndpointAccess: got %q, want %q", cfg.Auth.DefaultEndpointAccess, "all")
	}
	if len(cfg.Server.AllowedOrigins) != 1 || cfg.Server.AllowedOrigins[0] != "*" {
		t.Errorf("default AllowedOrigins: got %v, want [*]", cfg.Server.AllowedOrigins)
	}
	if cfg.Auth.RuntimeTokenLifetime.Duration != 1*time.Hour {
		t.Errorf("default RuntimeTokenLifetime: got %v, want 1h", cfg.Auth.RuntimeTokenLifetime.Duration)
	}
	if cfg.RateLimit.RequestsPerSecond != 10 {
		t.Errorf("default RateLimit.RequestsPerSecond: got %f, want 10", cfg.RateLimit.RequestsPerSecond)
	}
	if cfg.RateLimit.Burst != 20 {
		t.Errorf("default RateLimit.Burst: got %d, want 20", cfg.RateLimit.Burst)
	}
	if cfg.Server.MaxBodyBytes != 1024*1024 {
		t.Errorf("default Server.MaxBodyBytes: got %d, want %d", cfg.Server.MaxBodyBytes, 1024*1024)
	}
	if cfg.Session.MaxMessageBytes != 64*1024 {
		t.Errorf("default Session.MaxMessageBytes: got %d, want %d", cfg.Session.MaxMessageBytes, 64*1024)
	}
}
