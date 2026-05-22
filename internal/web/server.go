// Package web implements the embedded mobile-friendly HTTP server for fleet.
//
// The server runs inside the same process as the TUI when web.enabled=true.
// All state mutations route through the TUI's Bubble Tea event loop via
// tea.Program.Send so handlers don't race the worker goroutine on Home state.
// Read-only endpoints take a snapshot through the SessionSource interface,
// which is implemented by *ui.Home — keeping internal/web independent of
// internal/ui at compile time.
//
// Process model: one stdlib http.Server goroutine, plus per-request handler
// goroutines. Shutdown is driven from cmd/fleet/main.go via a defer that
// runs before the process exits (including on the p.Run() error path).
package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/brizzai/fleet/internal/debuglog"
)

// Mutation describes a state-changing request the web layer wants the TUI
// event loop to apply. The TUI's dispatcher converts each variant into the
// appropriate sessionXxxMsg / direct call and sends the result back on Reply.
//
// The Reply channel must be buffered (capacity 1) so a slow handler doesn't
// block the Update loop. The web side always reads with a timeout.
type Mutation struct {
	Kind    MutationKind
	ID      string         // session ID for per-session mutations
	Payload map[string]any // type-specific extras (keys, destroyWorkspace, etc.)
	Reply   chan error
}

// MutationKind enumerates the supported web-driven mutations.
type MutationKind string

const (
	MutationApprove  MutationKind = "approve"
	MutationRestart  MutationKind = "restart"
	MutationDelete   MutationKind = "delete"
	MutationSendKeys MutationKind = "sendKeys"
	MutationCreate   MutationKind = "create"
)

// SessionSource is the subset of TUI state the web layer reads. Implemented
// by *ui.Home. Snapshot methods must be safe to call from any goroutine —
// implementations are responsible for taking whatever lock guards the
// underlying state.
type SessionSource interface {
	SessionsSnapshot() []SessionSnapshot
	PaneSnapshot(id string) (string, error)
}

// MutationDispatcher delivers a Mutation to the TUI event loop. The
// implementation typically wraps tea.Program.Send.
//
// Implementations may block — the web handlers always read Mutation.Reply
// with a timeout to bound how long they wait.
type MutationDispatcher interface {
	Dispatch(m Mutation)
}

// Deps wires the server up to the TUI. All fields are required.
type Deps struct {
	Source     SessionSource
	Dispatcher MutationDispatcher

	// Addr is the listen address (e.g. "0.0.0.0:8765"). Loopback ("127.0.0.1",
	// "::1", "localhost") lets Token be empty for local-only access; any
	// other host requires a non-empty Token.
	Addr string

	// Token is the bearer token required on /api/* requests. If empty and
	// Addr is loopback, auth is disabled. If empty and Addr is non-loopback,
	// NewServer returns an error.
	Token string

	// MutationTimeout bounds how long a handler waits for the TUI to ack a
	// mutation reply. Defaults to 3s when zero.
	MutationTimeout time.Duration

	// SaveTokenBack is called when the server generated a fresh token
	// (because the caller passed Token=""). The implementation should
	// persist the token to the config file. Optional — may be nil, in which
	// case the generated token is logged but not persisted.
	SaveTokenBack func(token string) error
}

// Server is the long-lived HTTP server. Construct with NewServer, run with
// Start, drain with Shutdown.
type Server struct {
	deps  Deps
	hub   *eventHub
	http  *http.Server
	token string // resolved token (may differ from deps.Token if auto-generated)

	startOnce sync.Once
	startErr  error
}

// NewServer validates the configuration and constructs a Server.
//
// Returns an error when:
//   - deps.Source or deps.Dispatcher is nil
//   - deps.Addr is empty
//   - deps.Addr is non-loopback and deps.Token is empty (no auth would be applied)
//
// When deps.Token is empty AND deps.Addr is loopback, auth is disabled and
// the empty token is preserved.
//
// When deps.Token is empty AND deps.Addr is non-loopback this returns an
// error — the caller (cmd/fleet/main.go) is responsible for auto-generating
// a token before construction if it wants the auto-generation behaviour.
func NewServer(deps Deps) (*Server, error) {
	if deps.Source == nil {
		return nil, errors.New("web: Deps.Source is required")
	}
	if deps.Dispatcher == nil {
		return nil, errors.New("web: Deps.Dispatcher is required")
	}
	if deps.Addr == "" {
		return nil, errors.New("web: Deps.Addr is required")
	}
	if deps.Token == "" && !isLoopbackAddr(deps.Addr) {
		return nil, fmt.Errorf("web: refusing to start on non-loopback addr %q without an auth token", deps.Addr)
	}
	if deps.MutationTimeout == 0 {
		deps.MutationTimeout = 3 * time.Second
	}

	s := &Server{
		deps:  deps,
		hub:   newEventHub(),
		token: deps.Token,
	}
	s.http = &http.Server{
		Addr:              deps.Addr,
		Handler:           s.buildHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		// SSE handlers need unbounded write deadlines; the per-request
		// handler manages its own keepalive cadence.
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}
	return s, nil
}

// GenerateToken returns a fresh 32-byte hex-encoded random token. Exposed so
// cmd/fleet/main.go can mint a token, persist it, and pass it into Deps.
func GenerateToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("web: token generation: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// Token returns the token the server is using to authenticate requests.
// Empty when the server is running unauthenticated on a loopback address.
func (s *Server) Token() string {
	return s.token
}

// Start begins listening. Blocks until the server stops (Shutdown was
// called, or the listener errored). Safe to call exactly once — subsequent
// calls return the original startErr.
//
// Callers should run this in a goroutine. Panics inside the goroutine are
// recovered here so a listener-side panic doesn't take the TUI down.
func (s *Server) Start() error {
	s.startOnce.Do(func() {
		defer func() {
			if rec := recover(); rec != nil {
				debuglog.Logger.Error("web: server goroutine panic",
					"panic", rec,
					"stack", string(debug.Stack()),
				)
				s.startErr = fmt.Errorf("web: server panic: %v", rec)
			}
		}()

		ln, err := net.Listen("tcp", s.http.Addr)
		if err != nil {
			s.startErr = fmt.Errorf("web: listen %s: %w", s.http.Addr, err)
			return
		}
		debuglog.Logger.Info("web: server listening", "addr", s.http.Addr, "auth", s.token != "")

		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.startErr = fmt.Errorf("web: serve: %w", err)
		}
	})
	return s.startErr
}

// Shutdown gracefully drains in-flight requests and stops the listener.
// Honours the context deadline; pass a 5s timeout for typical TUI exit.
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}

// Publish broadcasts a session event to all SSE subscribers. Called from the
// TUI worker / Update path; non-blocking per subscriber (drops on full).
func (s *Server) Publish(evt SessionEvent) {
	if s == nil || s.hub == nil {
		return
	}
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now()
	}
	s.hub.publish(evt)
}

// buildHandler wires up the router. Static assets are unauthenticated so the
// SPA shell loads in a browser without manual headers; all /api/* paths are
// behind bearerAuth.
func (s *Server) buildHandler() http.Handler {
	mux := http.NewServeMux()

	api := &apiHandlers{
		source:          s.deps.Source,
		dispatcher:      s.deps.Dispatcher,
		hub:             s.hub,
		mutationTimeout: s.deps.MutationTimeout,
	}

	authed := func(h http.HandlerFunc) http.Handler {
		return bearerAuth(s.token, http.HandlerFunc(h))
	}

	mux.Handle("GET /api/sessions", authed(api.listSessions))
	mux.Handle("POST /api/sessions", authed(api.createSession))
	mux.Handle("GET /api/sessions/{id}/pane", authed(api.getPane))
	mux.Handle("POST /api/sessions/{id}/sendkeys", authed(api.sendKeys))
	mux.Handle("POST /api/sessions/{id}/approve", authed(api.approve))
	mux.Handle("POST /api/sessions/{id}/restart", authed(api.restart))
	mux.Handle("POST /api/sessions/{id}/delete", authed(api.deleteSession))
	// SSE: EventSource can't set headers so we accept ?token=… via the same
	// bearerAuth helper (it falls back to the query param).
	mux.Handle("GET /api/events", authed(api.events))

	// Static SPA. Unauthenticated; the JS then prompts for a token and uses
	// it on /api/* calls.
	mux.Handle("GET /", http.FileServerFS(staticFS()))

	return recoveryMiddleware(mux)
}
