package web

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// bearerAuth is a middleware that requires the configured bearer token on
// every wrapped request. The token can be supplied either as
// "Authorization: Bearer <token>" or, for endpoints that need to accept
// EventSource (which can't set headers), as the "?token=<token>" query
// parameter. Comparison is constant-time via subtle.ConstantTimeCompare.
//
// If the configured token is empty the middleware rejects every request —
// callers must guard against the empty-token-on-non-loopback case at server
// startup; this middleware is the last line of defence.
func bearerAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			// Defensive: should be caught at server startup, but never serve
			// auth-required endpoints with no token.
			http.Error(w, "server has no auth token configured", http.StatusServiceUnavailable)
			return
		}

		supplied := extractToken(r)
		if subtle.ConstantTimeCompare([]byte(supplied), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="fleet"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// extractToken pulls the bearer token from either the Authorization header
// or the ?token= query parameter. Header takes precedence.
func extractToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(h, prefix) {
			return strings.TrimSpace(h[len(prefix):])
		}
	}
	return r.URL.Query().Get("token")
}

// isLoopbackAddr reports whether addr listens only on loopback. Used at
// startup to decide whether an empty token is acceptable.
//
// Accepts "127.0.0.1:PORT", "[::1]:PORT", and "localhost:PORT". Anything
// else — including "0.0.0.0:PORT" and the empty/missing host case — is
// treated as non-loopback (the safer default).
func isLoopbackAddr(addr string) bool {
	// Strip port. net.SplitHostPort tolerates IPv6 brackets.
	host := addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
	}
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}
