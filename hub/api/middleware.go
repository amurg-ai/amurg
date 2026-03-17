package api

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

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

		// Check if the token has been revoked (logout).
		if s.tokenBlocklist != nil {
			if jti := auth.ExtractJTI(tokenStr); jti != "" && s.tokenBlocklist.isBlocked(jti) {
				writeError(w, http.StatusUnauthorized, "token has been revoked")
				return
			}
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
				// Keep Clerk-backed identities stable across HTTP and WebSocket
				// code paths by using the provider subject as the local user ID.
				ID:         identity.UserID,
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

// adminOnlyMiddleware rejects requests from non-admin users with 403 Forbidden.
// Must be placed after authMiddleware so the identity is available in context.
func adminOnlyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := getIdentityFromContext(r.Context())
		if identity == nil || identity.Role != "admin" {
			writeError(w, http.StatusForbidden, "admin access required")
			return
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

// makeTrustedProxyMiddleware returns middleware that sets r.RemoteAddr to the
// client IP from X-Forwarded-For only when the direct connection comes from a
// trusted proxy CIDR. If no trusted proxies are configured, X-Forwarded-For is
// never trusted and the direct connection IP is always used.
func makeTrustedProxyMiddleware(trustedCIDRs []string) func(http.Handler) http.Handler {
	var nets []*net.IPNet
	for _, cidr := range trustedCIDRs {
		// Allow bare IPs by appending /32 or /128.
		if !strings.Contains(cidr, "/") {
			if strings.Contains(cidr, ":") {
				cidr += "/128"
			} else {
				cidr += "/32"
			}
		}
		if _, ipNet, err := net.ParseCIDR(cidr); err == nil {
			nets = append(nets, ipNet)
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(nets) == 0 {
				// No trusted proxies — strip any X-Forwarded-For and use direct IP.
				r.Header.Del("X-Forwarded-For")
				r.Header.Del("X-Real-Ip")
				next.ServeHTTP(w, r)
				return
			}

			directIP, _, _ := net.SplitHostPort(r.RemoteAddr)
			ip := net.ParseIP(directIP)

			trusted := false
			if ip != nil {
				for _, n := range nets {
					if n.Contains(ip) {
						trusted = true
						break
					}
				}
			}

			if !trusted {
				// Direct connection is not from a trusted proxy — ignore forwarded headers.
				r.Header.Del("X-Forwarded-For")
				r.Header.Del("X-Real-Ip")
			}
			// When trusted, chi's RealIP middleware will parse X-Forwarded-For normally.
			next.ServeHTTP(w, r)
		})
	}
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
