package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/brizzai/fleet/internal/debuglog"
	"github.com/brizzai/fleet/internal/pathx"
	"github.com/brizzai/fleet/internal/session"
)

// maxJSONBody caps the size of any POST body the API accepts. JSON
// payloads here are tiny (path + title + flags), so 64KB is generous
// while preventing an authenticated client from crashing the TUI by
// streaming gigabytes into json.Decode.
const maxJSONBody = 64 << 10

// decodeJSONBody enforces maxJSONBody before decoding. Always pair with
// MaxBytesReader so the underlying read returns ErrMaxBytes rather than
// allocating unbounded memory.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	return json.NewDecoder(r.Body).Decode(dst)
}

type apiHandlers struct {
	source          SessionSource
	dispatcher      MutationDispatcher
	hub             *eventHub
	mutationTimeout time.Duration
}

// listSessions: GET /api/sessions
func (a *apiHandlers) listSessions(w http.ResponseWriter, _ *http.Request) {
	snaps := a.source.SessionsSnapshot()
	writeJSON(w, http.StatusOK, snaps)
}

// getPane: GET /api/sessions/{id}/pane
//
// Returns plain text (ANSI-stripped). The mobile client renders it inside a
// <pre> so we don't bother with JSON wrapping — content-type text/plain
// avoids the browser interpreting any HTML-looking bytes.
func (a *apiHandlers) getPane(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	content, err := a.source.PaneSnapshot(id)
	if err != nil {
		// Not-found vs capture-failed distinction: SessionSource is expected
		// to return an error for missing IDs; surface as 404 so the client
		// can drop the cached pane. Log the full error server-side but
		// return a generic message so we don't leak internal session IDs
		// or tmux failure strings to the client.
		debuglog.Logger.Warn("web: pane snapshot failed", "id", id, "err", err)
		http.Error(w, "session not found or pane unavailable", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(content))
}

// sendKeys: POST /api/sessions/{id}/sendkeys
// Body: {"keys": "..."}
func (a *apiHandlers) sendKeys(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	var body struct {
		Keys string `json:"keys"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if body.Keys == "" {
		http.Error(w, "keys is required", http.StatusBadRequest)
		return
	}
	a.dispatchAndWait(w, Mutation{
		Kind:    MutationSendKeys,
		ID:      id,
		Payload: map[string]any{"keys": body.Keys},
	})
}

// approve: POST /api/sessions/{id}/approve
func (a *apiHandlers) approve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	a.dispatchAndWait(w, Mutation{Kind: MutationApprove, ID: id})
}

// restart: POST /api/sessions/{id}/restart
func (a *apiHandlers) restart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	a.dispatchAndWait(w, Mutation{Kind: MutationRestart, ID: id})
}

// deleteSession: POST /api/sessions/{id}/delete
// Body: {"destroyWorkspace": bool, "unpinRepo": bool}
func (a *apiHandlers) deleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	var body struct {
		DestroyWorkspace bool `json:"destroyWorkspace"`
		UnpinRepo        bool `json:"unpinRepo"`
	}
	// Empty body is OK — both flags default to false.
	if r.ContentLength > 0 {
		if err := decodeJSONBody(w, r, &body); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
	}
	a.dispatchAndWait(w, Mutation{
		Kind: MutationDelete,
		ID:   id,
		Payload: map[string]any{
			"destroyWorkspace": body.DestroyWorkspace,
			"unpinRepo":        body.UnpinRepo,
		},
	})
}

// createSession: POST /api/sessions
// Body: {"title": "...", "path": "...", "workspaceName": "..."}
//
// Path is expanded (tilde + absolute) and stat'd to ensure it points at an
// existing directory before we dispatch the mutation. This matches the
// `fleet add` CLI path so an authenticated tailnet peer can't open a
// Claude Code session at "/etc" or "../../somewhere" — the TUI's tmux
// `new-session -c <path>` happily honours anything the caller hands it.
//
// We deliberately do NOT enforce a path-under-$HOME rule for this MVP —
// legitimate uses like /opt/work/repo would break. The authenticated
// bearer token plus existence+is-dir validation is the bar.
func (a *apiHandlers) createSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title         string `json:"title"`
		Path          string `json:"path"`
		WorkspaceName string `json:"workspaceName"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if body.Path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	resolved := pathx.Expand(body.Path)
	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "path does not exist", http.StatusBadRequest)
			return
		}
		http.Error(w, "path stat failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !info.IsDir() {
		http.Error(w, "path is not a directory", http.StatusBadRequest)
		return
	}
	if body.Title == "" {
		// Match the CLI: derive a sensible title from the directory name
		// (e.g. "/Users/sean/IdeaProjects/fleet" → "fleet") rather than
		// echoing the full raw path.
		body.Title = session.TitleFromPath(resolved)
	}
	a.dispatchAndWait(w, Mutation{
		Kind: MutationCreate,
		Payload: map[string]any{
			"title":         body.Title,
			"path":          resolved,
			"workspaceName": body.WorkspaceName,
		},
	})
}

// dispatchAndWait sends the mutation to the TUI loop and waits up to
// MutationTimeout for the reply. The reply channel is buffered (cap 1) so
// the TUI side never blocks if the HTTP handler has already given up.
//
// Response semantics:
//   - 202 Accepted on a nil reply. The TUI accepted the mutation and
//     scheduled the actual work (tmux send-keys, session create, etc.)
//     on its event loop — but the work itself runs asynchronously. The
//     SSE refresh stream will tell the client when state actually
//     changes. The /ui/web.go reply(nil) sites currently fire BEFORE the
//     returned tea.Cmd runs, so this is honest accept-not-complete.
//   - 504 on timeout. The mutation may still complete server-side but
//     the client should refetch.
//   - 500 on a non-nil reply. The error string is logged server-side; the
//     client sees a generic message so we don't leak tmux output, session
//     IDs, or file paths to a (potentially shared) bearer-token holder.
func (a *apiHandlers) dispatchAndWait(w http.ResponseWriter, m Mutation) {
	m.Reply = make(chan error, 1)
	a.dispatcher.Dispatch(m)

	ctx, cancel := context.WithTimeout(context.Background(), a.mutationTimeout)
	defer cancel()

	select {
	case err := <-m.Reply:
		if err != nil {
			debuglog.Logger.Warn("web: mutation failed", "kind", m.Kind, "id", m.ID, "err", err)
			http.Error(w, "mutation failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	case <-ctx.Done():
		debuglog.Logger.Warn("web: mutation timed out", "kind", m.Kind, "id", m.ID)
		http.Error(w, "TUI event loop timed out", http.StatusGatewayTimeout)
	}
}

// events: GET /api/events (SSE)
//
// Streams SessionEvent JSON one per line. A keepalive comment (": ping\n\n")
// goes out every 25s so intermediate proxies don't drop idle connections.
// When a previous publish dropped an event for this subscriber, the handler
// emits a synthetic "refresh" event so the client knows to refetch.
func (a *apiHandlers) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Subscribe BEFORE writing headers — if the hub is at capacity, fail
	// fast with 503 so the client retries later. Once we've sent the 200
	// + SSE headers we can't take it back.
	sub := a.hub.subscribe()
	if sub == nil {
		http.Error(w, "too many SSE subscribers", http.StatusServiceUnavailable)
		return
	}
	defer a.hub.unsubscribe(sub)

	// SSE streams stay open indefinitely; the server-wide WriteTimeout
	// (set in NewServer) would otherwise terminate this connection mid-
	// stream. Clear the write deadline per-connection so REST endpoints
	// keep their slowloris protection while SSE can run for hours.
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		// Older response writers may not implement SetWriteDeadline.
		// That's fine — the deadline is a defense-in-depth measure; the
		// keepalive ticker below still keeps long-idle connections
		// reasonably fresh.
		debuglog.Logger.Debug("web: SSE clear write deadline failed", "err", err)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // nginx-style buffering disable
	w.WriteHeader(http.StatusOK)

	// Initial refresh marker so the client always paints a fresh list on
	// connect (and re-connect).
	if err := writeSSEEvent(w, SessionEvent{Kind: "refresh", Timestamp: time.Now()}); err != nil {
		return
	}
	flusher.Flush()

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		// Surface any dropped-event refresh marker before the next event so
		// the client knows there's a gap.
		if sub.takePendingRefresh() {
			if err := writeSSEEvent(w, SessionEvent{Kind: "refresh", Timestamp: time.Now()}); err != nil {
				return
			}
			flusher.Flush()
		}
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-sub.ch:
			if !ok {
				return
			}
			if err := writeSSEEvent(w, evt); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// writeSSEEvent serializes evt as a single SSE message (event: + data:).
// Returns the underlying write error so the handler can drop the client on
// failure.
func writeSSEEvent(w http.ResponseWriter, evt SessionEvent) error {
	payload, err := json.Marshal(evt)
	if err != nil {
		// JSON marshalling of our own DTO shouldn't fail; if it does, log
		// and skip rather than tearing down the SSE stream.
		debuglog.Logger.Error("web: marshal SSE event", "err", err)
		return nil
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Kind, payload); err != nil {
		return err
	}
	return nil
}

// writeJSON writes v as application/json with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		debuglog.Logger.Error("web: write JSON", "err", err)
	}
}
