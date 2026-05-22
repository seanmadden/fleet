package ui

import (
	"fmt"

	"github.com/brizzai/fleet/internal/debuglog"
	"github.com/brizzai/fleet/internal/session"
	"github.com/brizzai/fleet/internal/web"
	tea "github.com/charmbracelet/bubbletea"
)

// Web integration on Home.
//
// The web server (internal/web) reads snapshots through SessionSource and
// dispatches mutations through MutationDispatcher. *Home satisfies both —
// snapshots take h.workerMu for the duration of a minimal copy (matching
// the pattern in renderBody), and mutations are wrapped in webMutationMsg
// and shipped through tea.Program.Send so they run on the Update loop.
//
// Two minor characterisations to keep straight:
//   - session.GetTmuxSession does not take an internal lock — it returns the
//     stored pointer. We only call it under workerMu so the pointer doesn't
//     race the worker.
//   - tea.Program.Send is BLOCKING on an unbuffered channel. Handlers
//     mitigate via a 3s reply-channel timeout (see internal/web/handlers.go).

// webMutationMsg is the tea.Msg form of a web.Mutation. The Update loop
// switches on Kind and runs the appropriate handler with workerMu held
// where the underlying helper expects it.
type webMutationMsg struct {
	m web.Mutation
}

// webSessionPublisher is the publisher contract Home uses to notify the web
// server of session changes. Decouples internal/ui from internal/web —
// Home stores it as a Publisher interface, and cmd/fleet/main.go injects
// the real *web.Server via SetWebPublisher.
type webSessionPublisher interface {
	Publish(evt web.SessionEvent)
}

// SetProgram stores the running tea.Program reference. Must be called once,
// before web.NewServer is wired up — the web dispatcher needs Program.Send.
func (h *Home) SetProgram(p *tea.Program) {
	h.program = p
}

// SetWebPublisher registers the SSE event sink. Nil-safe; pass nil to disable.
func (h *Home) SetWebPublisher(pub webSessionPublisher) {
	h.webPublisher = pub
}

// Dispatch implements web.MutationDispatcher by wrapping the mutation in a
// tea.Msg and routing it through Program.Send. Send is BLOCKING on
// bubbletea's unbuffered channel — the web handler is responsible for
// timing out on Reply (which it does).
func (h *Home) Dispatch(m web.Mutation) {
	if h.program == nil {
		// No program wired (e.g. mid-test) — fail fast on Reply so the
		// handler doesn't hang.
		if m.Reply != nil {
			m.Reply <- fmt.Errorf("ui: tea program not set; cannot dispatch %s", m.Kind)
		}
		return
	}
	h.program.Send(webMutationMsg{m: m})
}

// SessionsSnapshot implements web.SessionSource. Acquires workerMu for the
// duration of a flat-DTO copy — does NOT deep-copy *session.Session
// (which holds a sync.RWMutex). Per-field reads use the session's
// thread-safe getters.
func (h *Home) SessionsSnapshot() []web.SessionSnapshot {
	h.workerMu.Lock()
	defer h.workerMu.Unlock()
	out := make([]web.SessionSnapshot, 0, len(h.sessions))
	for _, s := range h.sessions {
		out = append(out, sessionToSnapshot(s))
	}
	return out
}

// PaneSnapshot implements web.SessionSource. Looks up the session under the
// lock, copies the pointer, releases the lock, then calls CapturePane()
// outside the lock — capture-pane shells out to tmux and can take tens of
// milliseconds. The session pointer's tmuxSession field is only mutated by
// Session.Restart under s.mu, so reading it after dropping workerMu is
// safe: at worst we capture from a just-killed pane and CapturePane
// returns an error.
func (h *Home) PaneSnapshot(id string) (string, error) {
	h.workerMu.Lock()
	s, ok := h.sessionByID[id]
	h.workerMu.Unlock()
	if !ok {
		return "", fmt.Errorf("session not found: %s", id)
	}
	ts := s.GetTmuxSession()
	if ts == nil {
		return "", fmt.Errorf("session %s has no tmux handle", id)
	}
	content, err := ts.CapturePane()
	if err != nil {
		return "", err
	}
	return session.StripANSI(content), nil
}

// sessionToSnapshot copies the public fields of a session.Session into a
// web.SessionSnapshot. Callers must hold the lock that guards the session
// list pointer — but the per-session getters take s.mu themselves.
func sessionToSnapshot(s *session.Session) web.SessionSnapshot {
	return web.SessionSnapshot{
		ID:              s.ID,
		Title:           s.Title,
		ProjectPath:     s.ProjectPath,
		Status:          string(s.GetStatus()),
		WorkspaceName:   s.WorkspaceName,
		CreatedAt:       s.CreatedAt,
		LastAccessedAt:  s.LastAccessedAt,
		ClaudeSessionID: s.ClaudeSessionID,
		FirstPrompt:     s.FirstPrompt,
		PromptCount:     s.PromptCount,
		IsAlive:         s.IsAlive(),
	}
}

// handleWebMutation routes a web-initiated mutation into the matching helper
// and signals the HTTP handler via m.Reply. Runs on the Update loop, so the
// helpers it calls must not block — most resolve to a tea.Cmd that runs in
// a background goroutine.
func (h *Home) handleWebMutation(msg webMutationMsg) (tea.Model, tea.Cmd) {
	m := msg.m
	reply := func(err error) {
		if m.Reply != nil {
			// Buffered cap 1 — never blocks.
			m.Reply <- err
		}
	}

	switch m.Kind {
	case web.MutationApprove:
		cmd, err := h.approveSessionByID(m.ID)
		if err != nil {
			reply(err)
			return h, nil
		}
		reply(nil)
		return h, cmd

	case web.MutationRestart:
		cmd, err := h.restartSessionByID(m.ID)
		if err != nil {
			reply(err)
			return h, nil
		}
		reply(nil)
		return h, cmd

	case web.MutationSendKeys:
		keys, _ := m.Payload["keys"].(string)
		if keys == "" {
			reply(fmt.Errorf("keys is required"))
			return h, nil
		}
		cmd, err := h.sendKeysToSessionByID(m.ID, keys)
		if err != nil {
			reply(err)
			return h, nil
		}
		reply(nil)
		return h, cmd

	case web.MutationDelete:
		destroy, _ := m.Payload["destroyWorkspace"].(bool)
		unpin, _ := m.Payload["unpinRepo"].(bool)
		// Look up under lock to validate existence and snapshot the fields
		// the delete pipeline needs.
		h.workerMu.Lock()
		s, ok := h.sessionByID[m.ID]
		h.workerMu.Unlock()
		if !ok {
			reply(fmt.Errorf("session not found: %s", m.ID))
			return h, nil
		}
		wsName := s.WorkspaceName
		repoPath := session.GetRepoRoot(s.ProjectPath)
		reply(nil)
		// Hand the message to the regular delete pipeline. deferDelete
		// runs on the Update loop on the next message dispatch.
		return h, func() tea.Msg {
			return sessionDeleteMsg{
				id:               m.ID,
				destroyWorkspace: destroy,
				workspaceName:    wsName,
				unpinRepo:        unpin,
				repoPath:         repoPath,
			}
		}

	case web.MutationCreate:
		title, _ := m.Payload["title"].(string)
		path, _ := m.Payload["path"].(string)
		wsName, _ := m.Payload["workspaceName"].(string)
		if path == "" {
			reply(fmt.Errorf("path is required"))
			return h, nil
		}
		reply(nil)
		return h, func() tea.Msg {
			return sessionCreateMsg{title: title, path: path, workspaceName: wsName}
		}
	}

	reply(fmt.Errorf("unknown mutation kind: %s", m.Kind))
	return h, nil
}

// approveSessionByID sends "y"+Enter to the session if it's alive and in
// waiting status. Extracted from quickApproveSelected so the web handler
// can target sessions by ID rather than cursor position.
func (h *Home) approveSessionByID(id string) (tea.Cmd, error) {
	h.workerMu.Lock()
	s, ok := h.sessionByID[id]
	h.workerMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	if !s.IsAlive() {
		return nil, fmt.Errorf("session not alive")
	}
	if s.GetStatus() != session.StatusWaiting {
		return nil, fmt.Errorf("session not waiting for approval")
	}
	h.markSessionAccessed(s)
	ts := s.GetTmuxSession()
	debuglog.Logger.Info("web: approve", "id", id, "title", s.Title)
	return func() tea.Msg {
		_ = ts.SendKeys("y")
		err := ts.SendKeys("Enter")
		return quickApproveMsg{err: err}
	}, nil
}

// restartSessionByID restarts the named session. Extracted from
// restartSelected. Returns a tea.Cmd that performs the restart in a
// background goroutine and produces a sessionRestartMsg on completion.
func (h *Home) restartSessionByID(id string) (tea.Cmd, error) {
	h.workerMu.Lock()
	s, ok := h.sessionByID[id]
	h.workerMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	h.markSessionAccessed(s)
	debuglog.Logger.Info("web: restart", "id", id, "title", s.Title)
	return func() tea.Msg {
		var err error
		if s.IsAlive() && !s.GetTmuxSession().IsPaneDead() {
			err = s.RespawnClaude()
		} else {
			err = s.Restart()
		}
		return sessionRestartMsg{id: id, err: err}
	}, nil
}

// sendKeysToSessionByID sends raw keys to the session's tmux pane.
func (h *Home) sendKeysToSessionByID(id, keys string) (tea.Cmd, error) {
	h.workerMu.Lock()
	s, ok := h.sessionByID[id]
	h.workerMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	if !s.IsAlive() {
		return nil, fmt.Errorf("session not alive")
	}
	ts := s.GetTmuxSession()
	if ts == nil {
		return nil, fmt.Errorf("session has no tmux handle")
	}
	debuglog.Logger.Info("web: sendkeys", "id", id, "keys", keys)
	return func() tea.Msg {
		if err := ts.SendKeys(keys); err != nil {
			return webSendKeysResultMsg{id: id, err: err}
		}
		return webSendKeysResultMsg{id: id}
	}, nil
}

// webSendKeysResultMsg is consumed in app.go so errors surface as toasts.
type webSendKeysResultMsg struct {
	id  string
	err error
}

// publishSessionEvent forwards a session change to the SSE hub (if wired).
// Safe to call with publisher nil.
func (h *Home) publishSessionEvent(kind string, id string) {
	if h.webPublisher == nil {
		return
	}
	var snap *web.SessionSnapshot
	if id != "" {
		h.workerMu.Lock()
		s, ok := h.sessionByID[id]
		h.workerMu.Unlock()
		if ok {
			v := sessionToSnapshot(s)
			snap = &v
		}
	}
	h.webPublisher.Publish(web.SessionEvent{
		Kind:      kind,
		SessionID: id,
		Snapshot:  snap,
	})
}
