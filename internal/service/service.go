package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/brizzai/fleet/internal/config"
	"github.com/brizzai/fleet/internal/debuglog"
	"github.com/brizzai/fleet/internal/git"
	"github.com/brizzai/fleet/internal/github"
	"github.com/brizzai/fleet/internal/hooks"
	"github.com/brizzai/fleet/internal/naming"
	"github.com/brizzai/fleet/internal/session"
	"github.com/brizzai/fleet/internal/tmux"
)

const (
	defaultTickInterval = 2 * time.Second
	statusRoundRobin    = 5 // sessions per worker cycle
	prTTL               = 60 * time.Second
)

// SessionService owns all session, git, slot, pin, and hook state.
// Both the TUI (via in-process struct) and future daemon clients (via the
// Service interface) consume it through the Observer pattern.
type SessionService struct {
	storage *session.StateDB
	cfg     *config.Config

	// Session state — guarded by mu.
	mu           sync.RWMutex
	sessions     []*session.Session
	sessionByID  map[string]*session.Session
	gitInfoCache map[string]*git.RepoInfo
	slotBindings map[int]string
	pinnedRepos  map[string]bool

	// Hook watcher.
	hookWatcher *hooks.HookWatcher

	// gh CLI availability.
	ghAvailable bool

	// Background worker.
	statusTrigger         chan struct{}
	priorityStatusUpdates chan string
	statusRRIndex         int
	gitRRIndex            int
	ctx                   context.Context
	cancel                context.CancelFunc
	workerStarted         bool

	// Observers.
	observerMu sync.RWMutex
	observers  []Observer
}

// NewSessionService creates a service. Start() must be called before the
// worker runs and observers begin receiving events.
func NewSessionService(storage *session.StateDB, cfg *config.Config) *SessionService {
	ctx, cancel := context.WithCancel(context.Background())
	return &SessionService{
		storage:               storage,
		cfg:                   cfg,
		sessionByID:           make(map[string]*session.Session),
		gitInfoCache:          make(map[string]*git.RepoInfo),
		slotBindings:          make(map[int]string),
		pinnedRepos:           make(map[string]bool),
		statusTrigger:         make(chan struct{}, 1),
		priorityStatusUpdates: make(chan string, 256),
		ctx:                   ctx,
		cancel:                cancel,
	}
}

// --- Observer pattern ---

func (s *SessionService) Subscribe(o Observer) {
	s.observerMu.Lock()
	s.observers = append(s.observers, o)
	s.observerMu.Unlock()
}

func (s *SessionService) Unsubscribe(o Observer) {
	s.observerMu.Lock()
	for i, obs := range s.observers {
		if obs == o {
			s.observers = append(s.observers[:i], s.observers[i+1:]...)
			break
		}
	}
	s.observerMu.Unlock()
}

func (s *SessionService) notify(evt Event) {
	s.observerMu.RLock()
	observers := make([]Observer, len(s.observers))
	copy(observers, s.observers)
	s.observerMu.RUnlock()
	for _, o := range observers {
		o.OnEvent(evt)
	}
}

// --- Lifecycle ---

// Start loads sessions, slot bindings, and pinned repos from storage; injects
// Claude hooks; starts the hook watcher and the background status worker.
// Returns a non-fatal warning string (e.g. "claude CLI not found") and any
// fatal error from storage.
func (s *SessionService) Start() (warning string, err error) {
	rows, err := s.storage.LoadSessions()
	if err != nil {
		return "", err
	}
	sessions := make([]*session.Session, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, session.FromRow(row))
	}

	bindings, _ := s.storage.LoadSlotBindings()
	pinned, _ := s.storage.LoadPinnedRepos()

	configDir := hooks.GetClaudeConfigDir()
	if _, ierr := hooks.InjectClaudeHooks(configDir); ierr != nil {
		debuglog.Logger.Warn("claude hooks: inject failed", "err", ierr)
	}
	s.ghAvailable = github.IsGHAvailable()

	if _, lookErr := exec.LookPath("claude"); lookErr != nil {
		warning = "claude CLI not found — install Claude Code to create sessions"
	}

	s.mu.Lock()
	s.sessions = sessions
	s.rebuildSessionMap()
	if bindings != nil {
		s.slotBindings = bindings
	}
	for _, p := range pinned {
		s.pinnedRepos[p] = true
	}
	s.mu.Unlock()

	if watcher, werr := hooks.NewHookWatcher(); werr == nil {
		s.hookWatcher = watcher
		go watcher.Start()
	} else {
		debuglog.Logger.Warn("hook watcher: init failed", "err", werr)
	}

	if !s.workerStarted {
		s.workerStarted = true
		go s.statusWorker()
	}
	return warning, nil
}

// Stop cancels the worker context and shuts down the hook watcher.
func (s *SessionService) Stop() {
	s.cancel()
	if s.hookWatcher != nil {
		s.hookWatcher.Stop()
	}
}

// TriggerRefresh nudges the worker to run a cycle immediately.
func (s *SessionService) TriggerRefresh() {
	select {
	case s.statusTrigger <- struct{}{}:
	default:
	}
}

// EnqueuePriority requests a priority pane scan for a session at the next
// worker cycle. Bypasses round-robin so hook-driven status changes surface
// within ~one tick instead of (N/statusRoundRobin)*tick. Drops on backpressure
// (256-slot buffer; a dropped event just means the round-robin will pick it
// up shortly).
func (s *SessionService) EnqueuePriority(id string) {
	select {
	case s.priorityStatusUpdates <- id:
	default:
	}
}

// --- Queries ---

func (s *SessionService) Sessions() []*session.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*session.Session, len(s.sessions))
	copy(out, s.sessions)
	return out
}

func (s *SessionService) GetSession(id string) *session.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionByID[id]
}

func (s *SessionService) GitInfo(repo string) *git.RepoInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.gitInfoCache[repo]
}

func (s *SessionService) GitInfoAll() map[string]*git.RepoInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]*git.RepoInfo, len(s.gitInfoCache))
	for k, v := range s.gitInfoCache {
		out[k] = v
	}
	return out
}

func (s *SessionService) IsGHAvailable() bool {
	return s.ghAvailable
}

// PinnedRepos returns a copy of the pinned-repo set as a slice.
func (s *SessionService) PinnedRepos() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.pinnedRepos))
	for p := range s.pinnedRepos {
		out = append(out, p)
	}
	return out
}

// SlotBindings returns a copy of the slot→sessionID map.
func (s *SessionService) SlotBindings() map[int]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[int]string, len(s.slotBindings))
	for k, v := range s.slotBindings {
		out[k] = v
	}
	return out
}

func (s *SessionService) CapturePreview(id string) (string, error) {
	s.mu.RLock()
	sess := s.sessionByID[id]
	s.mu.RUnlock()
	if sess == nil {
		return "", nil
	}
	return sess.GetTmuxSession().CapturePane()
}

// --- Mutations ---

// CreateSession spawns a new session, persists it, and notifies observers.
func (s *SessionService) CreateSession(title, projectPath, workspaceName string) (*session.Session, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return nil, err
	}

	sess := session.NewSession(title, projectPath)
	sess.WorkspaceName = workspaceName

	if err := sess.Start(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.sessions = append(s.sessions, sess)
	s.rebuildSessionMap()
	s.mu.Unlock()

	if err := s.storage.SaveSession(sess.ToRow()); err != nil {
		debuglog.Logger.Error("storage: SaveSession", "err", err)
	}
	s.notify(Event{Type: EventSessionsChanged})
	return sess, nil
}

// DeleteSession kills the session, removes it from storage, deletes the
// hook status file, and prunes any slot binding (FK cascade in SQL).
func (s *SessionService) DeleteSession(id string) {
	s.mu.Lock()
	sess, ok := s.sessionByID[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	if sess.IsAlive() {
		_ = sess.Kill()
	}
	_ = s.storage.DeleteSession(id)
	_ = os.Remove(filepath.Join(hooks.GetHooksDir(), id+".json"))

	remaining := make([]*session.Session, 0, len(s.sessions))
	for _, ss := range s.sessions {
		if ss.ID != id {
			remaining = append(remaining, ss)
		}
	}
	s.sessions = remaining
	s.rebuildSessionMap()
	for slot, sid := range s.slotBindings {
		if sid == id {
			delete(s.slotBindings, slot)
		}
	}
	s.mu.Unlock()
	s.notify(Event{Type: EventSessionsChanged})
}

// SoftDelete removes a session from in-memory tracking and storage but does
// NOT kill its tmux pane — caller (today: the TUI's deferDelete) keeps the
// pane alive during the undo window and is responsible for the eventual
// kill if undo doesn't fire. Returns the row snapshot so the caller can
// later pass it back to RestoreDeleted.
func (s *SessionService) SoftDelete(id string) (*session.SessionRow, error) {
	s.mu.Lock()
	sess, ok := s.sessionByID[id]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("session %s not found", id)
	}
	row := sess.ToRow()
	if err := s.storage.DeleteSession(id); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	remaining := make([]*session.Session, 0, len(s.sessions))
	for _, ss := range s.sessions {
		if ss.ID != id {
			remaining = append(remaining, ss)
		}
	}
	s.sessions = remaining
	s.rebuildSessionMap()
	for slot, sid := range s.slotBindings {
		if sid == id {
			delete(s.slotBindings, slot)
		}
	}
	s.mu.Unlock()
	s.notify(Event{Type: EventSessionsChanged})
	return row, nil
}

// RestartSession respawns the Claude pane (or full restart if dead),
// persists the new status + tmux name.
func (s *SessionService) RestartSession(id string) error {
	s.mu.RLock()
	sess := s.sessionByID[id]
	s.mu.RUnlock()
	if sess == nil {
		return nil
	}

	var err error
	if sess.IsAlive() && !sess.GetTmuxSession().IsPaneDead() {
		err = sess.RespawnClaude()
	} else {
		err = sess.Restart()
	}

	_ = s.storage.UpdateStatus(sess.ID, string(sess.GetStatus()))
	_ = s.storage.UpdateTmuxSession(sess.ID, sess.TmuxSessionName)
	s.notify(Event{Type: EventSessionStatusChanged})
	return err
}

// RenameSession sets a new title and marks the session manually-renamed so
// the auto-namer leaves it alone going forward.
func (s *SessionService) RenameSession(id, newTitle string) {
	s.mu.RLock()
	sess := s.sessionByID[id]
	s.mu.RUnlock()
	if sess == nil {
		return
	}
	sess.Title = newTitle
	sess.ManuallyRenamed = true
	_ = s.storage.UpdateTitle(id, newTitle)
	_ = s.storage.MarkManuallyRenamed(id)
	s.notify(Event{Type: EventSessionsChanged})
}

// AcknowledgeSession clears the "needs attention" flag and bumps last-accessed.
func (s *SessionService) AcknowledgeSession(id string) {
	s.mu.RLock()
	sess := s.sessionByID[id]
	s.mu.RUnlock()
	if sess == nil {
		return
	}
	sess.MarkAccessed()
	sess.Acknowledge()
	_ = s.storage.SetAcknowledged(id, true)
	_ = s.storage.UpdateLastAccessed(id)
}

// BindSlot binds a digit slot (0–9) to a session.
func (s *SessionService) BindSlot(slot int, sessionID string) error {
	s.mu.Lock()
	if _, ok := s.sessionByID[sessionID]; !ok {
		s.mu.Unlock()
		return fmt.Errorf("session %s not found", sessionID)
	}
	if err := s.storage.BindSlot(slot, sessionID); err != nil {
		s.mu.Unlock()
		return err
	}
	s.slotBindings[slot] = sessionID
	s.mu.Unlock()
	s.notify(Event{Type: EventSessionsChanged})
	return nil
}

// UnbindSlot removes a slot binding.
func (s *SessionService) UnbindSlot(slot int) error {
	s.mu.Lock()
	if err := s.storage.UnbindSlot(slot); err != nil {
		s.mu.Unlock()
		return err
	}
	delete(s.slotBindings, slot)
	s.mu.Unlock()
	s.notify(Event{Type: EventSessionsChanged})
	return nil
}

// PinRepo pins a repo path so its (possibly empty) group stays in the sidebar.
func (s *SessionService) PinRepo(path string) error {
	s.mu.Lock()
	if s.pinnedRepos[path] {
		s.mu.Unlock()
		return nil
	}
	if err := s.storage.PinRepo(path); err != nil {
		s.mu.Unlock()
		return err
	}
	s.pinnedRepos[path] = true
	s.mu.Unlock()
	s.notify(Event{Type: EventSessionsChanged})
	return nil
}

// UnpinRepo removes the pin.
func (s *SessionService) UnpinRepo(path string) error {
	s.mu.Lock()
	if !s.pinnedRepos[path] {
		s.mu.Unlock()
		return nil
	}
	if err := s.storage.UnpinRepo(path); err != nil {
		s.mu.Unlock()
		return err
	}
	delete(s.pinnedRepos, path)
	s.mu.Unlock()
	s.notify(Event{Type: EventSessionsChanged})
	return nil
}

// SnapshotForUndo returns a row snapshot of the live session for later
// restore via RestoreDeleted. Caller is responsible for snapshotting BEFORE
// calling DeleteSession.
func (s *SessionService) SnapshotForUndo(id string) (*session.SessionRow, error) {
	s.mu.RLock()
	sess := s.sessionByID[id]
	s.mu.RUnlock()
	if sess == nil {
		return nil, fmt.Errorf("session %s not found", id)
	}
	return sess.ToRow(), nil
}

// RestoreDeleted re-saves a previously-snapshotted row and re-loads the
// session into memory. The tmux pane is not respawned — that's the caller's
// responsibility (UI keeps the pane alive during the undo window).
func (s *SessionService) RestoreDeleted(row *session.SessionRow) error {
	if row == nil {
		return fmt.Errorf("nil row")
	}
	if err := s.storage.SaveSession(row); err != nil {
		return err
	}
	sess := session.FromRow(row)
	s.mu.Lock()
	s.sessions = append(s.sessions, sess)
	s.rebuildSessionMap()
	s.mu.Unlock()
	s.notify(Event{Type: EventSessionsChanged})
	return nil
}

// OnFirstPrompt persists FirstPrompt and, if the session isn't manually
// renamed and has no title yet, generates a title via the naming heuristic.
func (s *SessionService) OnFirstPrompt(id, prompt string) {
	if prompt == "" {
		return
	}
	s.mu.RLock()
	sess := s.sessionByID[id]
	s.mu.RUnlock()
	if sess == nil {
		return
	}

	if sess.FirstPrompt != prompt {
		sess.FirstPrompt = prompt
		_ = s.storage.UpdateFirstPrompt(id, prompt)
	}
	if sess.ManuallyRenamed || sess.TitleGenerated || !s.cfg.IsAutoNameEnabled() {
		return
	}
	title := naming.GenerateTitle(prompt)
	if title == "" {
		return
	}
	sess.Title = title
	sess.TitleGenerated = true
	_ = s.storage.UpdateTitle(id, title)
	_ = s.storage.MarkTitleGenerated(id)
	s.notify(Event{Type: EventSessionsChanged})
}

// OnPromptCount persists the count and resets TitleGenerated so the next
// auto-name cycle re-titles from the latest prompt (matches master's
// "retitle on every new prompt" behaviour). No-op if the session is
// manually renamed or has a Claude session name.
func (s *SessionService) OnPromptCount(id string, count int) {
	s.mu.RLock()
	sess := s.sessionByID[id]
	s.mu.RUnlock()
	if sess == nil || count <= sess.PromptCount {
		return
	}
	sess.PromptCount = count
	_ = s.storage.UpdatePromptCount(id, count)
	if !s.cfg.IsAutoNameEnabled() || sess.ManuallyRenamed || sess.ClaudeSessionName != "" {
		return
	}
	if sess.TitleGenerated {
		sess.TitleGenerated = false
		_ = s.storage.ResetTitleGenerated(id)
	}
}

// --- Background worker ---

func (s *SessionService) statusWorker() {
	interval := defaultTickInterval
	if s.cfg != nil && s.cfg.TickIntervalSec > 0 {
		interval = time.Duration(s.cfg.TickIntervalSec) * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.statusTrigger:
		case <-ticker.C:
		}
		s.statusWorkerCycle()
	}
}

func (s *SessionService) statusWorkerCycle() {
	defer func() {
		if r := recover(); r != nil {
			debuglog.Logger.Error("statusWorkerCycle panic recovered", "panic", r)
		}
	}()

	tmux.RefreshSessionCache()

	s.mu.RLock()
	sessions := make([]*session.Session, len(s.sessions))
	copy(sessions, s.sessions)
	s.mu.RUnlock()

	if len(sessions) == 0 {
		return
	}

	// Hook sync: merge hook-derived state, persist drift, enqueue any session
	// whose status changed for the priority pane scan below.
	if s.hookWatcher != nil {
		for _, sess := range sessions {
			hs := s.hookWatcher.GetStatus(sess.ID)
			if hs == nil {
				continue
			}
			oldStatus := sess.GetStatus()
			oldClaudeSessionID := sess.ClaudeSessionID
			oldFirstPrompt := sess.FirstPrompt
			oldPromptCount := sess.PromptCount
			sess.UpdateHookStatus(&session.HookStatus{
				Status:      hs.Status,
				SessionID:   hs.SessionID,
				UpdatedAt:   hs.UpdatedAt,
				UserPrompt:  hs.UserPrompt,
				PromptCount: hs.PromptCount,
			})
			if sess.ClaudeSessionID != oldClaudeSessionID && sess.ClaudeSessionID != "" {
				_ = s.storage.UpdateClaudeSessionID(sess.ID, sess.ClaudeSessionID)
			}
			if sess.PromptCount != oldPromptCount {
				_ = s.storage.UpdatePromptCount(sess.ID, sess.PromptCount)
				if s.cfg.IsAutoNameEnabled() && sess.TitleGenerated && !sess.ManuallyRenamed && sess.ClaudeSessionName == "" {
					sess.TitleGenerated = false
					_ = s.storage.ResetTitleGenerated(sess.ID)
				}
			}
			if sess.FirstPrompt != "" && sess.FirstPrompt != oldFirstPrompt {
				_ = s.storage.UpdateFirstPrompt(sess.ID, sess.FirstPrompt)
			}
			if sess.GetStatus() != oldStatus {
				select {
				case s.priorityStatusUpdates <- sess.ID:
				default:
				}
			}
		}
	}

	// Auto-name: one session per cycle. Mirrors the UI worker that this
	// service replaces (legacy app.go:2246-2290): Claude's JSONL-derived
	// session name takes priority, falling back to the prompt heuristic.
	if s.cfg.IsAutoNameEnabled() {
		for _, sess := range sessions {
			if sess.ManuallyRenamed {
				continue
			}
			if sess.ClaudeSessionID != "" && time.Since(sess.ClaudeNameLastChecked) > 30*time.Second {
				sess.ClaudeNameLastChecked = time.Now()
				name := session.ReadClaudeSessionName(sess.ClaudeSessionID, sess.ProjectPath)
				if name != "" && name != sess.ClaudeSessionName {
					sess.ClaudeSessionName = name
					sess.Title = name
					_ = s.storage.UpdateTitle(sess.ID, name)
					sess.TitleGenerated = true
					_ = s.storage.MarkTitleGenerated(sess.ID)
				}
			}
			if sess.ClaudeSessionName != "" {
				continue
			}
			if sess.FirstPrompt != "" && !sess.TitleGenerated {
				title := naming.GenerateTitle(sess.FirstPrompt)
				if title != "" && title != sess.Title {
					sess.Title = title
					_ = s.storage.UpdateTitle(sess.ID, title)
				}
				sess.TitleGenerated = true
				_ = s.storage.MarkTitleGenerated(sess.ID)
				break // one per cycle
			}
		}
	}

	// Drain priority queue (sessions whose hook status just changed, plus any
	// external EnqueuePriority callers).
	priorityIDs := make(map[string]bool)
drainPriority:
	for {
		select {
		case id := <-s.priorityStatusUpdates:
			priorityIDs[id] = true
		default:
			break drainPriority
		}
	}
	processed := make(map[string]bool, len(priorityIDs))
	for _, sess := range sessions {
		if !priorityIDs[sess.ID] {
			continue
		}
		s.updateAndPersistStatus(sess)
		processed[sess.ID] = true
	}

	// Round-robin pane scan (skipping already-processed priority sessions).
	count := statusRoundRobin
	if count > len(sessions) {
		count = len(sessions)
	}
	for i := 0; i < count; i++ {
		idx := (s.statusRRIndex + i) % len(sessions)
		sess := sessions[idx]
		if processed[sess.ID] {
			continue
		}
		s.updateAndPersistStatus(sess)
	}
	s.statusRRIndex = (s.statusRRIndex + count) % len(sessions)

	repos := uniqueRepoPathsFromSessions(sessions)
	if len(repos) > 0 {
		idx := s.gitRRIndex % len(repos)
		repo := repos[idx]

		info := git.RefreshGitInfo(repo)

		s.mu.Lock()
		if old, ok := s.gitInfoCache[repo]; ok && old.PR != nil {
			info.PR = old.PR
			info.LastPRRefresh = old.LastPRRefresh
		}
		s.mu.Unlock()

		if s.ghAvailable && (info.LastPRRefresh.IsZero() || time.Since(info.LastPRRefresh) > prTTL) {
			git.RefreshPRInfo(info, repo)
		}

		s.mu.Lock()
		s.gitInfoCache[repo] = info
		s.mu.Unlock()

		s.gitRRIndex++
	}

	s.notify(Event{Type: EventSessionStatusChanged})
}

// --- Internal helpers ---

// updateAndPersistStatus runs sess.UpdateStatus() (which scans the tmux pane
// and merges with hook state), persisting any change to storage.
func (s *SessionService) updateAndPersistStatus(sess *session.Session) {
	oldStatus := sess.GetStatus()
	sess.UpdateStatus()
	newStatus := sess.GetStatus()
	if oldStatus != newStatus {
		_ = s.storage.UpdateStatus(sess.ID, string(newStatus))
	}
}

func (s *SessionService) rebuildSessionMap() {
	s.sessionByID = make(map[string]*session.Session, len(s.sessions))
	for _, sess := range s.sessions {
		s.sessionByID[sess.ID] = sess
	}
}

func uniqueRepoPathsFromSessions(sessions []*session.Session) []string {
	seen := make(map[string]bool)
	repos := make([]string, 0)
	for _, sess := range sessions {
		root := session.GetRepoRoot(sess.ProjectPath)
		if !seen[root] {
			seen[root] = true
			repos = append(repos, root)
		}
	}
	return repos
}
