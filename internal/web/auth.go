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
// The configured token is always non-empty in production — NewServer
// rejects empty-token construction. The empty-token guard here is a
// defense-in-depth check for tests (or future refactors) that build a
// Server struct directly.
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
