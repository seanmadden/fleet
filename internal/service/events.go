package service

// EventType identifies a service event kind.
type EventType int

const (
	EventSessionsChanged      EventType = iota // session list changed (add/delete)
	EventSessionStatusChanged                  // one or more session statuses updated
	EventGitInfoChanged                        // git/PR info refreshed
	EventError                                 // non-fatal error
)

// Event carries a service-layer notification.
type Event struct {
	Type    EventType
	Message string // optional human-readable detail (used for errors)
}

// Observer receives service events. Implementations must be safe for
// concurrent calls — the worker goroutine calls OnEvent directly.
type Observer interface {
	OnEvent(Event)
}
