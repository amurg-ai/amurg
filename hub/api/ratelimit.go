package api

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"
)

// rateLimiter implements a per-user token bucket rate limiter.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64 // tokens per second
	burst   int     // max tokens
}

type bucket struct {
	tokens     float64
	lastCheck  time.Time
	lastAccess time.Time
}

func newRateLimiter(requestsPerSecond float64, burst int) *rateLimiter {
	return &rateLimiter{
		buckets: make(map[string]*bucket),
		rate:    requestsPerSecond,
		burst:   burst,
	}
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok {
		b = &bucket{
			tokens:    float64(rl.burst),
			lastCheck: now,
		}
		rl.buckets[key] = b
	}

	// Refill tokens based on elapsed time.
	elapsed := now.Sub(b.lastCheck).Seconds()
	b.tokens += elapsed * rl.rate
	if b.tokens > float64(rl.burst) {
		b.tokens = float64(rl.burst)
	}
	b.lastCheck = now
	b.lastAccess = now

	if b.tokens < 1 {
		return false
	}

	b.tokens--
	return true
}

// cleanup removes buckets that haven't been accessed for maxAge.
func (rl *rateLimiter) cleanup(maxAge time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for key, b := range rl.buckets {
		if b.lastAccess.Before(cutoff) {
			delete(rl.buckets, key)
		}
	}
}

// StartCleanup periodically removes stale rate limit buckets.
func (rl *rateLimiter) StartCleanup(ctx context.Context, interval, maxAge time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rl.cleanup(maxAge)
			}
		}
	}()
}

// loginIPRateLimitMiddleware returns HTTP middleware that rate-limits by remote IP.
func loginIPRateLimitMiddleware(rl *rateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Use RemoteAddr which is already set to the real IP by chi's RealIP middleware.
			// Strip the port to rate-limit by IP only.
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				ip = r.RemoteAddr // fallback if no port
			}
			if !rl.allow(ip) {
				w.Header().Set("Retry-After", "1")
				writeError(w, http.StatusTooManyRequests, "too many login attempts")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// rateLimitMiddleware returns HTTP middleware that rate-limits by user ID.
func rateLimitMiddleware(rl *rateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity := getIdentityFromContext(r.Context())
			if identity == nil {
				next.ServeHTTP(w, r)
				return
			}

			if !rl.allow(identity.UserID) {
				w.Header().Set("Retry-After", "1")
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
