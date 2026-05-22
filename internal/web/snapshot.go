package web

import "time"

// SessionSnapshot is the JSON-friendly DTO returned by GET /api/sessions and
// embedded in SSE event payloads. It mirrors the read-only subset of
// session.Session that the mobile UI cares about — never include internal
// fields (mutexes, tmux handles, etc.).
//
// The DTO is owned by the web package on purpose: keeping it here avoids
// pulling internal/ui types into HTTP handlers, and lets the schema evolve
// independently of the on-disk SQLite row.
type SessionSnapshot struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	ProjectPath     string    `json:"projectPath"`
	Status          string    `json:"status"`
	WorkspaceName   string    `json:"workspaceName,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	LastAccessedAt  time.Time `json:"lastAccessedAt,omitempty"`
	ClaudeSessionID string    `json:"claudeSessionId,omitempty"`
	FirstPrompt     string    `json:"firstPrompt,omitempty"`
	PromptCount     int       `json:"promptCount"`
	IsAlive         bool      `json:"isAlive"`
}

// SessionEvent is emitted on the SSE stream when something about a session
// changes — status, creation, deletion, or a generic refresh. The web client
// uses Kind to decide whether to refetch the full list or just update one row.
type SessionEvent struct {
	Kind      string           `json:"kind"`                // "created", "updated", "deleted", "refresh"
	SessionID string           `json:"sessionId,omitempty"` // empty for "refresh"
	Snapshot  *SessionSnapshot `json:"snapshot,omitempty"`  // populated for created/updated
	Timestamp time.Time        `json:"timestamp"`
}
