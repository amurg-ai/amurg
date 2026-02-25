package auth

import (
	"fmt"
	"time"

	"github.com/amurg-ai/amurg/hub/config"
	"github.com/amurg-ai/amurg/hub/store"
)

// NewProvider creates an auth Provider based on configuration.
func NewProvider(cfg config.AuthConfig, s store.Store) (Provider, error) {
	switch cfg.Provider {
	case "clerk":
		clerk, err := NewClerkProvider(cfg.ClerkIssuer)
		if err != nil {
			return nil, err
		}
		// Wrap Clerk with a Service to provide runtime token validation.
		svc := NewService(s, cfg)
		return &clerkWithRuntime{ClerkProvider: clerk, svc: svc}, nil
	case "builtin", "":
		return NewService(s, cfg), nil
	default:
		return nil, fmt.Errorf("unknown auth provider: %q", cfg.Provider)
	}
}

// clerkWithRuntime combines ClerkProvider (for user JWT validation) with
// Service (for runtime token validation).
type clerkWithRuntime struct {
	*ClerkProvider
	svc *Service
}

func (c *clerkWithRuntime) ValidateRuntimeToken(runtimeID, token string) bool {
	return c.svc.ValidateRuntimeToken(runtimeID, token)
}

func (c *clerkWithRuntime) ValidateTimeLimitedToken(token string) (string, error) {
	return c.svc.ValidateTimeLimitedToken(token)
}

func (c *clerkWithRuntime) GenerateRuntimeToken(runtimeID string) string {
	return c.svc.GenerateRuntimeToken(runtimeID)
}

func (c *clerkWithRuntime) RuntimeTokenSecret() string {
	return c.svc.RuntimeTokenSecret()
}

func (c *clerkWithRuntime) RuntimeTokenLifetime() time.Duration {
	return c.svc.RuntimeTokenLifetime()
}
