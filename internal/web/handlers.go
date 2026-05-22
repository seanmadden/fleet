package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/brizzai/fleet/internal/debuglog"
)

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
		// can drop the cached pane.
		http.Error(w, err.Error(), http.StatusNotFound)
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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
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
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
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
func (a *apiHandlers) createSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title         string `json:"title"`
		Path          string `json:"path"`
		WorkspaceName string `json:"workspaceName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if body.Path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	if body.Title == "" {
		body.Title = body.Path
	}
	a.dispatchAndWait(w, Mutation{
		Kind: MutationCreate,
		Payload: map[string]any{
			"title":         body.Title,
			"path":          body.Path,
			"workspaceName": body.WorkspaceName,
		},
	})
}

// dispatchAndWait sends the mutation to the TUI loop and waits up to
// MutationTimeout for the reply. The reply channel is buffered (cap 1) so
// the TUI side never blocks if the HTTP handler has already given up.
//
// On timeout we return 504 — the mutation may still complete on the TUI
// side, but the client should treat its operation as in-doubt and refetch.
func (a *apiHandlers) dispatchAndWait(w http.ResponseWriter, m Mutation) {
	m.Reply = make(chan error, 1)
	a.dispatcher.Dispatch(m)

	ctx, cancel := context.WithTimeout(context.Background(), a.mutationTimeout)
	defer cancel()

	select {
	case err := <-m.Reply:
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
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

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // nginx-style buffering disable
	w.WriteHeader(http.StatusOK)

	sub := a.hub.subscribe()
	defer a.hub.unsubscribe(sub)

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
