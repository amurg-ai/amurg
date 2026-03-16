package api

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"
)

// tokenBlocklist tracks revoked JWT IDs (jti) for logout support.
type tokenBlocklist struct {
	mu      sync.RWMutex
	entries map[string]time.Time // jti -> expiry
}

func newTokenBlocklist() *tokenBlocklist {
	return &tokenBlocklist{entries: make(map[string]time.Time)}
}

func (b *tokenBlocklist) add(jti string, expiry time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries[jti] = expiry
}

func (b *tokenBlocklist) isBlocked(jti string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.entries[jti]
	return ok
}

func (b *tokenBlocklist) cleanup() {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	for jti, expiry := range b.entries {
		if now.After(expiry) {
			delete(b.entries, jti)
		}
	}
}

// loginLockout tracks failed login attempts per account.
type loginLockout struct {
	mu       sync.Mutex
	attempts map[string]*lockoutEntry // username -> entry
	maxFails int
	lockDur  time.Duration
}

type lockoutEntry struct {
	failures int
	lockedAt time.Time
}

func newLoginLockout(maxFails int, lockDuration time.Duration) *loginLockout {
	return &loginLockout{
		attempts: make(map[string]*lockoutEntry),
		maxFails: maxFails,
		lockDur:  lockDuration,
	}
}

func (l *loginLockout) isLocked(username string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.attempts[username]
	if !ok {
		return false
	}
	if !e.lockedAt.IsZero() && time.Since(e.lockedAt) < l.lockDur {
		return true
	}
	if !e.lockedAt.IsZero() && time.Since(e.lockedAt) >= l.lockDur {
		// Lockout expired — reset.
		delete(l.attempts, username)
	}
	return false
}

func (l *loginLockout) recordFailure(username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.attempts[username]
	if !ok {
		e = &lockoutEntry{}
		l.attempts[username] = e
	}
	e.failures++
	if e.failures >= l.maxFails {
		e.lockedAt = time.Now()
	}
}

func (l *loginLockout) recordSuccess(username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, username)
}

func (l *loginLockout) cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-l.lockDur)
	for user, e := range l.attempts {
		if !e.lockedAt.IsZero() && e.lockedAt.Before(cutoff) {
			delete(l.attempts, user)
		}
	}
}

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
