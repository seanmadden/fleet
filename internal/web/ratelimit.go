package web

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// rateLimiter is a tiny per-IP token-bucket limiter applied to mutation
// (POST) endpoints. GETs and SSE are not limited — they're cheap and the
// SSE subscriber cap (see events.go) already bounds the long-lived
// resource cost there. POSTs route through the TUI's tea.Program.Send
// channel, which is unbuffered; a misbehaving client could otherwise
// stall every web handler in flight while the Update loop drains.
//
// Implementation is intentionally minimal — no x/time/rate dependency,
// no LRU eviction. The map grows with unique client IPs but in the
// personal-tool deployment that's effectively a handful of addresses.
// If a deployment ever proves otherwise we can swap in golang.org/x/time/rate.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	rps     float64 // tokens per second
	burst   float64 // bucket capacity (also initial tokens)
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(rps, burst float64) *rateLimiter {
	return &rateLimiter{
		buckets: make(map[string]*tokenBucket),
		rps:     rps,
		burst:   burst,
	}
}

// allow returns true if the given key (typically a client IP) has a
// token available, deducting one if so. Token replenishment is lazy —
// the bucket's last-refill time is updated on every call.
func (rl *rateLimiter) allow(key string) bool {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	b, ok := rl.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: rl.burst, last: now}
		rl.buckets[key] = b
	}
	// Refill since last check.
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * rl.rps
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// clientIP extracts the request's remote IP, stripping the port. Honors
// the X-Forwarded-For header's first entry when present — there's no
// trusted reverse proxy in the personal-tool deployment so this is a
// best-effort attribution, not a security boundary.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first comma-separated entry.
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return trimSpace(xff[:i])
			}
		}
		return trimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// trimSpace is strings.TrimSpace inlined to avoid pulling the import in
// this small file when the only other use is the X-Forwarded-For split.
func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

// rateLimit wraps h with per-IP rate limiting. Returns 429 when the
// caller exceeds the configured rps/burst.
func rateLimit(rl *rateLimiter, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		h.ServeHTTP(w, r)
	})
}
