// Package auth provides authentication and authorization for the hub.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/amurg-ai/amurg/hub/internal/config"
	"github.com/amurg-ai/amurg/hub/internal/store"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUserExists         = errors.New("user already exists")
	ErrUnauthorized       = errors.New("unauthorized")
)

// Claims represents the JWT token claims.
type Claims struct {
	UserID   string `json:"uid"`
	Username string `json:"usr"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// Service handles authentication operations.
// It implements Provider, LoginProvider, and RuntimeAuthProvider.
type Service struct {
	store               store.Store
	jwtSecret           []byte
	jwtExpiry           time.Duration
	runtimeTokens       map[string]string // runtime_id -> token (static, deprecated)
	runtimeTokenSecret  string            // HMAC secret for time-limited tokens
	runtimeTokenLifetime time.Duration
	initialAdmin        *config.InitialAdmin
}

// NewService creates a new auth service.
func NewService(s store.Store, cfg config.AuthConfig) *Service {
	tokens := make(map[string]string)
	for _, rt := range cfg.RuntimeTokens {
		tokens[rt.RuntimeID] = rt.Token
	}

	return &Service{
		store:                s,
		jwtSecret:            []byte(cfg.JWTSecret),
		jwtExpiry:            cfg.JWTExpiry.Duration,
		runtimeTokens:        tokens,
		runtimeTokenSecret:   cfg.RuntimeTokenSecret,
		runtimeTokenLifetime: cfg.RuntimeTokenLifetime.Duration,
		initialAdmin:         cfg.InitialAdmin,
	}
}

// RuntimeTokenSecret returns the HMAC secret for time-limited runtime tokens.
func (s *Service) RuntimeTokenSecret() string {
	return s.runtimeTokenSecret
}

// RuntimeTokenLifetime returns the lifetime for generated runtime tokens.
func (s *Service) RuntimeTokenLifetime() time.Duration {
	return s.runtimeTokenLifetime
}

// GenerateRuntimeToken creates a time-limited HMAC token for a runtime.
// Token format: {runtimeID}:{timestamp}:{hmac-sha256(runtimeID+timestamp, secret)}
func (s *Service) GenerateRuntimeToken(runtimeID string) string {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(s.runtimeTokenSecret))
	mac.Write([]byte(runtimeID + ":" + ts))
	sig := hex.EncodeToString(mac.Sum(nil))
	return runtimeID + ":" + ts + ":" + sig
}

// ValidateTimeLimitedToken verifies an HMAC runtime token and returns the runtime ID.
func (s *Service) ValidateTimeLimitedToken(token string) (string, error) {
	parts := strings.SplitN(token, ":", 3)
	if len(parts) != 3 {
		return "", errors.New("invalid token format")
	}

	runtimeID, tsStr, sig := parts[0], parts[1], parts[2]

	// Verify HMAC
	mac := hmac.New(sha256.New, []byte(s.runtimeTokenSecret))
	mac.Write([]byte(runtimeID + ":" + tsStr))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return "", errors.New("invalid token signature")
	}

	// Check timestamp
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return "", errors.New("invalid token timestamp")
	}

	age := time.Since(time.Unix(ts, 0))
	if age > s.runtimeTokenLifetime {
		return "", errors.New("token expired")
	}
	if age < -1*time.Minute {
		return "", errors.New("token from the future")
	}

	return runtimeID, nil
}

// Bootstrap creates the initial admin user if configured and no users exist.
// This implements the Provider interface.
func (s *Service) Bootstrap(ctx context.Context) error {
	return s.BootstrapAdmin(ctx, s.initialAdmin)
}

// BootstrapAdmin creates the initial admin user from the given config.
func (s *Service) BootstrapAdmin(ctx context.Context, admin *config.InitialAdmin) error {
	if admin == nil {
		return nil
	}

	existing, err := s.store.GetUser(ctx, "default", admin.Username)
	if err != nil {
		return fmt.Errorf("check existing user: %w", err)
	}
	if existing != nil {
		return nil // already bootstrapped
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(admin.Password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	user := &store.User{
		ID:           uuid.New().String(),
		OrgID:        "default",
		Username:     admin.Username,
		PasswordHash: string(hash),
		Role:         "admin",
		CreatedAt:    time.Now(),
	}

	return s.store.CreateUser(ctx, user)
}

// Name returns the provider name.
func (s *Service) Name() string { return "builtin" }

// Login authenticates a user and returns a JWT token.
func (s *Service) Login(ctx context.Context, username, password string) (string, error) {
	user, err := s.store.GetUser(ctx, "default", username)
	if err != nil {
		return "", fmt.Errorf("get user: %w", err)
	}
	if user == nil {
		return "", ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return "", ErrInvalidCredentials
	}

	return s.generateToken(user)
}

// Register creates a new user account.
func (s *Service) Register(ctx context.Context, username, password, role string) (*store.User, error) {
	existing, err := s.store.GetUser(ctx, "default", username)
	if err != nil {
		return nil, fmt.Errorf("check existing: %w", err)
	}
	if existing != nil {
		return nil, ErrUserExists
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	if role == "" {
		role = "user"
	}

	user := &store.User{
		ID:           uuid.New().String(),
		OrgID:        "default",
		Username:     username,
		PasswordHash: string(hash),
		Role:         role,
		CreatedAt:    time.Now(),
	}

	if err := s.store.CreateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	return user, nil
}

// ValidateToken validates a bearer token and returns an Identity.
// This implements the Provider interface.
func (s *Service) ValidateToken(ctx context.Context, tokenStr string) (*Identity, error) {
	claims, err := s.validateJWT(tokenStr)
	if err != nil {
		return nil, err
	}
	return &Identity{
		UserID:   claims.UserID,
		Username: claims.Username,
		Role:     claims.Role,
		OrgID:    "default",
	}, nil
}

// validateJWT validates a JWT token and returns the claims.
func (s *Service) validateJWT(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.jwtSecret, nil
	})
	if err != nil {
		return nil, ErrUnauthorized
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrUnauthorized
	}

	return claims, nil
}

// ValidateRuntimeToken checks if a runtime token is valid and returns the runtime ID.
func (s *Service) ValidateRuntimeToken(runtimeID, token string) bool {
	expected, ok := s.runtimeTokens[runtimeID]
	if !ok {
		return false
	}
	return hmac.Equal([]byte(expected), []byte(token))
}

func (s *Service) generateToken(user *store.User) (string, error) {
	claims := &Claims{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(s.jwtExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        uuid.New().String(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.jwtSecret)
}
