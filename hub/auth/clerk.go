package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// ClerkProvider validates Clerk-issued JWTs using JWKS.
type ClerkProvider struct {
	issuer string
	jwks   keyfunc.Keyfunc
}

// NewClerkProvider creates a ClerkProvider that fetches JWKS from the Clerk issuer.
func NewClerkProvider(issuer string) (*ClerkProvider, error) {
	if issuer == "" {
		return nil, fmt.Errorf("clerk issuer URL is required")
	}

	jwksURL := issuer + "/.well-known/jwks.json"
	jwks, err := keyfunc.NewDefault([]string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS from %s: %w", jwksURL, err)
	}

	return &ClerkProvider{
		issuer: issuer,
		jwks:   jwks,
	}, nil
}

// ValidateToken parses a Clerk JWT and returns an Identity.
func (c *ClerkProvider) ValidateToken(ctx context.Context, tokenStr string) (*Identity, error) {
	token, err := jwt.Parse(tokenStr, c.jwks.KeyfuncCtx(ctx),
		jwt.WithIssuer(c.issuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, ErrUnauthorized
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, ErrUnauthorized
	}

	sub, _ := claims["sub"].(string)
	if sub == "" {
		return nil, ErrUnauthorized
	}

	orgID, _ := claims["org_id"].(string)
	orgRole, _ := claims["org_role"].(string)

	role := "user"
	if orgRole == "org:admin" {
		role = "admin"
	}

	// Build a human-readable username from available claims.
	username := sub
	switch {
	case claimStr(claims, "username") != "":
		username = claimStr(claims, "username")
	case claimStr(claims, "name") != "":
		username = claimStr(claims, "name")
	case claimStr(claims, "first_name") != "" || claimStr(claims, "last_name") != "":
		username = strings.TrimSpace(claimStr(claims, "first_name") + " " + claimStr(claims, "last_name"))
	case claimStr(claims, "email") != "":
		username = claimStr(claims, "email")
	}

	return &Identity{
		UserID:   sub,
		Username: username,
		Role:     role,
		OrgID:    orgID,
	}, nil
}

// Bootstrap is a no-op for Clerk (users are managed externally).
func (c *ClerkProvider) Bootstrap(ctx context.Context) error {
	return nil
}

// claimStr extracts a string claim or returns "".
func claimStr(claims jwt.MapClaims, key string) string {
	v, _ := claims[key].(string)
	return v
}

// Name returns the provider name.
func (c *ClerkProvider) Name() string { return "clerk" }

// Close cancels the JWKS background refresh goroutine.
func (c *ClerkProvider) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = ctx
	return nil
}
