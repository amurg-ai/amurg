package auth

import (
	"context"
	"time"

	"github.com/amurg-ai/amurg/hub/internal/store"
)

// Identity is the unified identity representation for all auth providers.
type Identity struct {
	UserID   string // Internal user ID (builtin) or external provider user ID
	Username string
	Role     string // "admin" or "user"
	OrgID    string // "default" for self-hosted, org_id for SaaS
}

// Provider validates bearer tokens and returns identities.
type Provider interface {
	ValidateToken(ctx context.Context, token string) (*Identity, error)
	Bootstrap(ctx context.Context) error
	Name() string
}

// LoginProvider is implemented by providers that support username/password login.
type LoginProvider interface {
	Login(ctx context.Context, username, password string) (string, error)
	Register(ctx context.Context, username, password, role string) (*store.User, error)
}

// RuntimeAuthProvider handles runtime token validation and generation.
type RuntimeAuthProvider interface {
	ValidateRuntimeToken(runtimeID, token string) bool
	ValidateTimeLimitedToken(token string) (string, error)
	GenerateRuntimeToken(runtimeID string) string
	RuntimeTokenSecret() string
	RuntimeTokenLifetime() time.Duration
}
