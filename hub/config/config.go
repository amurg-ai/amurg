// Package config handles hub configuration loading and validation.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// knownWeakSecrets is a blocklist of secrets that must never be used in production.
var knownWeakSecrets = map[string]bool{
	"local-dev-secret-for-testing-only-32chars!": true,
	"changeme": true,
	"secret":   true,
}

// GenerateRandomSecret returns a cryptographically random 64-character hex string
// suitable for use as a JWT secret.
func GenerateRandomSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Config is the top-level hub configuration.
type Config struct {
	Server    ServerConfig    `json:"server"`
	Auth      AuthConfig      `json:"auth"`
	Storage   StorageConfig   `json:"storage"`
	Session   SessionConfig   `json:"session"`
	Logging   LoggingConfig   `json:"logging"`
	RateLimit RateLimitConfig `json:"rate_limit,omitempty"`
	Billing   BillingConfig   `json:"billing,omitempty"`
}

// BillingConfig defines Stripe billing settings. Disabled by default.
type BillingConfig struct {
	Enabled              bool   `json:"enabled,omitempty"`               // false by default
	StripeSecretKey      string `json:"stripe_secret_key,omitempty"`
	StripeWebhookSecret  string `json:"stripe_webhook_secret,omitempty"`
	StripePublishableKey string `json:"stripe_publishable_key,omitempty"` // for frontend checkout
	StripePriceSingle    string `json:"stripe_price_single,omitempty"`    // Stripe price ID for single plan
	StripePriceTeam      string `json:"stripe_price_team,omitempty"`      // Stripe price ID for team plan
}

// ServerConfig defines the hub's listener settings.
type ServerConfig struct {
	Addr            string   `json:"addr"`                       // e.g. ":8080"
	TLSCert         string   `json:"tls_cert,omitempty"`
	TLSKey          string   `json:"tls_key,omitempty"`
	UIStaticDir     string   `json:"ui_static_dir,omitempty"`    // path to built UI files
	AllowedOrigins  []string `json:"allowed_origins,omitempty"`  // CORS origins; default ["*"]
	MaxBodyBytes    int64    `json:"max_body_bytes,omitempty"`   // max request body size; default 1MB
	FileStoragePath string   `json:"file_storage_path,omitempty"` // path for uploaded files; default "./amurg-files"
	MaxFileBytes    int64    `json:"max_file_bytes,omitempty"`    // max file size; default 10MB
	WhisperURL      string   `json:"whisper_url,omitempty"`       // upstream Whisper WebSocket URL to proxy at /asr
}

// AuthConfig defines authentication settings.
type AuthConfig struct {
	Provider               string              `json:"provider,omitempty"`                // "builtin" (default) or "clerk"
	ClerkIssuer            string              `json:"clerk_issuer,omitempty"`            // e.g. "https://foo.clerk.accounts.dev"
	ClerkSecretKey         string              `json:"clerk_secret_key,omitempty"`
	JWTSecret              string              `json:"jwt_secret"`
	JWTExpiry              Duration            `json:"jwt_expiry,omitempty"`
	RuntimeTokens          []RuntimeTokenEntry `json:"runtime_tokens"`
	RuntimeTokenSecret     string              `json:"runtime_token_secret,omitempty"`     // HMAC secret for time-limited tokens
	RuntimeTokenLifetime   Duration            `json:"runtime_token_lifetime,omitempty"`   // lifetime for generated tokens (default 1h)
	InitialAdmin           *InitialAdmin       `json:"initial_admin,omitempty"`
	DefaultEndpointAccess  string              `json:"default_endpoint_access,omitempty"` // "all" (default) or "none"
}

// RuntimeTokenEntry maps a runtime ID to its auth token.
type RuntimeTokenEntry struct {
	RuntimeID string `json:"runtime_id"`
	Token     string `json:"token"`
	Name      string `json:"name,omitempty"`
}

// InitialAdmin is used to bootstrap the first admin user.
type InitialAdmin struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// StorageConfig defines database settings.
type StorageConfig struct {
	Driver         string   `json:"driver"`                    // "sqlite" (default)
	DSN            string   `json:"dsn"`                       // e.g. "amurg.db" or ":memory:"
	Retention      Duration `json:"retention,omitempty"`       // transcript retention
	AuditRetention Duration `json:"audit_retention,omitempty"` // audit event retention; defaults to Retention
}

// SessionConfig defines session behavior.
type SessionConfig struct {
	MaxPerUser          int                 `json:"max_per_user,omitempty"`
	IdleTimeout         Duration            `json:"idle_timeout,omitempty"`
	TurnBased           bool                `json:"turn_based,omitempty"`              // enforce turn-based globally
	ReplayBuffer        int                 `json:"replay_buffer,omitempty"`           // messages to buffer for reconnect
	ProfileIdleTimeouts map[string]Duration `json:"profile_idle_timeouts,omitempty"`   // per-profile idle timeout overrides; "0" disables
	MaxMessageBytes     int64               `json:"max_message_bytes,omitempty"`       // max WebSocket message from client; default 64KB
}

// LoggingConfig defines logging settings.
type LoggingConfig struct {
	Level  string `json:"level,omitempty"`
	Format string `json:"format,omitempty"` // "json" or "text"
}

// RateLimitConfig defines rate limiting settings.
type RateLimitConfig struct {
	RequestsPerSecond float64 `json:"requests_per_second,omitempty"` // default 10
	Burst             int     `json:"burst,omitempty"`               // default 20
}

// Duration is a JSON-friendly time.Duration.
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch val := v.(type) {
	case string:
		dur, err := time.ParseDuration(val)
		if err != nil {
			return err
		}
		d.Duration = dur
	case float64:
		d.Duration = time.Duration(val) * time.Second
	default:
		return fmt.Errorf("invalid duration: %v", v)
	}
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

// Load reads and validates a config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	cfg.applyDefaults()
	return &cfg, nil
}

func (c *Config) validate() error {
	if c.Server.Addr == "" {
		return fmt.Errorf("server.addr is required")
	}
	// JWTSecret is only required for builtin auth provider.
	if (c.Auth.Provider == "" || c.Auth.Provider == "builtin") && c.Auth.JWTSecret == "" {
		return fmt.Errorf("auth.jwt_secret is required")
	}
	if c.Auth.JWTSecret != "" && len(c.Auth.JWTSecret) < 32 {
		return fmt.Errorf("auth.jwt_secret must be at least 32 characters")
	}
	if knownWeakSecrets[c.Auth.JWTSecret] {
		return fmt.Errorf("auth.jwt_secret is a well-known weak secret — generate a new one")
	}
	if c.Auth.Provider == "clerk" && c.Auth.ClerkIssuer == "" {
		return fmt.Errorf("auth.clerk_issuer is required when provider is clerk")
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.Auth.JWTExpiry.Duration == 0 {
		c.Auth.JWTExpiry.Duration = 24 * time.Hour
	}
	if c.Storage.Driver == "" {
		c.Storage.Driver = "sqlite"
	}
	if c.Storage.DSN == "" {
		c.Storage.DSN = "amurg.db"
	}
	if c.Storage.Retention.Duration == 0 {
		c.Storage.Retention.Duration = 30 * 24 * time.Hour // 30 days
	}
	if c.Storage.AuditRetention.Duration == 0 {
		c.Storage.AuditRetention.Duration = c.Storage.Retention.Duration
	}
	if c.Session.MaxPerUser == 0 {
		c.Session.MaxPerUser = 20
	}
	if c.Session.IdleTimeout.Duration == 0 {
		c.Session.IdleTimeout.Duration = 30 * time.Minute
	}
	if c.Session.ReplayBuffer == 0 {
		c.Session.ReplayBuffer = 100
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.Format == "" {
		c.Logging.Format = "json"
	}
	if c.Auth.DefaultEndpointAccess == "" {
		c.Auth.DefaultEndpointAccess = "all"
	}
	if c.Auth.RuntimeTokenLifetime.Duration == 0 {
		c.Auth.RuntimeTokenLifetime.Duration = 1 * time.Hour
	}
	if c.RateLimit.RequestsPerSecond == 0 {
		c.RateLimit.RequestsPerSecond = 10
	}
	if c.RateLimit.Burst == 0 {
		c.RateLimit.Burst = 20
	}
	if c.Server.MaxBodyBytes == 0 {
		c.Server.MaxBodyBytes = 1024 * 1024 // 1MB
	}
	if c.Session.MaxMessageBytes == 0 {
		c.Session.MaxMessageBytes = 64 * 1024 // 64KB
	}
	if c.Server.FileStoragePath == "" {
		c.Server.FileStoragePath = "./amurg-files"
	}
	if c.Server.MaxFileBytes == 0 {
		c.Server.MaxFileBytes = 10 * 1024 * 1024 // 10MB
	}
}
