package web

import (
	"net/http"
	"runtime/debug"

	"github.com/brizzai/fleet/internal/debuglog"
)

// recoveryMiddleware catches panics in per-request handler goroutines, logs
// the panic + stack to debuglog, and returns a 500 to the client. Without
// this, a single panic in a handler would crash the entire TUI process.
func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				debuglog.Logger.Error("web: handler panic",
					"method", r.Method,
					"path", r.URL.Path,
					"panic", rec,
					"stack", string(debug.Stack()),
				)
				// Best-effort 500 — if headers were already written this is a
				// no-op (Go's ResponseWriter silently drops late WriteHeader
				// calls and we have no way to recover the connection).
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
