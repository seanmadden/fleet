package web

import (
	"sync"

	"github.com/brizzai/fleet/internal/debuglog"
)

const (
	// subscriberBuffer is the per-subscriber channel buffer. When a subscriber
	// can't keep up, the publisher drops the event and emits a "refresh" marker
	// (via the drop signaller) so the client can refetch from scratch instead
	// of getting a torn view.
	subscriberBuffer = 32

	// maxSubscribers caps the number of concurrent SSE clients. Each
	// subscriber pins a goroutine + buffered channel + open TCP socket;
	// without a cap an authenticated client could open thousands of
	// parallel `/api/events` streams and exhaust the process's FD/goroutine
	// budget. 50 is plenty for a personal-tool deployment (one phone, one
	// laptop, headroom for stale reconnects).
	maxSubscribers = 50
)

// eventHub fans SessionEvent values out to all live SSE subscribers.
//
// Sends to a slow subscriber are non-blocking (select+default). A drop sets
// the subscriber's pendingRefresh flag; the SSE handler emits a synthetic
// "refresh" event the next time it wakes up so the client knows to refetch.
type eventHub struct {
	mu          sync.Mutex
	subscribers map[*subscriber]struct{}
}

type subscriber struct {
	ch              chan SessionEvent
	pendingRefresh  bool // set when an event was dropped; surfaced on the next deliver
	pendingRefreshM sync.Mutex
}

func newEventHub() *eventHub {
	return &eventHub{
		subscribers: make(map[*subscriber]struct{}),
	}
}

// subscribe registers a new subscriber and returns its channel. The caller
// must call unsubscribe when the SSE request ends, otherwise the hub leaks
// memory for dead clients.
//
// Returns nil if the hub already has maxSubscribers active subscribers;
// the SSE handler turns that into a 503 so a runaway client can't
// exhaust process resources by opening unlimited concurrent streams.
func (h *eventHub) subscribe() *subscriber {
	s := &subscriber{ch: make(chan SessionEvent, subscriberBuffer)}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subscribers) >= maxSubscribers {
		return nil
	}
	h.subscribers[s] = struct{}{}
	return s
}

func (h *eventHub) unsubscribe(s *subscriber) {
	if s == nil {
		return
	}
	h.mu.Lock()
	_, present := h.subscribers[s]
	delete(h.subscribers, s)
	h.mu.Unlock()
	if !present {
		// Already removed (or never inserted) — closing the channel a
		// second time would panic.
		return
	}
	// Close the channel so the SSE handler's range loop exits cleanly.
	close(s.ch)
}

// publish broadcasts an event to all subscribers. Non-blocking — if a
// subscriber's buffer is full, the event is dropped and the subscriber is
// flagged for a refresh marker on its next delivery.
func (h *eventHub) publish(evt SessionEvent) {
	h.mu.Lock()
	subs := make([]*subscriber, 0, len(h.subscribers))
	for s := range h.subscribers {
		subs = append(subs, s)
	}
	h.mu.Unlock()

	for _, s := range subs {
		select {
		case s.ch <- evt:
		default:
			s.pendingRefreshM.Lock()
			s.pendingRefresh = true
			s.pendingRefreshM.Unlock()
			debuglog.Logger.Warn("web: dropped SSE event (subscriber buffer full)", "kind", evt.Kind, "session", evt.SessionID)
		}
	}
}

// takePendingRefresh returns true if a previous publish dropped an event for
// this subscriber, and resets the flag.
func (s *subscriber) takePendingRefresh() bool {
	s.pendingRefreshM.Lock()
	defer s.pendingRefreshM.Unlock()
	v := s.pendingRefresh
	s.pendingRefresh = false
	return v
}
