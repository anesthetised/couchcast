// Package ratelimit provides in-memory token-bucket limiting keyed by an
// arbitrary string (client IP, username, user id). It is per process,
// which matches the single-instance web server.
package ratelimit

import (
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Limiter keeps one token bucket per key and forgets idle keys.
type Limiter struct {
	limit rate.Limit
	burst int

	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
}

type bucket struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

// idleTTL is how long an unused bucket is kept before the janitor drops it.
const idleTTL = 10 * time.Minute

// PerHour is New for hourly budgets.
func PerHour(perHour float64, burst int) *Limiter { return New(perHour/60, burst) }

// New creates a limiter allowing perMinute events sustained with the given
// burst per key.
func New(perMinute float64, burst int) *Limiter {
	return &Limiter{
		limit:   rate.Limit(perMinute / 60),
		burst:   burst,
		buckets: make(map[string]*bucket),
		now:     time.Now,
	}
}

// Allow reports whether one more event is permitted for the key.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{lim: rate.NewLimiter(l.limit, l.burst)}
		l.buckets[key] = b
	}
	b.lastSeen = now

	return b.lim.AllowN(now, 1)
}

// Sweep drops buckets idle for longer than idleTTL. Run it periodically.
func (l *Limiter) Sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := l.now().Add(-idleTTL)
	for k, b := range l.buckets {
		if b.lastSeen.Before(cutoff) {
			delete(l.buckets, k)
		}
	}
}

// Run sweeps until the context is cancelled.
func (l *Limiter) Run(stop <-chan struct{}) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			l.Sweep()
		}
	}
}

// KeyFunc derives the limiter key from a request. Returning "" skips
// limiting for that request.
type KeyFunc func(r *http.Request) string

// Middleware rejects requests over the limit with 429.
func (l *Limiter) Middleware(key KeyFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if k := key(r); k != "" && !l.Allow(k) {
				w.Header().Set("Retry-After", "60")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "too many requests"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP returns the peer address, or the first X-Forwarded-For entry
// when trustProxy is set (only enable behind a proxy that overwrites it).
func ClientIP(trustProxy bool) KeyFunc {
	return func(r *http.Request) string {
		if trustProxy {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				if i := indexByte(xff, ','); i >= 0 {
					xff = xff[:i]
				}
				return trimSpace(xff)
			}
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			return r.RemoteAddr
		}
		return host
	}
}

func indexByte(s string, c byte) int {
	for i := range len(s) {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func trimSpace(s string) string {
	for len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	return s
}
