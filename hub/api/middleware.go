package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/amurg-ai/amurg/hub/auth"
	"github.com/amurg-ai/amurg/hub/store"
)

type contextKey string

const identityKey contextKey = "identity"

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "missing authorization header")
			return
		}

		tokenStr := authHeader[7:]
		identity, err := s.authProvider.ValidateToken(r.Context(), tokenStr)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}

		ctx := context.WithValue(r.Context(), identityKey, identity)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func getIdentityFromContext(ctx context.Context) *auth.Identity {
	identity, _ := ctx.Value(identityKey).(*auth.Identity)
	return identity
}

// ensureUserMiddleware auto-provisions a user and organization in the local
// database when an externally-authenticated user is seen for the first time.
// This is only active when the auth provider is "clerk".
func (s *Server) ensureUserMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := getIdentityFromContext(r.Context())
		if identity == nil {
			next.ServeHTTP(w, r)
			return
		}

		// Normalize empty org to "default" so all downstream handlers see a
		// consistent org ID (Clerk users without an active organization have
		// an empty org_id claim).
		if identity.OrgID == "" {
			identity.OrgID = "default"
			ctx := context.WithValue(r.Context(), identityKey, identity)
			r = r.WithContext(ctx)
		}

		ctx := r.Context()

		// Check if user already exists by their external ID (Clerk sub).
		existing, _ := s.store.GetUserByExternalID(ctx, identity.UserID)
		if existing == nil {
			orgID := identity.OrgID
			org, _ := s.store.GetOrganization(ctx, orgID)
			if org == nil {
				_ = s.store.CreateOrganization(ctx, &store.Organization{
					ID:        orgID,
					Name:      orgID,
					Plan:      "free",
					CreatedAt: time.Now(),
				})
			}

			// Create the user.
			_ = s.store.CreateUser(ctx, &store.User{
				ID:         uuid.New().String(),
				OrgID:      orgID,
				ExternalID: identity.UserID,
				Username:   identity.Username,
				Role:       identity.Role,
				CreatedAt:  time.Now(),
			})
		}

		next.ServeHTTP(w, r)
	})
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

func makeCORSMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	allowAll := len(allowedOrigins) == 1 && allowedOrigins[0] == "*"
	originSet := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		originSet[o] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else if origin != "" && originSet[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
