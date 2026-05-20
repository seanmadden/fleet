package ui

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brizzai/fleet/internal/analytics"
	"github.com/brizzai/fleet/internal/chrome"
	"github.com/brizzai/fleet/internal/config"
	"github.com/brizzai/fleet/internal/debuglog"
	"github.com/brizzai/fleet/internal/forge"
	"github.com/brizzai/fleet/internal/git"
	"github.com/brizzai/fleet/internal/hooks"
	"github.com/brizzai/fleet/internal/naming"
	"github.com/brizzai/fleet/internal/perfwatch"
	"github.com/brizzai/fleet/internal/session"
	"github.com/brizzai/fleet/internal/tmux"
	"github.com/brizzai/fleet/internal/workspace"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	overlay "github.com/rmhubbert/bubbletea-overlay"
)

const (
	tickInterval           = 2 * time.Second
	previewTickInterval    = 500 * time.Millisecond
	previewCacheTTL        = 500 * time.Millisecond
	layoutBreakpointSingle = 50
	layoutBreakpointDual   = 80
	helpBarHeight          = 2 // border line + shortcuts
	statusRoundRobin       = 5 // sessions per tick
	undoDeleteTimeout      = 5 * time.Second
)

// PendingDelete holds state for a deferred session deletion (undo window).
type PendingDelete struct {
	Nonce         string              // unique ID for timer matching
	Session       *session.Session    // kept alive (tmux still running)
	Row           *session.SessionRow // DB snapshot for re-insert
	RepoPath      string
	DestroyWS     bool
	WorkspaceName string
	UnpinRepo     bool // true if user chose D +Remove Repo
	DeletedAt     time.Time
}

// Message types.
type (
	tickMsg          time.Time
	hookChangedMsg   struct{} // HookWatcher detected a status file change
	statusUpdateMsg  struct{ attachedSessionID string }
	sessionDeleteMsg struct {
		id               string
		err              error
		destroyWorkspace bool
		workspaceName    string
		unpinRepo        bool
		repoPath         string
	}
	pendingDeleteExpireMsg struct {
		nonce string
	}
	sessionRestartMsg struct {
		id  string
		err error
	}
	sessionCreateResultMsg struct {
		session   *session.Session
		err       error
		autoFocus bool // attach/focus immediately after creation (per cfg.IsFocusOnNewSessionEnabled)
	}
	previewMsg struct {
		sessionID string
		content   string
	}
	loadSessionsMsg struct {
		sessions     []*session.Session
		slotBindings map[int]string
		repoOrder    map[string]int64
		warning      string
		err          error
	}
	openEditorMsg        struct{ err error }
	openPRMsg            struct{ err error }
	quickApproveMsg      struct{ err error }
	spinnerTickMsg       struct{}
	previewTickMsg       time.Time
	focusTickMsg         time.Time
	slotAssignTimeoutMsg struct{}
	reloadAllResultMsg   struct {
		restarted int
		skipped   int
		errors    []string
	}
)

func spinnerTickCmd() tea.Msg {
	time.Sleep(100 * time.Millisecond)
	return spinnerTickMsg{}
}

// Home is the main Bubble Tea model.
type Home struct {
	width  int
	height int

	sessions    []*session.Session
	sessionByID map[string]*session.Session
	storage     *session.StateDB
	flatItems   []SidebarItem

	cursor     int
	viewOffset int

	isAttaching atomic.Bool
	err         error
	errTime     time.Time
	infoMsg     string
	infoTime    time.Time

	newDialog             *NewSessionDialog
	confirmDialog         *ConfirmDialog
	renameDialog          *RenameDialog
	helpOverlay           *HelpOverlay
	settingsDialog        *SettingsDialog
	worktreeDialog        *WorktreeDialog
	createWorkspaceDialog *CreateWorkspaceDialog
	branchDialog          *BranchCheckoutDialog
	commandPalette        *CommandPaletteDialog

	pendingWorkspaces []*PendingWorkspace // in-flight workspace creations
	pendingDeletes    []PendingDelete     // undo stack for deferred deletions
	// finalizingDeletes holds entries whose undo window has expired but whose
	// background cleanup (tmux kill, hook removal, workspace destroy) is still
	// running. Quit drains both this list and pendingDeletes so an in-flight
	// kill isn't lost when fleet exits mid-cleanup.
	finalizingDeletes []PendingDelete
	pinnedRepos       map[string]bool  // pinned repo paths (persist in SQLite)
	repoOrder         map[string]int64 // repo path -> explicit sort key (persist in SQLite); missing = alphabetical fallback

	repoExpanded     map[string]bool // repo path -> expanded state
	previewCache     map[string]string
	previewCacheTime map[string]time.Time
	statusRRIndex    int // round-robin index for status updates

	gitInfoCache map[string]*git.RepoInfo  // main repo path -> git info (drives group headers)
	gitRRIndex   int                       // round-robin index for git refresh
	repoForge    map[string]forge.Provider // main repo path -> forge provider (nil = none); worker-only, populated lazily

	// Per-session git info — drives the worktree-aware sidebar row info (own
	// branch + dirty + PR). Keyed by session ID rather than repo path because
	// each worktree-session has its own branch/PR pairing distinct from the
	// main-repo header above it. Populated by a separate worker round-robin
	// that skips no-worktree sessions (where GetRepoRoot == GetMainRepo).
	sessionGitInfo    map[string]*git.RepoInfo
	sessionGitRRIndex int

	hookWatcher *hooks.HookWatcher

	// Focus mode (split view).
	focusMode     bool
	controlClient *tmux.ControlClient
	cachedSidebar string // cached sidebar render for focus mode
	sidebarDirty  bool   // true when sidebar needs rebuild

	// Filter.
	filterInput  textinput.Model
	filterActive bool
	filterText   string

	// Slot hotkeys (RTS-style quick access: digit=jump, double-digit=attach, alt+digit or =<digit>=bind).
	slotBindings      map[int]string // slot (0-9) -> session ID
	lastSlotTapSlot   int            // -1 when no pending tap
	lastSlotTapAt     time.Time
	slotAssignMode    int // 0=off, 1=bind pending (=<digit>), 2=unbind pending (==<digit>)
	slotAssignExpires time.Time

	// Floating toast overlay (bottom-right).
	toasts *ToastStack

	// Config.
	cfg     *config.Config
	version string

	// Bug report / diagnostics.
	errorHistory *ErrorHistory
	actionLog    *ActionLog
	bugReport    *BugReportDialog

	// Background worker for async status/git/PR updates.
	statusTrigger         chan struct{} // buffered(1), triggers worker
	priorityStatusUpdates chan string   // buffered, session IDs with fresh hook changes — drained before round-robin
	workerMu              sync.Mutex    // protects sessions/gitInfoCache from concurrent worker access
	ctx                   context.Context
	cancel                context.CancelFunc
	workerStarted         bool

	startTime time.Time // app start time for uptime tracking

	// Rendering diagnostics (accumulated counters for bug reports).
	renderStats RenderStats
}

// NewHome creates the main TUI model.
func NewHome(storage *session.StateDB, cfg *config.Config, version string) *Home {
	ctx, cancel := context.WithCancel(context.Background())

	fi := textinput.New()
	fi.Placeholder = "filter..."
	fi.CharLimit = 64
	fi.Width = 20

	// Apply theme from config if set.
	if cfg.Theme != "" {
		ApplyPalette(PaletteByName(cfg.Theme))
	}

	return &Home{
		storage:               storage,
		sessionByID:           make(map[string]*session.Session),
		repoExpanded:          make(map[string]bool),
		slotBindings:          make(map[int]string),
		lastSlotTapSlot:       -1,
		toasts:                NewToastStack(),
		pinnedRepos:           make(map[string]bool),
		repoOrder:             make(map[string]int64),
		newDialog:             NewNewSessionDialog(),
		confirmDialog:         NewConfirmDialog(),
		renameDialog:          NewRenameDialog(),
		helpOverlay:           NewHelpOverlay(),
		settingsDialog:        NewSettingsDialog(cfg),
		worktreeDialog:        NewWorktreeDialog(),
		createWorkspaceDialog: NewCreateWorkspaceDialog(),
		branchDialog:          NewBranchCheckoutDialog(),
		commandPalette:        NewCommandPaletteDialog(),
		bugReport:             NewBugReportDialog(),
		previewCache:          make(map[string]string),
		previewCacheTime:      make(map[string]time.Time),
		gitInfoCache:          make(map[string]*git.RepoInfo),
		repoForge:             make(map[string]forge.Provider),
		sessionGitInfo:        make(map[string]*git.RepoInfo),
		filterInput:           fi,
		cfg:                   cfg,
		version:               version,
		errorHistory:          NewErrorHistory(50),
		actionLog:             NewActionLog(100),
		statusTrigger:         make(chan struct{}, 1),
		priorityStatusUpdates: make(chan string, 256),
		ctx:                   ctx,
		cancel:                cancel,
		startTime:             time.Now(),
	}
}

// Init implements tea.Model.
func (h *Home) Init() tea.Cmd {
	return tea.Batch(
		h.loadSessions,
		h.tick(),
		h.previewTick(),
	)
}

// Update implements tea.Model.
func (h *Home) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if perfwatch.Enabled() {
		tok := perfwatch.MarkUpdateStart(fmt.Sprintf("%T", msg))
		defer perfwatch.MarkUpdateEnd(tok)
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h.renderStats.RecordResize(msg.Width, msg.Height)
		// Only log resizes after the initial one (startup always sends one).
		if h.width > 0 && (msg.Width != h.width || msg.Height != h.height) {
			debuglog.Logger.Info("window resized",
				"from", fmt.Sprintf("%dx%d", h.width, h.height),
				"to", fmt.Sprintf("%dx%d", msg.Width, msg.Height),
				"resize_count", h.renderStats.ResizeCount,
			)
		}
		h.width = msg.Width
		h.height = msg.Height
		h.sidebarDirty = true
		h.newDialog.SetSize(msg.Width, msg.Height)
		h.confirmDialog.SetSize(msg.Width, msg.Height)
		h.renameDialog.SetSize(msg.Width, msg.Height)
		h.helpOverlay.SetSize(msg.Width, msg.Height)
		h.settingsDialog.SetSize(msg.Width, msg.Height)
		h.worktreeDialog.SetSize(msg.Width, msg.Height)
		h.createWorkspaceDialog.SetSize(msg.Width, msg.Height)
		h.branchDialog.SetSize(msg.Width, msg.Height)
		h.commandPalette.SetSize(msg.Width, msg.Height)
		h.bugReport.SetSize(msg.Width, msg.Height)
		h.syncViewport()
		// Resize tmux sessions so Claude wraps to fit the new preview pane.
		// Without this, capture-pane returns content wider than fleet renders
		// and ansi.Truncate (preview.go) chops the right side off.
		cols, rows := h.previewPaneSize()
		h.dispatchSessionResize(cols, rows)
		return h, nil

	case tea.KeyMsg:
		return h.handleKey(msg)

	case tickMsg:
		return h.handleTick()

	case hookChangedMsg:
		// HookWatcher detected a status file change. Do immediate hook-only sync,
		// then hand sessions whose hook changed to the worker via the priority queue
		// so they get a full UpdateStatus() within ~100ms instead of waiting for round-robin.
		h.workerMu.Lock()
		changed := h.syncHookStatuses(h.sessions)
		h.rebuildFlatItems()
		h.workerMu.Unlock()
		h.enqueuePriorityUpdates(changed)
		return h, h.listenForHookChanges

	case statusUpdateMsg:
		// Returned after detaching from session.
		h.isAttaching.Store(false)
		// Immediate hook sync (data already in HookWatcher from hooks that fired during attach).
		h.workerMu.Lock()
		changed := h.syncHookStatuses(h.sessions)
		h.rebuildFlatItems()
		h.workerMu.Unlock()
		h.enqueuePriorityUpdates(changed)
		// Also trigger full background refresh for pane captures, git, etc.
		select {
		case h.statusTrigger <- struct{}{}:
		default:
		}
		return h, nil

	case sessionCreateMsg:
		return h.handleSessionCreate(msg)

	case newSessionRequestMsg:
		// Dispatched from the path-picker dialog (palette-only "New Session at
		// Path") when the user picks an existing git repo. Mirrors the `n` key
		// path so both entry points produce the same worktree-by-default flow.
		return h.startWorktreeSessionForRepo(msg.path)

	case forkSessionMsg:
		s := session.NewSession(msg.title, msg.path)
		s.WorkspaceName = msg.workspaceName
		s.ForkFromID = msg.parentClaudeSessionID
		if cols, rows := h.previewPaneSize(); cols > 0 {
			s.SetPreferredSize(cols, rows)
		}
		return h, func() tea.Msg {
			if err := s.Start(); err != nil {
				return sessionCreateResultMsg{err: err}
			}
			return sessionCreateResultMsg{session: s}
		}

	case sessionCreateResultMsg:
		return h.handleSessionCreateResult(msg)

	case slotAssignTimeoutMsg:
		if h.slotAssignMode != 0 && !time.Now().Before(h.slotAssignExpires) {
			h.slotAssignMode = 0
		}
		return h, nil

	case sessionDeleteMsg:
		if msg.err != nil {
			h.setError(msg.err)
			return h, nil
		}
		analytics.Track(analytics.EventSessionDeleted, nil)
		return h.deferDelete(msg)

	case pendingDeleteExpireMsg:
		return h.handlePendingDeleteExpire(msg)

	case sessionRestartMsg:
		if msg.err != nil {
			h.setError(fmt.Errorf("restart failed: %w", msg.err))
		}
		// Update storage with new status and tmux session name.
		if s, ok := h.sessionByID[msg.id]; ok {
			if err := h.storage.UpdateStatus(s.ID, string(s.GetStatus())); err != nil {
				debuglog.Logger.Error("storage: UpdateStatus after restart", "id", s.ID, "err", err)
			}
			if err := h.storage.UpdateTmuxSession(s.ID, s.TmuxSessionName); err != nil {
				debuglog.Logger.Error("storage: UpdateTmuxSession after restart", "id", s.ID, "err", err)
			}
		}
		h.rebuildFlatItems()

	case commandPaletteMsg:
		h.actionLog.Add("command: "+msg.commandID, "", true)
		return h.dispatchCommand(msg.commandID)

	case reloadAllResultMsg:
		for _, s := range h.sessions {
			if err := h.storage.UpdateStatus(s.ID, string(s.GetStatus())); err != nil {
				debuglog.Logger.Error("storage: UpdateStatus after reload all", "id", s.ID, "err", err)
			}
			if err := h.storage.UpdateTmuxSession(s.ID, s.TmuxSessionName); err != nil {
				debuglog.Logger.Error("storage: UpdateTmuxSession after reload all", "id", s.ID, "err", err)
			}
		}
		h.rebuildFlatItems()
		// Trigger immediate status refresh.
		select {
		case h.statusTrigger <- struct{}{}:
		default:
		}
		if len(msg.errors) > 0 {
			h.setError(fmt.Errorf("reloaded %d sessions, %d failed: %s",
				msg.restarted, len(msg.errors), strings.Join(msg.errors, ", ")))
		} else if msg.restarted > 0 {
			h.setInfo(fmt.Sprintf("Reloaded %d sessions (%d skipped)", msg.restarted, msg.skipped))
		}
		return h, nil

	case sessionRenameMsg:
		if s, ok := h.sessionByID[msg.id]; ok {
			s.Title = msg.newTitle
			s.ManuallyRenamed = true
			analytics.Track(analytics.EventSessionRenamed, nil)
			if err := h.storage.UpdateTitle(s.ID, msg.newTitle); err != nil {
				debuglog.Logger.Error("storage: UpdateTitle (rename)", "id", s.ID, "err", err)
			}
			if err := h.storage.MarkManuallyRenamed(s.ID); err != nil {
				debuglog.Logger.Error("storage: MarkManuallyRenamed", "id", s.ID, "err", err)
			}
			h.rebuildFlatItems()
		}
		return h, nil

	case settingsClosedMsg:
		// Re-read tick interval from config after settings change.
		return h, nil

	case bugReportClosedMsg:
		return h, nil

	case openEditorMsg:
		if msg.err != nil {
			h.setError(fmt.Errorf("editor: %w", msg.err))
		}
		return h, nil

	case openPRMsg:
		if msg.err != nil {
			h.setError(msg.err)
		}
		return h, nil

	case quickApproveMsg:
		if msg.err != nil {
			h.setError(fmt.Errorf("approve: %w", msg.err))
		}
		return h, nil

	case branchListMsg:
		if msg.err != nil {
			h.branchDialog.ShowError(msg.err.Error())
			return h, nil
		}
		h.branchDialog.Show(msg.branches, msg.repoPath, msg.isDirty, msg.userEmail)
		return h, nil

	case branchCheckoutMsg:
		h.branchDialog.Hide()
		if msg.err != nil {
			h.setError(fmt.Errorf("checkout: %w", msg.err))
			return h, nil
		}
		// Refresh git info for the repo.
		h.workerMu.Lock()
		h.gitInfoCache[msg.repoPath] = git.RefreshGitInfo(msg.repoPath)
		h.workerMu.Unlock()
		h.rebuildFlatItems()
		// Trigger PR refresh for new branch.
		select {
		case h.statusTrigger <- struct{}{}:
		default:
		}
		return h, nil

	case statusSnapshotMsg:
		if msg.err != nil {
			h.setError(fmt.Errorf("snapshot: %w", msg.err))
		} else {
			h.setInfo("Snapshot saved: " + msg.path)
		}
		return h, nil

	case previewMsg:
		h.previewCache[msg.sessionID] = msg.content
		h.previewCacheTime[msg.sessionID] = time.Now()
		return h, nil

	case workspaceListMsg:
		if msg.err != nil {
			h.worktreeDialog.Hide()
			h.setError(fmt.Errorf("worktree list: %w", msg.err))
			return h, nil
		}
		if msg.provider.IsCustom() {
			// Custom provider: go straight to create workspace dialog.
			h.worktreeDialog.Hide()
			h.createWorkspaceDialog.Show(msg.provider, msg.repoPath)
			return h, nil
		}
		h.worktreeDialog.Show(msg.workspaces, h.sessions, msg.provider, msg.repoPath, msg.defaultBranch)
		return h, nil

	case workspaceSelectedMsg:
		return h.handleSessionCreate(sessionCreateMsg{
			path:          msg.info.Path,
			title:         msg.info.Name,
			workspaceName: msg.info.Name,
		})

	case showCreateWorkspaceMsg:
		h.worktreeDialog.Hide()
		h.createWorkspaceDialog.Show(msg.provider, msg.repoPath)
		return h, nil

	case showWorktreeDialogMsg:
		h.createWorkspaceDialog.Hide()
		// Re-fetch worktree list for the same repo.
		if msg.repoPath != "" {
			h.worktreeDialog.ShowLoading()
			return h, tea.Batch(h.fetchWorkspaceListForRepo(msg.repoPath), spinnerTickCmd)
		}
		return h, nil

	case workspaceCreateMsg:
		// Close dialog immediately — creation runs in background.
		h.createWorkspaceDialog.Hide()
		analytics.Track(analytics.EventWorkspaceCreated, map[string]interface{}{
			"provider": func() string {
				if msg.provider.IsCustom() {
					return "shell"
				}
				return "git"
			}(),
		})

		pw := &PendingWorkspace{
			ID:       generatePendingID(),
			Name:     msg.name,
			RepoPath: msg.repoPath,
		}
		h.pendingWorkspaces = append(h.pendingWorkspaces, pw)

		// Expand the repo group and rebuild sidebar.
		h.repoExpanded[msg.repoPath] = true
		h.rebuildFlatItems()

		// Auto-select the phantom entry.
		for i, item := range h.flatItems {
			if item.Pending != nil && item.Pending.ID == pw.ID {
				h.cursor = i
				h.syncViewport()
				break
			}
		}

		pendingID := pw.ID
		provider := msg.provider
		repoPath := msg.repoPath
		name := msg.name
		branch := msg.branch
		baseBranch := msg.baseBranch
		copyClaudeSettings := h.cfg.IsCopyClaudeSettingsEnabled() && !provider.IsCustom()
		return h, tea.Batch(func() tea.Msg {
			info, err := provider.Create(repoPath, name, branch, baseBranch)
			if err == nil && info != nil && copyClaudeSettings {
				copyClaudeSettingsFile(repoPath, info.Path)
			}
			return workspaceCreateResultMsg{info: info, err: err, pendingID: pendingID, repoPath: repoPath}
		}, spinnerTickCmd)

	case workspaceCreateResultMsg:
		h.removePendingWorkspace(msg.pendingID)

		if msg.err != nil {
			h.setError(fmt.Errorf("workspace create failed: %w", msg.err))
			h.rebuildFlatItems()
			// Clamp cursor if it was on the removed phantom.
			if h.cursor >= len(h.flatItems) && len(h.flatItems) > 0 {
				h.cursor = len(h.flatItems) - 1
			}
			return h, nil
		}
		return h.handleSessionCreate(sessionCreateMsg{
			path:          msg.info.Path,
			title:         msg.info.Name,
			workspaceName: msg.info.Name,
		})

	case deleteCleanupDoneMsg:
		for i, pd := range h.finalizingDeletes {
			if pd.Session.ID == msg.sessionID {
				h.finalizingDeletes = append(h.finalizingDeletes[:i], h.finalizingDeletes[i+1:]...)
				break
			}
		}
		if msg.workspaceErr != nil {
			h.setError(fmt.Errorf("workspace destroy: %w", msg.workspaceErr))
		}
		return h, nil

	case previewTickMsg:
		// Fast preview-only tick — skips status/git work, just refreshes the preview pane.
		if h.focusMode {
			return h, h.previewTick() // focus mode has its own faster tick
		}
		var previewCmd tea.Cmd
		if sel := h.selectedSession(); sel != nil && sel.IsAlive() {
			previewCmd = h.fetchPreview(sel)
		}
		if previewCmd != nil {
			return h, tea.Batch(previewCmd, h.previewTick())
		}
		return h, h.previewTick()

	case focusTickMsg:
		if !h.focusMode {
			return h, nil
		}
		s := h.selectedSession()
		if s == nil || !s.IsAlive() {
			h.focusMode = false
			h.sidebarDirty = true
			return h, nil
		}
		return h, tea.Batch(h.fetchPreviewFresh(s), h.focusTick())

	case spinnerTickMsg:
		// Advance spinner in whichever dialog is active.
		if h.worktreeDialog.IsVisible() && h.worktreeDialog.loading {
			h.worktreeDialog.frame++
			return h, spinnerTickCmd
		}
		if h.branchDialog.IsVisible() && h.branchDialog.loading {
			h.branchDialog.frame++
			return h, spinnerTickCmd
		}
		if h.createWorkspaceDialog.IsVisible() && h.createWorkspaceDialog.creating {
			h.createWorkspaceDialog.frame++
			return h, spinnerTickCmd
		}
		// Animate pending workspace spinners in sidebar.
		if len(h.pendingWorkspaces) > 0 {
			for _, pw := range h.pendingWorkspaces {
				pw.Frame++
			}
			return h, spinnerTickCmd
		}
		return h, nil

	case loadSessionsMsg:
		if msg.err != nil {
			h.setError(msg.err)
			return h, nil
		}
		if msg.warning != "" {
			h.setError(fmt.Errorf("%s", msg.warning))
		}
		h.sessions = msg.sessions
		h.rebuildSessionMap()
		// Keep only bindings whose session is present in the loaded view. Do
		// NOT delete absent bindings from storage here: FLEET_DEMO_PREFIX and
		// similar filters shrink the session set transiently, and writing back
		// would permanently destroy real bindings. The FK cascade on session
		// delete handles the only case where a binding should actually vanish.
		if msg.slotBindings != nil {
			h.slotBindings = make(map[int]string, len(msg.slotBindings))
			for slot, id := range msg.slotBindings {
				if _, ok := h.sessionByID[id]; ok {
					h.slotBindings[slot] = id
				}
			}
		}
		// Load pinned repos from storage.
		if pinnedPaths, err := h.storage.LoadPinnedRepos(); err == nil {
			for _, p := range pinnedPaths {
				h.pinnedRepos[p] = true
			}
		}
		// Adopt user-controlled repo order (path → explicit sort_key). Repos absent
		// from this map sort alphabetically in BuildFlatItems.
		if msg.repoOrder != nil {
			h.repoOrder = msg.repoOrder
		}
		// Pinned-repo migration (per-session-worktrees): pre-feature, fleet
		// pinned the *worktree* path on session create. Post-feature, sessions
		// group by main repo, so a worktree pin would surface as its own empty
		// (or worse, duplicated) group. Re-key any worktree-rooted pins to the
		// main repo. Idempotent: re-runs on each restart but only writes when
		// it finds something to migrate.
		h.migrateWorktreePinsToMainRepo()
		// Default all repos to expanded on first load.
		groups := session.GroupByRepo(h.sessions)
		for repo := range groups {
			if _, exists := h.repoExpanded[repo]; !exists {
				h.repoExpanded[repo] = true
			}
		}
		// Also expand pinned repos that have no sessions.
		for repo := range h.pinnedRepos {
			if _, exists := h.repoExpanded[repo]; !exists {
				h.repoExpanded[repo] = true
			}
		}
		h.rebuildFlatItems()
		if len(h.flatItems) > 0 && h.cursor == 0 {
			h.cursor = FirstSelectableItem(h.flatItems)
		}

		// Apply the current preview size to all reconnected sessions so existing
		// tmux windows shrink to fit the preview pane on startup (without this,
		// they stay at whatever size the tmux server first booted at, often the
		// full host terminal width).
		if cols, rows := h.previewPaneSize(); cols > 0 {
			h.dispatchSessionResize(cols, rows)
		}

		// Start hook watcher.
		if h.hookWatcher == nil {
			if watcher, err := hooks.NewHookWatcher(); err == nil {
				h.hookWatcher = watcher
				go watcher.Start()
			}
		}

		// Start background status worker (once).
		if !h.workerStarted {
			h.workerStarted = true
			go h.statusWorker()

			// Initialize analytics (once, after first load).
			analytics.Init(h.cfg.IsTelemetryEnabled())
			effectiveTheme := h.cfg.Theme
			if effectiveTheme == "" {
				effectiveTheme = "tokyo-night"
			}
			analytics.TrackAppStarted(
				h.version,
				len(h.sessions),
				len(groups),
				effectiveTheme,
				h.cfg.GetEnterMode(),
				h.cfg.IsAutoNameEnabled(),
				h.cfg.IsCopyClaudeSettingsEnabled(),
			)
		}

		// Start listening for hook changes.
		if h.hookWatcher != nil {
			return h, h.listenForHookChanges
		}
		return h, nil
	}

	return h, nil
}

// View implements tea.Model.
func (h *Home) View() string {
	if h.isAttaching.Load() {
		return ""
	}
	if h.width == 0 {
		return lipgloss.NewStyle().Bold(true).Foreground(ColorAccent).Render("   fleet")
	}
	base := h.renderBody()
	toast := h.toasts.View(h.width)
	if toast == "" {
		return base
	}
	// Bottom-right, with a 1-cell right margin and a 1-row lift so the toast
	// clears the help-bar baseline.
	return overlay.Composite(toast, base, overlay.Right, overlay.Bottom, -1, -1)
}

func (h *Home) renderBody() string {
	// Modals take priority.
	if h.helpOverlay.IsVisible() {
		return h.helpOverlay.View()
	}
	if h.bugReport.IsVisible() {
		return h.bugReport.View()
	}
	if h.settingsDialog.IsVisible() {
		return h.settingsDialog.View()
	}
	if h.createWorkspaceDialog.IsVisible() {
		return h.createWorkspaceDialog.View()
	}
	if h.worktreeDialog.IsVisible() {
		return h.worktreeDialog.View()
	}
	if h.branchDialog.IsVisible() {
		return h.branchDialog.View()
	}
	if h.commandPalette.IsVisible() {
		return h.commandPalette.View()
	}
	if h.newDialog.IsVisible() {
		return h.newDialog.View()
	}
	if h.confirmDialog.IsVisible() {
		return h.confirmDialog.View()
	}
	if h.renameDialog.IsVisible() {
		return h.renameDialog.View()
	}

	var b strings.Builder

	// Snapshot gitInfoCache + sessionGitInfo under lock — the worker goroutine
	// writes to both concurrently, and View() must not read them without the lock.
	h.workerMu.Lock()
	gitInfoSnap := make(map[string]*git.RepoInfo, len(h.gitInfoCache))
	for k, v := range h.gitInfoCache {
		gitInfoSnap[k] = v
	}
	sessionInfoSnap := make(map[string]*git.RepoInfo, len(h.sessionGitInfo))
	for k, v := range h.sessionGitInfo {
		sessionInfoSnap[k] = v
	}
	h.workerMu.Unlock()

	// Header.
	header := h.renderHeader()
	b.WriteString(header)
	b.WriteString("\n")

	// Content area.
	contentHeight := h.height - 2 - helpBarHeight // header + help bar
	if contentHeight < 1 {
		contentHeight = 1
	}

	switch h.layoutMode() {
	case "single":
		sidebar := RenderSidebar(h.flatItems, h.sessions, gitInfoSnap, sessionInfoSnap, h.slotBindings, h.cursor, h.viewOffset, h.width, contentHeight)
		b.WriteString(sidebar)
	case "stacked":
		sidebarHeight := (contentHeight * 55) / 100
		if sidebarHeight < 3 {
			sidebarHeight = 3
		}
		previewHeight := contentHeight - sidebarHeight - 1 // 1 for separator
		sidebar := RenderSidebar(h.flatItems, h.sessions, gitInfoSnap, sessionInfoSnap, h.slotBindings, h.cursor, h.viewOffset, h.width, sidebarHeight)
		b.WriteString(sidebar)
		b.WriteString("\n")
		b.WriteString(DimStyle.Render(strings.Repeat("─", h.width)))
		b.WriteString("\n")
		s, content := h.selectedPreview()
		preview := RenderPreview(s, content, h.repoInfoFromSnap(gitInfoSnap, sessionInfoSnap), h.width, previewHeight, h.focusMode)
		b.WriteString(preview)
	default: // dual
		sidebarWidth := h.width * 35 / 100
		if sidebarWidth < 20 {
			sidebarWidth = 20
		}
		previewWidth := h.width - sidebarWidth - 3 // 3 for separator " │ "

		// In focus mode, reuse cached sidebar to avoid expensive rebuild on every keystroke.
		var leftPanel string
		if h.focusMode && !h.sidebarDirty && h.cachedSidebar != "" {
			leftPanel = h.cachedSidebar
		} else {
			leftPanel = RenderSidebar(h.flatItems, h.sessions, gitInfoSnap, sessionInfoSnap, h.slotBindings, h.cursor, h.viewOffset, sidebarWidth, contentHeight)
			leftPanel = ensureExactHeight(leftPanel, contentHeight)
			leftPanel = ensureExactWidth(leftPanel, sidebarWidth)
			h.cachedSidebar = leftPanel
			h.sidebarDirty = false
		}

		s, content := h.selectedPreview()
		rightPanel := RenderPreview(s, content, h.repoInfoFromSnap(gitInfoSnap, sessionInfoSnap), previewWidth, contentHeight, h.focusMode)

		// Build separator as explicit lines.
		sepColor := ColorBorder
		if h.focusMode {
			sepColor = ColorAccent
		}
		sepStyle := lipgloss.NewStyle().Foreground(sepColor)
		sepLines := make([]string, contentHeight)
		for i := range sepLines {
			sepLines[i] = sepStyle.Render(" │ ")
		}
		separator := strings.Join(sepLines, "\n")

		// Ensure exact dimensions before joining (prevents ANSI misalignment).
		rightPanel = ensureExactHeight(rightPanel, contentHeight)
		rightPanel = ensureExactWidth(rightPanel, previewWidth)

		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, separator, rightPanel))
	}

	// Pad to fill content area.
	lineCount := strings.Count(b.String(), "\n") + 1
	for lineCount < h.height-helpBarHeight {
		b.WriteString("\n")
		lineCount++
	}

	// Focus mode bar / Filter bar / Help bar.
	if h.focusMode {
		border := lipgloss.NewStyle().Foreground(ColorAccent).Render(strings.Repeat("─", h.width))
		b.WriteString("\n")
		b.WriteString(border + "\n")
		b.WriteString(" " + HelpKeyStyle.Render("esc") + " " + HelpDescStyle.Render("Unfocus") + "  " +
			DimStyle.Render("all keys forwarded to session"))
		lineCount += 2 // border + shortcut line
	} else if h.filterActive {
		border := lipgloss.NewStyle().Foreground(ColorBorder).Render(strings.Repeat("─", h.width))
		b.WriteString("\n")
		b.WriteString(border + "\n")
		b.WriteString(" " + HelpKeyStyle.Render("/") + " " + h.filterInput.View())
		lineCount += 2
	} else if h.filterText != "" {
		// Show active filter indicator even when not typing.
		border := lipgloss.NewStyle().Foreground(ColorBorder).Render(strings.Repeat("─", h.width))
		b.WriteString("\n")
		b.WriteString(border + "\n")
		b.WriteString(" " + HelpKeyStyle.Render("/") + " " + DimStyle.Render(h.filterText) + "  " + DimStyle.Render("(/ to edit, esc to clear)"))
		lineCount += 2
	} else {
		// Help bar (border + "\n " + shortcuts = 2 lines).
		b.WriteString("\n")
		b.WriteString(h.renderHelpBar())
		lineCount += 2
	}

	// Track height mismatches (counter for bug report, log only on first occurrence).
	// Uses incremental lineCount instead of re-scanning the output.
	if h.height > 0 && lineCount != h.height {
		diff := lineCount - h.height
		prevCount := h.renderStats.HeightMismatchCount
		h.renderStats.RecordHeightMismatch(diff)
		if prevCount == 0 {
			debuglog.Logger.Warn("View height mismatch detected",
				"output_lines", lineCount,
				"expected", h.height,
				"diff", diff,
				"layout", h.layoutMode(),
			)
		}
	}

	return b.String()
}

// --- Key handling ---

func (h *Home) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Route to active dialog/overlay.
	if h.helpOverlay.IsVisible() {
		overlay, cmd := h.helpOverlay.Update(msg)
		h.helpOverlay = overlay
		return h, cmd
	}
	if h.bugReport.IsVisible() {
		dialog, cmd := h.bugReport.Update(msg)
		h.bugReport = dialog
		return h, cmd
	}
	if h.settingsDialog.IsVisible() {
		dialog, cmd := h.settingsDialog.Update(msg)
		h.settingsDialog = dialog
		return h, cmd
	}
	if h.createWorkspaceDialog.IsVisible() {
		dialog, cmd := h.createWorkspaceDialog.Update(msg)
		h.createWorkspaceDialog = dialog
		return h, cmd
	}
	if h.worktreeDialog.IsVisible() {
		dialog, cmd := h.worktreeDialog.Update(msg)
		h.worktreeDialog = dialog
		return h, cmd
	}
	if h.branchDialog.IsVisible() {
		dialog, cmd := h.branchDialog.Update(msg)
		h.branchDialog = dialog
		return h, cmd
	}
	if h.commandPalette.IsVisible() {
		dialog, cmd := h.commandPalette.Update(msg)
		h.commandPalette = dialog
		return h, cmd
	}
	if h.newDialog.IsVisible() {
		dialog, cmd := h.newDialog.Update(msg)
		h.newDialog = dialog
		return h, cmd
	}
	if h.confirmDialog.IsVisible() {
		dialog, cmd := h.confirmDialog.Update(msg)
		h.confirmDialog = dialog
		return h, cmd
	}
	if h.renameDialog.IsVisible() {
		dialog, cmd := h.renameDialog.Update(msg)
		h.renameDialog = dialog
		return h, cmd
	}

	// Focus mode: forward keys to tmux session.
	if h.focusMode {
		return h.handleFocusKey(msg)
	}

	// Filter mode: route keys to filter input.
	if h.filterActive {
		switch msg.String() {
		case "esc":
			h.filterActive = false
			h.filterText = ""
			h.filterInput.SetValue("")
			h.filterInput.Blur()
			h.rebuildFlatItems()
			// Reset cursor to first item.
			if len(h.flatItems) > 0 {
				h.cursor = FirstSelectableItem(h.flatItems)
			}
			h.syncViewport()
			return h, h.fetchPreviewForSelected()
		case "enter":
			// Accept filter and exit filter mode.
			h.filterActive = false
			h.filterInput.Blur()
			return h, nil
		default:
			var cmd tea.Cmd
			h.filterInput, cmd = h.filterInput.Update(msg)
			h.filterText = h.filterInput.Value()
			h.rebuildFlatItems()
			// Reset cursor when filter changes.
			if len(h.flatItems) > 0 {
				h.cursor = FirstSelectableItem(h.flatItems)
			} else {
				h.cursor = 0
			}
			h.syncViewport()
			if previewCmd := h.fetchPreviewForSelected(); previewCmd != nil {
				return h, tea.Batch(cmd, previewCmd)
			}
			return h, cmd
		}
	}

	// Snapshot and clear the double-tap window: only a consecutive digit press
	// should attach, so any other key falling through this switch invalidates
	// the window for free. The digit case restores the snapshot before jumping.
	prevSlotTapSlot := h.lastSlotTapSlot
	prevSlotTapAt := h.lastSlotTapAt
	h.lastSlotTapSlot = -1

	switch msg.String() {
	case "j", "down":
		h.cursor = NextSelectableItem(h.flatItems, h.cursor, 1)
		h.syncViewport()
		return h, h.fetchPreviewForSelected()
	case "k", "up":
		h.cursor = NextSelectableItem(h.flatItems, h.cursor, -1)
		h.syncViewport()
		return h, h.fetchPreviewForSelected()
	case "pgdown":
		target := h.cursor + h.sidebarPanelRows()
		if target > len(h.flatItems)-1 {
			target = len(h.flatItems) - 1
		}
		if target < 0 {
			target = 0
		}
		h.cursor = target
		h.syncViewport()
		return h, h.fetchPreviewForSelected()
	case "pgup":
		target := h.cursor - h.sidebarPanelRows()
		if target < 0 {
			target = 0
		}
		h.cursor = target
		h.syncViewport()
		return h, h.fetchPreviewForSelected()
	case "shift+down":
		h.moveCursorItem(1)
		return h, h.fetchPreviewForSelected()
	case "shift+up":
		h.moveCursorItem(-1)
		return h, h.fetchPreviewForSelected()
	case "enter":
		// Toggle repo group or attach session.
		if h.cursor >= 0 && h.cursor < len(h.flatItems) && h.flatItems[h.cursor].IsRepoHeader {
			h.toggleRepoGroup()
			return h, nil
		}
		if h.cfg.GetEnterMode() == "split" {
			return h, h.enterFocusMode()
		}
		if s := h.selectedSession(); s != nil {
			h.actionLog.Add("attach session", s.Title, true)
			analytics.Track(analytics.EventSessionAttached, nil)
		}
		return h, h.attachSelected()
	case "tab":
		if h.cursor >= 0 && h.cursor < len(h.flatItems) && h.flatItems[h.cursor].IsRepoHeader {
			return h, nil
		}
		if h.cfg.GetEnterMode() == "split" {
			if s := h.selectedSession(); s != nil {
				h.actionLog.Add("attach session", s.Title, true)
			}
			return h, h.attachSelected()
		}
		return h, h.enterFocusMode()
	case " ":
		// Jump to next waiting (or finished) session.
		h.jumpToNextAttentionSession()
		analytics.Track(analytics.EventSpaceJump, nil)
		return h, h.fetchPreviewForSelected()
	case "left", "h":
		h.collapseRepoAtCursor()
		return h, nil
	case "right", "l":
		h.expandRepoAtCursor()
		return h, nil
	case "a":
		// Instant session at current repo path.
		repoPath := h.resolveCurrentRepo()
		if repoPath == "" {
			h.newDialog.Show()
			return h, nil
		}
		repoName := filepath.Base(repoPath)
		h.actionLog.Add("create session", repoPath, true)
		return h.handleSessionCreate(sessionCreateMsg{
			path:  repoPath,
			title: repoName,
		})
	case "n":
		// Worktree-by-default: spin up a `claude/<8hex>` worktree in the repo
		// at the cursor without any dialog. The historic path-picker lives on
		// in the palette as "New Session at Path".
		return h.startWorktreeSessionAtCursor()
	case "f":
		return h, h.forkSelected()
	case "d":
		// Handle d on empty pinned repo header: unpin directly.
		if h.cursor >= 0 && h.cursor < len(h.flatItems) && h.flatItems[h.cursor].IsRepoHeader {
			item := h.flatItems[h.cursor]
			if h.countSessionsForRepo(item.RepoPath) == 0 && h.pinnedRepos[item.RepoPath] {
				delete(h.pinnedRepos, item.RepoPath)
				if err := h.storage.UnpinRepo(item.RepoPath); err != nil {
					debuglog.Logger.Error("failed to unpin repo", "repo", item.RepoPath, "err", err)
				}
				// Drop any explicit sidebar ordering for this repo so a future
				// re-pin/re-add starts from the alphabetical fallback rather
				// than its old slot.
				if _, ok := h.repoOrder[item.RepoPath]; ok {
					delete(h.repoOrder, item.RepoPath)
					if err := h.storage.DeleteRepoOrder(item.RepoPath); err != nil {
						debuglog.Logger.Error("failed to delete repo order", "repo", item.RepoPath, "err", err)
					}
				}
				h.actionLog.Add("unpin repo", filepath.Base(item.RepoPath), true)
				h.rebuildFlatItems()
				// Fix cursor if it's now out of bounds.
				if h.cursor >= len(h.flatItems) {
					h.cursor = len(h.flatItems) - 1
				}
				if h.cursor < 0 {
					h.cursor = 0
				}
				return h, nil
			}
			return h, nil // non-empty repo header, ignore
		}
		if s := h.selectedSession(); s != nil {
			h.actionLog.Add("delete session", s.Title, true)
		}
		return h, h.confirmDeleteSelected()
	case "z":
		return h.undoDelete()
	case "r":
		if s := h.selectedSession(); s != nil {
			h.actionLog.Add("restart session", s.Title, true)
			analytics.Track(analytics.EventSessionRestarted, nil)
		}
		return h, h.restartSelected()
	case "R":
		return h, h.renameSelected()
	case "e":
		if s := h.selectedSession(); s != nil {
			h.actionLog.Add("open editor", fmt.Sprintf("%q at %s", h.cfg.GetEditor(), s.ProjectPath), true)
			analytics.Track(analytics.EventEditorOpened, map[string]interface{}{"editor": h.cfg.GetEditor()})
		}
		return h, h.openEditorSelected()
	case "p":
		h.actionLog.Add("open PR/MR", "", true)
		analytics.Track(analytics.EventPROpened, nil)
		return h, h.openPRInBrowser()
	case "Y":
		if s := h.selectedSession(); s != nil {
			h.actionLog.Add("quick approve", s.Title, true)
			analytics.Track(analytics.EventQuickApprove, nil)
		}
		return h, h.quickApproveSelected()
	case "b":
		repoPath := h.resolveCurrentRepo()
		if repoPath == "" {
			return h, nil
		}
		h.branchDialog.ShowLoading()
		return h, tea.Batch(h.fetchBranchList(repoPath), spinnerTickCmd)
	case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
		digit := int(msg.String()[0] - '0')
		switch h.slotAssignMode {
		case 1:
			h.slotAssignMode = 0
			h.bindCurrentSessionToSlot(digit)
			return h, nil
		case 2:
			h.slotAssignMode = 0
			h.unbindSlot(digit)
			return h, nil
		}
		// Restore double-tap state so two consecutive digit presses attach.
		h.lastSlotTapSlot = prevSlotTapSlot
		h.lastSlotTapAt = prevSlotTapAt
		return h.jumpToSlot(digit)
	case "alt+0", "alt+1", "alt+2", "alt+3", "alt+4", "alt+5", "alt+6", "alt+7", "alt+8", "alt+9":
		s := msg.String()
		digit := int(s[len(s)-1] - '0')
		h.bindCurrentSessionToSlot(digit)
		return h, nil
	case "=":
		switch h.slotAssignMode {
		case 0:
			h.slotAssignMode = 1
			h.setInfo("Slot: digit=bind · = again=unbind · Esc=cancel")
		case 1:
			h.slotAssignMode = 2
			h.setInfo("Unbind slot: digit=clear · Esc=cancel")
		default:
			h.slotAssignMode = 0
			h.setInfo("Slot assign cancelled")
			return h, nil
		}
		h.slotAssignExpires = time.Now().Add(2 * time.Second)
		return h, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return slotAssignTimeoutMsg{} })
	case "/":
		h.filterActive = true
		h.filterInput.Focus()
		analytics.Track(analytics.EventFilterUsed, nil)
		return h, nil
	case "esc":
		// Cancel pending slot-assign leader.
		if h.slotAssignMode != 0 {
			h.slotAssignMode = 0
			h.setInfo("Slot assign cancelled")
			return h, nil
		}
		// Clear active filter.
		if h.filterText != "" {
			h.filterText = ""
			h.filterInput.SetValue("")
			h.rebuildFlatItems()
			if len(h.flatItems) > 0 {
				h.cursor = FirstSelectableItem(h.flatItems)
			}
			h.syncViewport()
			return h, h.fetchPreviewForSelected()
		}
		return h, nil
	case ":", "ctrl+p":
		h.commandPalette.Show(h.buildPaletteCommands())
		analytics.Track(analytics.EventCommandPalette, nil)
		return h, nil
	case "S":
		h.settingsDialog.Show()
		analytics.Track(analytics.EventSettingsOpened, nil)
		return h, nil
	case "!":
		h.actionLog.Add("open diagnostics", "", true)
		h.bugReport.Show(h.version, len(h.sessions), h.errorHistory, h.actionLog, h.width, h.height)
		analytics.Track(analytics.EventBugReportOpened, nil)
		return h, nil
	case "D":
		s := h.selectedSession()
		if s == nil {
			return h, nil
		}
		h.actionLog.Add("status snapshot", s.Title, true)
		return h, func() tea.Msg {
			return captureStatusSnapshot(s, s.ID)
		}
	case "?":
		h.helpOverlay.Show()
		return h, nil
	case "q", "ctrl+c":
		h.cancel() // stops background worker
		if h.hookWatcher != nil {
			h.hookWatcher.Stop()
		}
		if h.controlClient != nil {
			h.controlClient.Close()
		}
		// Finalize all pending deletes before quitting.
		h.finalizeAllPendingDeletes()
		analytics.Track(analytics.EventAppQuit, map[string]interface{}{
			"uptime_seconds": int(time.Since(h.startTime).Seconds()),
		})
		analytics.Shutdown()
		return h, tea.Quit
	}

	return h, nil
}

// --- Session operations ---

func (h *Home) markSessionAccessed(s *session.Session) {
	s.MarkAccessed()
	if err := h.storage.UpdateLastAccessed(s.ID); err != nil {
		debuglog.Logger.Error("storage: UpdateLastAccessed", "id", s.ID, "err", err)
	}
}

func (h *Home) attachSelected() tea.Cmd {
	if h.cursor < 0 || h.cursor >= len(h.flatItems) || h.flatItems[h.cursor].IsRepoHeader {
		return nil
	}
	s := h.flatItems[h.cursor].Session
	if s == nil || !s.IsAlive() {
		return nil
	}

	h.markSessionAccessed(s)
	s.Acknowledge()
	if err := h.storage.SetAcknowledged(s.ID, true); err != nil {
		debuglog.Logger.Error("storage: SetAcknowledged", "id", s.ID, "err", err)
	}

	h.isAttaching.Store(true)

	previewCols, previewRows := h.previewPaneSize()
	cmd := attachCmd{
		session:     s.GetTmuxSession(),
		termCols:    h.width,
		termRows:    h.height,
		previewCols: previewCols,
		previewRows: previewRows,
	}
	return tea.Exec(cmd, func(err error) tea.Msg {
		// CRITICAL: Clear isAttaching before returning the message.
		// Prevents race where View() returns empty string after detach.
		h.isAttaching.Store(false)
		return statusUpdateMsg{attachedSessionID: s.ID}
	})
}

type attachCmd struct {
	session *tmux.Session
	// termCols/termRows: host terminal size — tmux is resized up to this
	// before attach so the user sees a full-screen pane instead of one stuck
	// at the preview-pane width.
	termCols, termRows int
	// previewCols/previewRows: fleet preview size — tmux is resized back to
	// this on detach so the next capture-pane fits without truncation.
	previewCols, previewRows int
}

func (a attachCmd) Run() error {
	if a.termCols > 0 && a.termRows > 0 {
		_ = a.session.ResizeWindow(a.termCols, a.termRows)
	}
	err := a.session.Attach(context.Background())
	if a.previewCols > 0 && a.previewRows > 0 {
		_ = a.session.ResizeWindow(a.previewCols, a.previewRows)
	}
	return err
}

func (a attachCmd) SetStdin(r io.Reader)  {}
func (a attachCmd) SetStdout(w io.Writer) {}
func (a attachCmd) SetStderr(w io.Writer) {}

// claudeBranchExisting is the regexp shape of a fleet-managed branch that
// hasn't been renamed yet — `claude/<8 lowercase hex>`. The auto-name worker
// uses this to decide whether to attempt a `git branch -m` to `claude/<slug>`.
var claudeBranchExistingRE = regexp.MustCompile(`^claude/[0-9a-f]{8}$`)

// startWorktreeSessionAtCursor handles the `n` key: resolves the repo at the
// cursor (using cursor-locality semantics) and dispatches a workspaceCreateMsg
// for a `claude/<8hex>` worktree against the main repo. If the cursor isn't on
// anything resolvable (e.g. empty sidebar), falls back to the path-picker.
func (h *Home) startWorktreeSessionAtCursor() (tea.Model, tea.Cmd) {
	repo := h.resolveCurrentRepo()
	if repo == "" {
		// No cursor target — show the path-picker so the user can type a path.
		h.newDialog.Show()
		return h, nil
	}
	return h.startWorktreeSessionForRepo(repo)
}

// startWorktreeSessionForRepo creates an instant worktree-backed session for
// the given repo path (which may be either the main repo or any worktree
// underneath it — GetMainRepo normalises). Generates a `claude/<8hex>` branch
// with a collision check, then drops through to the existing workspaceCreateMsg
// pipeline (phantom entry, background create, sessionCreate on success).
func (h *Home) startWorktreeSessionForRepo(repo string) (tea.Model, tea.Cmd) {
	mainRepo := session.GetMainRepo(repo)
	if mainRepo == "" {
		mainRepo = repo
	}

	// Non-git path → fall back to the no-worktree session (matches the
	// dialog's own fallback when the path isn't a git work tree).
	if !isGitWorkTree(mainRepo) {
		h.actionLog.Add("create session", mainRepo, true)
		return h.handleSessionCreate(sessionCreateMsg{
			path:  mainRepo,
			title: filepath.Base(mainRepo),
		})
	}

	hex, err := randHex8()
	if err != nil {
		h.setError(fmt.Errorf("worktree create: %w", err))
		return h, nil
	}
	branch := "claude/" + hex
	name := "claude-" + hex

	// Branch collision check — re-roll up to 4 times then suffix `-1`..`-5`.
	branch = ensureFreshBranch(mainRepo, branch)

	provider := workspace.ResolveProvider(mainRepo)
	if provider == nil {
		h.setError(fmt.Errorf("no workspace provider for repo"))
		return h, nil
	}
	h.actionLog.Add("create worktree session", branch, true)
	return h, func() tea.Msg {
		return workspaceCreateMsg{
			name:       name,
			branch:     branch,
			baseBranch: "", // current HEAD — git worktree add defaults to it
			repoPath:   mainRepo,
			provider:   provider,
		}
	}
}

// ensureFreshBranch returns the first branch name from {branch, branch+"-1",
// …, branch+"-5"} that doesn't already exist as a local ref. If all five are
// taken, returns the last candidate anyway and lets `git worktree add` surface
// the error to the user. Cheap operation: each probe is `git show-ref --verify`,
// returning instantly when the ref doesn't exist.
func ensureFreshBranch(repoPath, branch string) string {
	candidate := branch
	for i := 1; i <= 5; i++ {
		cmd := exec.Command("git", "-C", repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+candidate)
		if err := cmd.Run(); err != nil {
			// Non-zero exit (or command failure) → ref doesn't exist → free.
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", branch, i)
	}
	return candidate
}

// randHex8 returns 4 random bytes encoded as 8 lowercase hex characters.
// Used for the `claude/<8hex>` branch name. Mirrors generateID's hex-of-4-bytes
// pattern so the namespace is consistent across fleet's own ID generation.
func randHex8() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

func (h *Home) handleSessionCreate(msg sessionCreateMsg) (tea.Model, tea.Cmd) {
	if _, err := exec.LookPath("claude"); err != nil {
		h.setError(fmt.Errorf("claude CLI not found — install Claude Code to create sessions"))
		return h, nil
	}
	debuglog.Logger.Info("creating session", "title", msg.title, "path", msg.path)
	s := session.NewSession(msg.title, msg.path)
	s.WorkspaceName = msg.workspaceName
	// Boot the new tmux window at preview-pane size so Claude wraps to fit from
	// the very first render — avoids a brief moment where output is too wide.
	if cols, rows := h.previewPaneSize(); cols > 0 {
		s.SetPreferredSize(cols, rows)
	}
	autoFocus := h.cfg.IsFocusOnNewSessionEnabled()
	return h, func() tea.Msg {
		if err := s.Start(); err != nil {
			debuglog.Logger.Error("session Start() failed", "title", msg.title, "path", msg.path, "err", err)
			return sessionCreateResultMsg{err: err}
		}
		return sessionCreateResultMsg{session: s, autoFocus: autoFocus}
	}
}

func (h *Home) handleSessionCreateResult(msg sessionCreateResultMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		h.setError(fmt.Errorf("failed to start session: %w", msg.err))
		return h, nil
	}

	analytics.Track(analytics.EventSessionCreated, nil)

	s := msg.session
	h.workerMu.Lock()
	h.sessions = append(h.sessions, s)
	h.rebuildSessionMap()
	h.workerMu.Unlock()

	// Ensure the repo group is expanded for the new session and pin the main
	// repo (so worktree sessions don't pin their own worktree path — they
	// group under the main repo above them).
	repo := session.GetMainRepo(s.ProjectPath)
	h.repoExpanded[repo] = true
	if !h.pinnedRepos[repo] {
		h.pinnedRepos[repo] = true
		if err := h.storage.PinRepo(repo); err != nil {
			debuglog.Logger.Error("failed to pin repo", "repo", repo, "err", err)
		}
	}
	h.rebuildFlatItems()

	// Save to storage.
	if err := h.storage.SaveSession(s.ToRow()); err != nil {
		h.setError(fmt.Errorf("failed to save session: %w", err))
	}

	// Auto-select the new session.
	for i, item := range h.flatItems {
		if !item.IsRepoHeader && item.Session != nil && item.Session.ID == s.ID {
			h.cursor = i
			h.syncViewport()
			break
		}
	}

	// Auto-focus the freshly created session if the user opted in. Uses
	// the existing Enter mode setting so the post-create behavior matches
	// what pressing Enter on the session would do.
	if msg.autoFocus {
		if h.cfg.GetEnterMode() == "split" {
			h.actionLog.Add("focus preview", s.Title, true)
			return h, h.enterFocusMode()
		}
		h.actionLog.Add("attach session", s.Title, true)
		analytics.Track(analytics.EventSessionAttached, nil)
		return h, h.attachSelected()
	}

	return h, h.fetchPreviewForSelected()
}

func (h *Home) confirmDeleteSelected() tea.Cmd {
	if h.cursor < 0 || h.cursor >= len(h.flatItems) || h.flatItems[h.cursor].IsRepoHeader {
		return nil
	}
	s := h.flatItems[h.cursor].Session
	if s == nil {
		return nil
	}

	id := s.ID
	wsName := s.WorkspaceName
	repoPath := session.GetRepoRoot(s.ProjectPath)
	isLastInRepo := h.countSessionsForRepo(repoPath) == 1 && h.pinnedRepos[repoPath]
	hasDestroyableWorkspace := false

	if wsName != "" {
		provider := workspace.ResolveProvider(repoPath)
		hasDestroyableWorkspace = provider.CanDestroy()
	}

	details := []string{
		"Press z to undo within 5s",
	}
	if isLastInRepo {
		details = append(details, "Last session in this repo")
	}

	onYes := func() tea.Msg {
		return sessionDeleteMsg{id: id}
	}
	onRemoveRepo := func() tea.Msg {
		return sessionDeleteMsg{id: id, unpinRepo: true, repoPath: repoPath}
	}

	switch {
	case hasDestroyableWorkspace && isLastInRepo:
		onYesWS := func() tea.Msg {
			return sessionDeleteMsg{id: id, destroyWorkspace: true, workspaceName: wsName}
		}
		h.confirmDialog.ShowDangerLastInRepoWithWorkspace("Delete Session?", s.Title, details, wsName, onYes, onYesWS, onRemoveRepo)
	case hasDestroyableWorkspace:
		onYesWS := func() tea.Msg {
			return sessionDeleteMsg{id: id, destroyWorkspace: true, workspaceName: wsName}
		}
		h.confirmDialog.ShowDangerWithWorkspace("Delete Session?", s.Title, details, wsName, onYes, onYesWS)
	case isLastInRepo:
		h.confirmDialog.ShowDangerLastInRepo("Delete Session?", s.Title, details, onYes, onRemoveRepo)
	default:
		h.confirmDialog.ShowDanger("Delete Session?", s.Title, details, onYes)
	}
	return nil
}

func (h *Home) restartSelected() tea.Cmd {
	if h.cursor < 0 || h.cursor >= len(h.flatItems) || h.flatItems[h.cursor].IsRepoHeader {
		return nil
	}
	s := h.flatItems[h.cursor].Session
	if s == nil {
		return nil
	}

	h.markSessionAccessed(s)
	id := s.ID
	title := s.Title
	debuglog.Logger.Info("restarting session", "id", id, "title", title)
	return func() tea.Msg {
		var err error
		if s.IsAlive() && !s.GetTmuxSession().IsPaneDead() {
			// Tmux session alive, just respawn the pane.
			err = s.RespawnClaude()
			if err != nil {
				debuglog.Logger.Error("RespawnClaude failed", "id", id, "err", err)
			}
		} else {
			// Tmux session dead or pane dead — full restart.
			err = s.Restart()
			if err != nil {
				debuglog.Logger.Error("Restart failed", "id", id, "err", err)
			}
		}
		return sessionRestartMsg{id: id, err: err}
	}
}

func (h *Home) forkSelected() tea.Cmd {
	s := h.selectedSession()
	if s == nil {
		h.setError(fmt.Errorf("cannot fork: no session selected"))
		return nil
	}
	if s.ClaudeSessionID == "" {
		h.setError(fmt.Errorf("cannot fork: session has no Claude conversation ID yet"))
		return nil
	}
	title := s.Title + " (fork)"
	claudeSessionID := s.ClaudeSessionID
	path := s.ProjectPath
	workspaceName := s.WorkspaceName
	return func() tea.Msg {
		return forkSessionMsg{
			parentClaudeSessionID: claudeSessionID,
			path:                  path,
			title:                 title,
			workspaceName:         workspaceName,
		}
	}
}

func (h *Home) toggleRepoGroup() {
	if h.cursor < 0 || h.cursor >= len(h.flatItems) || !h.flatItems[h.cursor].IsRepoHeader {
		return
	}
	repo := h.flatItems[h.cursor].RepoPath
	h.repoExpanded[repo] = !h.repoExpanded[repo]
	h.rebuildFlatItems()
	// Keep cursor on the same repo header.
	for i, item := range h.flatItems {
		if item.IsRepoHeader && item.RepoPath == repo {
			h.cursor = i
			break
		}
	}
	h.syncViewport()
}

func (h *Home) expandRepoAtCursor() {
	if h.cursor < 0 || h.cursor >= len(h.flatItems) {
		return
	}
	item := h.flatItems[h.cursor]
	var repo string
	if item.IsRepoHeader {
		repo = item.RepoPath
	} else if item.Session != nil {
		// Sidebar headers key off the main repo (GroupByRepo) — translate the
		// session's project path so the expanded-state lookup matches.
		repo = session.GetMainRepo(item.Session.ProjectPath)
	} else {
		return
	}
	if h.repoExpanded[repo] {
		return // Already expanded.
	}
	h.repoExpanded[repo] = true
	h.rebuildFlatItems()
	h.syncViewport()
}

func (h *Home) collapseRepoAtCursor() {
	if h.cursor < 0 || h.cursor >= len(h.flatItems) {
		return
	}
	item := h.flatItems[h.cursor]
	var repo string
	if item.IsRepoHeader {
		repo = item.RepoPath
	} else if item.Session != nil {
		repo = session.GetMainRepo(item.Session.ProjectPath)
	} else {
		return
	}
	if !h.repoExpanded[repo] {
		return // Already collapsed.
	}
	h.repoExpanded[repo] = false
	h.rebuildFlatItems()
	// Move cursor to the repo header.
	for i, fi := range h.flatItems {
		if fi.IsRepoHeader && fi.RepoPath == repo {
			h.cursor = i
			break
		}
	}
	h.syncViewport()
}

// jumpToNextAttentionSession cycles through sessions needing attention:
// waiting first, then finished. Wraps around, auto-expands collapsed groups.
func (h *Home) jumpToNextAttentionSession() {
	// Build ordered list of ALL sessions (same order as sidebar).
	groups := session.GroupByRepo(h.sessions)
	repos := make([]string, 0, len(groups))
	for repo := range groups {
		repos = append(repos, repo)
	}
	sort.Strings(repos)

	type candidate struct {
		s    *session.Session
		repo string
	}
	var allSessions []candidate
	for _, repo := range repos {
		for _, s := range groups[repo] {
			allSessions = append(allSessions, candidate{s: s, repo: repo})
		}
	}
	if len(allSessions) == 0 {
		return
	}

	// Find the current session's position in allSessions.
	var currentID string
	if h.cursor >= 0 && h.cursor < len(h.flatItems) && !h.flatItems[h.cursor].IsRepoHeader {
		if s := h.flatItems[h.cursor].Session; s != nil {
			currentID = s.ID
		}
	}
	currentIdx := -1
	for i, c := range allSessions {
		if c.s.ID == currentID {
			currentIdx = i
			break
		}
	}

	// findNext scans forward (wrapping) for a session with the given status.
	findNext := func(status session.Status) *candidate {
		n := len(allSessions)
		start := currentIdx + 1
		for i := 0; i < n; i++ {
			c := &allSessions[(start+i)%n]
			if c.s.GetStatus() == status {
				return c
			}
		}
		return nil
	}

	// Priority: waiting > finished.
	target := findNext(session.StatusWaiting)
	if target == nil {
		target = findNext(session.StatusFinished)
	}
	if target == nil {
		return // Silent no-op.
	}

	// Expand the repo group if collapsed.
	h.repoExpanded[target.repo] = true
	h.rebuildFlatItems()

	// Set cursor to the target session.
	for i, item := range h.flatItems {
		if !item.IsRepoHeader && item.Session != nil && item.Session.ID == target.s.ID {
			h.cursor = i
			h.syncViewport()
			return
		}
	}
}

func (h *Home) renameSelected() tea.Cmd {
	if h.cursor < 0 || h.cursor >= len(h.flatItems) || h.flatItems[h.cursor].IsRepoHeader {
		return nil
	}
	s := h.flatItems[h.cursor].Session
	if s == nil {
		return nil
	}
	h.renameDialog.Show(s.ID, s.Title)
	return nil
}

func (h *Home) openEditorSelected() tea.Cmd {
	if h.cursor < 0 || h.cursor >= len(h.flatItems) || h.flatItems[h.cursor].IsRepoHeader {
		return nil
	}
	s := h.flatItems[h.cursor].Session
	if s == nil {
		return nil
	}
	parts := strings.Fields(h.cfg.GetEditor())
	if len(parts) == 0 {
		return func() tea.Msg {
			return openEditorMsg{err: fmt.Errorf("no editor configured")}
		}
	}
	projectPath := s.ProjectPath
	return func() tea.Msg {
		args := append(parts[1:], projectPath)
		cmd := exec.Command(parts[0], args...)
		if err := cmd.Start(); err != nil {
			debuglog.Logger.Error("editor launch failed", "editor", parts[0], "args", args, "err", err)
			return openEditorMsg{err: err}
		}
		return openEditorMsg{}
	}
}

func (h *Home) quickApproveSelected() tea.Cmd {
	if h.cursor < 0 || h.cursor >= len(h.flatItems) || h.flatItems[h.cursor].IsRepoHeader {
		return nil
	}
	s := h.flatItems[h.cursor].Session
	if s == nil || !s.IsAlive() {
		return nil
	}
	if s.GetStatus() != session.StatusWaiting {
		h.setError(fmt.Errorf("session not waiting for approval"))
		return nil
	}
	h.markSessionAccessed(s)
	ts := s.GetTmuxSession()
	debuglog.Logger.Info("quick approve", "id", s.ID, "title", s.Title)
	return func() tea.Msg {
		// Send "y" then Enter: menu-style prompts ignore "y" and Enter confirms;
		// (Y/n) and (y/N) prompts accept "y" as approval, Enter submits.
		_ = ts.SendKeys("y")
		err := ts.SendKeys("Enter")
		return quickApproveMsg{err: err}
	}
}

// --- Focus mode (split preview) ---

func (h *Home) getControlClient() *tmux.ControlClient {
	if h.controlClient == nil || h.controlClient.IsClosed() {
		cc, err := tmux.NewControlClient()
		if err != nil {
			debuglog.Logger.Error("failed to create control client", "err", err)
			return nil
		}
		h.controlClient = cc
	}
	return h.controlClient
}

func (h *Home) enterFocusMode() tea.Cmd {
	s := h.selectedSession()
	if s == nil || !s.IsAlive() {
		h.setError(fmt.Errorf("cannot focus: session not running"))
		return nil
	}
	h.focusMode = true
	h.sidebarDirty = true // separator color changes
	h.actionLog.Add("focus preview", s.Title, true)
	return h.focusTick()
}

func (h *Home) focusTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return focusTickMsg(t)
	})
}

// focusKeySend is one tmux send-keys invocation produced for a focused-pane
// keypress. When literal is true the value is sent verbatim (`send-keys -l`);
// otherwise it is a tmux key name, optionally with an "M-"/"C-" modifier
// prefix (`send-keys`).
type focusKeySend struct {
	literal bool
	val     string
}

// allASCIILetters reports whether rs is non-empty and every rune is an ASCII
// letter — i.e. safe to embed in a tmux "M-<rune>" key name without tripping
// the control-mode command parser.
func allASCIILetters(rs []rune) bool {
	if len(rs) == 0 {
		return false
	}
	for _, r := range rs {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
	}
	return true
}

// translateFocusKey maps a bubbletea key event onto the tmux send-keys
// invocation(s) needed to reproduce it inside the focused session. It returns
// (nil, true) when the key should instead exit focus mode (Esc).
//
// Modified keys must keep their modifier: Alt/Meta keys go through with the
// tmux "M-" prefix (M-BSpace, M-Left, M-a, ...) so word-wise line editing
// works in the focused pane instead of degrading to an unmodified keypress.
// Alt+<non-letter rune> can't safely use "M-<rune>" (tmux's command parser
// treats `;`, quotes, `#`, ... specially), so it falls back to ESC followed by
// the rune sent literally.
func translateFocusKey(msg tea.KeyMsg) (sends []focusKeySend, unfocus bool) {
	if msg.Type == tea.KeyEsc {
		return nil, true
	}
	named := func(v string) []focusKeySend { return []focusKeySend{{val: v}} }

	if msg.Alt {
		switch msg.Type {
		case tea.KeyRunes:
			if allASCIILetters(msg.Runes) {
				for _, r := range msg.Runes {
					sends = append(sends, focusKeySend{val: "M-" + string(r)})
				}
				return sends, false
			}
			// ESC then the rune(s) sent literally (-l → quoteTmux), which is
			// what Alt+<rune> is at the terminal level anyway.
			return []focusKeySend{{val: "Escape"}, {literal: true, val: string(msg.Runes)}}, false
		case tea.KeyBackspace:
			return named("M-BSpace"), false
		case tea.KeyDelete:
			return named("M-DC"), false
		case tea.KeyLeft:
			return named("M-Left"), false
		case tea.KeyRight:
			return named("M-Right"), false
		case tea.KeyUp:
			return named("M-Up"), false
		case tea.KeyDown:
			return named("M-Down"), false
		case tea.KeyEnter:
			return named("M-Enter"), false
		default:
			// Alt+X == ESC then X: emit Escape, then translate the bare key.
			rest, _ := translateFocusKey(tea.KeyMsg{Type: msg.Type, Runes: msg.Runes})
			return append(named("Escape"), rest...), false
		}
	}

	switch msg.Type {
	case tea.KeyEnter:
		return named("Enter"), false
	case tea.KeyBackspace:
		return named("BSpace"), false
	case tea.KeyTab:
		return named("Tab"), false
	case tea.KeyShiftTab:
		return named("BTab"), false
	case tea.KeySpace:
		return named("Space"), false
	case tea.KeyUp:
		return named("Up"), false
	case tea.KeyDown:
		return named("Down"), false
	case tea.KeyLeft:
		return named("Left"), false
	case tea.KeyRight:
		return named("Right"), false
	case tea.KeyCtrlLeft:
		return named("C-Left"), false
	case tea.KeyCtrlRight:
		return named("C-Right"), false
	case tea.KeyCtrlUp:
		return named("C-Up"), false
	case tea.KeyCtrlDown:
		return named("C-Down"), false
	case tea.KeyHome:
		return named("Home"), false
	case tea.KeyEnd:
		return named("End"), false
	case tea.KeyPgUp:
		return named("PageUp"), false
	case tea.KeyPgDown:
		return named("PageDown"), false
	case tea.KeyDelete:
		return named("DC"), false
	case tea.KeyCtrlC:
		return named("C-c"), false
	case tea.KeyCtrlD:
		return named("C-d"), false
	case tea.KeyCtrlA:
		return named("C-a"), false
	case tea.KeyCtrlU:
		return named("C-u"), false
	case tea.KeyCtrlL:
		return named("C-l"), false
	case tea.KeyCtrlW:
		return named("C-w"), false
	case tea.KeyCtrlK:
		return named("C-k"), false
	case tea.KeyRunes:
		return []focusKeySend{{literal: true, val: string(msg.Runes)}}, false
	default:
		if str := msg.String(); str != "" {
			return []focusKeySend{{literal: true, val: str}}, false
		}
		return nil, false
	}
}

// handleFocusKey forwards a keypress to the focused session's tmux pane, or
// exits focus mode on Esc.
//
// The tmux sends run synchronously here rather than from a tea.Cmd on purpose:
// Bubble Tea processes KeyMsgs sequentially, so staying in Update() keeps
// forwarded keystrokes in order — concurrent tea.Cmds would not preserve that.
// Each send is a sub-millisecond write to the long-lived `tmux -C` control
// client, not a shell-out.
func (h *Home) handleFocusKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := h.selectedSession()
	if s == nil || !s.IsAlive() {
		h.focusMode = false
		h.sidebarDirty = true
		return h, nil
	}

	sends, unfocus := translateFocusKey(msg)
	debuglog.Logger.Debug("focus key",
		"type", int(msg.Type), "typeStr", msg.Type.String(), "alt", msg.Alt,
		"runes", string(msg.Runes), "string", msg.String(),
		"sends", fmt.Sprintf("%+v", sends), "unfocus", unfocus)
	if unfocus {
		h.focusMode = false
		h.sidebarDirty = true
		h.actionLog.Add("unfocus preview", s.Title, true)
		return h, nil
	}

	cc := h.getControlClient()
	if cc == nil {
		h.setError(fmt.Errorf("failed to connect to tmux"))
		h.focusMode = false
		h.sidebarDirty = true
		return h, nil
	}

	target := s.GetTmuxSession().Name
	for _, sk := range sends {
		if sk.literal {
			cc.SendLiteralKeys(target, sk.val)
		} else {
			cc.SendKeys(target, sk.val)
		}
	}
	return h, nil
}

func (h *Home) fetchPreviewFresh(s *session.Session) tea.Cmd {
	id := s.ID
	ts := s.GetTmuxSession()
	return func() tea.Msg {
		content, _ := ts.CapturePaneFresh()
		return previewMsg{sessionID: id, content: content}
	}
}

func (h *Home) openPRInBrowser() tea.Cmd {
	repo := h.resolveCurrentRepo()
	if repo == "" {
		debuglog.Logger.Debug("openPR: no repo selected")
		h.setError(fmt.Errorf("no repo selected"))
		return nil
	}

	// Prefer the selected worktree-session's own PR (e.g. an MR opened for
	// `claude/foo`) over the group header's PR (which tracks the main repo's
	// current branch). Falls back to the header when no session is selected
	// or the per-session PR hasn't been fetched yet.
	mainRepo := session.GetMainRepo(repo)
	var info *git.RepoInfo
	h.workerMu.Lock()
	if s := h.selectedSession(); s != nil {
		if si, ok := h.sessionGitInfo[s.ID]; ok && si != nil && si.PR != nil && si.PR.URL != "" {
			info = si
		}
	}
	if info == nil {
		info = h.gitInfoCache[mainRepo]
	}
	h.workerMu.Unlock()
	if info == nil || info.PR == nil || info.PR.URL == "" {
		debuglog.Logger.Debug("openPR: no PR/MR for branch", "repo", repo)
		h.setError(fmt.Errorf("no PR/MR for this branch"))
		return nil
	}

	prURL := info.PR.URL
	repoName := filepath.Base(mainRepo)

	return func() tea.Msg {
		// Try Chrome extension first.
		client := &chrome.Client{}
		cmd := &chrome.Command{
			ID:     fmt.Sprintf("pr-%d", time.Now().UnixNano()),
			Action: chrome.ActionOpenOrFocus,
			URL:    prURL,
			Group:  repoName,
		}

		_, err := client.Send(cmd)
		if err != nil {
			// Fallback to macOS open command.
			debuglog.Logger.Debug("chrome extension unavailable, falling back to open", "err", err)
			if openErr := exec.Command("open", prURL).Start(); openErr != nil {
				debuglog.Logger.Error("failed to open PR/MR in browser", "url", prURL, "err", openErr)
				return openPRMsg{err: fmt.Errorf("open PR/MR: %w", openErr)}
			}
		}
		return openPRMsg{}
	}
}

// deferDelete removes a session from the UI and DB but defers tmux/hook/workspace
// cleanup for the undo window. Returns a tick command for expiry.
func (h *Home) deferDelete(msg sessionDeleteMsg) (tea.Model, tea.Cmd) {
	s, ok := h.sessionByID[msg.id]
	if !ok {
		return h, nil
	}

	debuglog.Logger.Info("deferred delete", "id", msg.id, "title", s.Title)

	// Snapshot DB row before deleting.
	row := s.ToRow()
	repoPath := session.GetRepoRoot(s.ProjectPath)

	// Delete from SQLite immediately (crash-safe).
	if err := h.storage.DeleteSession(msg.id); err != nil {
		debuglog.Logger.Error("failed to delete session from storage", "id", msg.id, "err", err)
	}

	// Handle repo unpin if requested.
	if msg.unpinRepo {
		delete(h.pinnedRepos, msg.repoPath)
		if err := h.storage.UnpinRepo(msg.repoPath); err != nil {
			debuglog.Logger.Error("failed to unpin repo", "repo", msg.repoPath, "err", err)
		}
	}

	// Clear any slot binding pointing at this session. FK cascade drops the
	// DB row (triggered by the DeleteSession above), but the in-memory map
	// needs explicit cleanup so the [N] badge disappears from the sidebar.
	// Slot bindings do NOT survive undo: restoring the session via `z` leaves
	// it unbound, and the user can re-press Alt+<N> to rebind.
	for slot, sid := range h.slotBindings {
		if sid == msg.id {
			delete(h.slotBindings, slot)
		}
	}
	if h.lastSlotTapSlot >= 0 {
		if sid, ok := h.slotBindings[h.lastSlotTapSlot]; !ok || sid == msg.id {
			h.lastSlotTapSlot = -1
		}
	}

	// Drop per-session git info (worker will not re-populate after delete).
	h.workerMu.Lock()
	delete(h.sessionGitInfo, msg.id)
	h.workerMu.Unlock()

	// Remove from in-memory session list.
	var remaining []*session.Session
	for _, sess := range h.sessions {
		if sess.ID != msg.id {
			remaining = append(remaining, sess)
		}
	}
	h.sessions = remaining
	h.rebuildSessionMap()
	h.rebuildFlatItems()

	// Fix cursor.
	if h.cursor >= len(h.flatItems) {
		h.cursor = len(h.flatItems) - 1
	}
	if h.cursor < 0 {
		h.cursor = 0
	}
	if len(h.flatItems) > 0 && h.flatItems[h.cursor].IsRepoHeader {
		h.cursor = NextSelectableItem(h.flatItems, h.cursor, 1)
	}

	// Generate nonce for timer matching.
	nonce := fmt.Sprintf("%s-%d", msg.id, time.Now().UnixNano())

	// Push onto undo stack.
	h.pendingDeletes = append(h.pendingDeletes, PendingDelete{
		Nonce:         nonce,
		Session:       s,
		Row:           row,
		RepoPath:      repoPath,
		DestroyWS:     msg.destroyWorkspace,
		WorkspaceName: msg.workspaceName,
		UnpinRepo:     msg.unpinRepo,
		DeletedAt:     time.Now(),
	})

	// Show undo flash.
	h.setInfo(h.buildUndoFlashMessage())

	// Start expiry timer.
	return h, tea.Tick(undoDeleteTimeout, func(t time.Time) tea.Msg {
		return pendingDeleteExpireMsg{nonce: nonce}
	})
}

// undoDelete restores the most recent pending delete.
func (h *Home) undoDelete() (tea.Model, tea.Cmd) {
	if len(h.pendingDeletes) == 0 {
		return h, nil
	}

	// Pop most recent.
	pd := h.pendingDeletes[len(h.pendingDeletes)-1]
	h.pendingDeletes = h.pendingDeletes[:len(h.pendingDeletes)-1]

	debuglog.Logger.Info("undo delete", "id", pd.Session.ID, "title", pd.Session.Title)

	// Re-insert into SQLite.
	if err := h.storage.SaveSession(pd.Row); err != nil {
		h.setError(fmt.Errorf("undo failed: %w", err))
		return h, nil
	}

	// Re-pin repo if it was unpinned.
	if pd.UnpinRepo {
		h.pinnedRepos[pd.RepoPath] = true
		if err := h.storage.PinRepo(pd.RepoPath); err != nil {
			debuglog.Logger.Error("failed to re-pin repo on undo", "repo", pd.RepoPath, "err", err)
		}
	}

	// Re-add to session list (tmux is still alive).
	h.workerMu.Lock()
	h.sessions = append(h.sessions, pd.Session)
	h.rebuildSessionMap()
	h.workerMu.Unlock()

	// Expand repo group and rebuild sidebar.
	h.repoExpanded[pd.RepoPath] = true
	h.rebuildFlatItems()

	// Move cursor to restored session.
	for i, item := range h.flatItems {
		if !item.IsRepoHeader && item.Session != nil && item.Session.ID == pd.Session.ID {
			h.cursor = i
			h.syncViewport()
			break
		}
	}

	h.actionLog.Add("undo delete", pd.Session.Title, true)
	h.setInfo(fmt.Sprintf("Restored %q", pd.Session.Title))
	return h, nil
}

// handlePendingDeleteExpire finalizes a deferred delete after the undo window.
func (h *Home) handlePendingDeleteExpire(msg pendingDeleteExpireMsg) (tea.Model, tea.Cmd) {
	// Find by nonce.
	idx := -1
	for i, pd := range h.pendingDeletes {
		if pd.Nonce == msg.nonce {
			idx = i
			break
		}
	}
	if idx < 0 {
		return h, nil // already undone
	}

	pd := h.pendingDeletes[idx]
	h.pendingDeletes = append(h.pendingDeletes[:idx], h.pendingDeletes[idx+1:]...)
	// Move into finalizingDeletes so an in-flight cleanup is visible to
	// finalizeAllPendingDeletes if the user quits mid-finalize.
	h.finalizingDeletes = append(h.finalizingDeletes, pd)

	return h, h.finalizeDelete(pd)
}

// finalizeDelete schedules cleanup (tmux kill, hook removal, optional
// workspace destruction) on a background goroutine. All steps shell out or
// hit the filesystem, so they must stay off the Bubble Tea Update loop —
// otherwise an undo-window expiry blocks keystroke processing for the
// duration of `tmux kill-session`. Always returns deleteCleanupDoneMsg so
// the entry can be removed from finalizingDeletes.
func (h *Home) finalizeDelete(pd PendingDelete) tea.Cmd {
	return func() tea.Msg {
		debuglog.Logger.Info("finalizing delete", "id", pd.Session.ID, "title", pd.Session.Title)

		if pd.Session.IsAlive() {
			if err := pd.Session.Kill(); err != nil {
				debuglog.Logger.Error("failed to kill tmux session", "id", pd.Session.ID, "err", err)
			}
		}

		if err := os.Remove(filepath.Join(hooks.GetHooksDir(), pd.Session.ID+".json")); err != nil && !os.IsNotExist(err) {
			debuglog.Logger.Error("failed to remove hook status file", "id", pd.Session.ID, "err", err)
		}

		var workspaceErr error
		if pd.DestroyWS && pd.WorkspaceName != "" {
			provider := workspace.ResolveProvider(pd.RepoPath)
			if provider != nil && provider.CanDestroy() {
				workspaceErr = provider.Destroy(pd.RepoPath, pd.WorkspaceName)
			}
		}
		return deleteCleanupDoneMsg{sessionID: pd.Session.ID, workspaceErr: workspaceErr}
	}
}

// finalizeAllPendingDeletes synchronously drains both pendingDeletes (undo
// window still open) and finalizingDeletes (cleanup goroutine in flight) on
// quit. tmux kill and hook-file removal are idempotent and run for both lists.
// Workspace Destroy is NOT idempotent (GitWorktreeProvider.Destroy errors when
// the worktree is already gone, and a concurrent run would race with the
// in-flight goroutine's `git worktree remove`), so it only runs for
// pendingDeletes — finalizingDeletes entries already have a goroutine
// responsible for the destroy.
func (h *Home) finalizeAllPendingDeletes() {
	finalize := func(pd PendingDelete, destroyWorkspace bool) {
		debuglog.Logger.Info("finalizing pending delete on quit", "id", pd.Session.ID, "title", pd.Session.Title)
		if pd.Session.IsAlive() {
			if err := pd.Session.Kill(); err != nil {
				debuglog.Logger.Error("failed to kill tmux session on quit", "id", pd.Session.ID, "err", err)
			}
		}
		if err := os.Remove(filepath.Join(hooks.GetHooksDir(), pd.Session.ID+".json")); err != nil && !os.IsNotExist(err) {
			debuglog.Logger.Error("failed to remove hook status file on quit", "id", pd.Session.ID, "err", err)
		}
		if destroyWorkspace && pd.DestroyWS && pd.WorkspaceName != "" {
			provider := workspace.ResolveProvider(pd.RepoPath)
			if provider != nil && provider.CanDestroy() {
				if err := provider.Destroy(pd.RepoPath, pd.WorkspaceName); err != nil {
					debuglog.Logger.Error("failed to destroy workspace on quit", "id", pd.Session.ID, "workspace", pd.WorkspaceName, "err", err)
				}
			}
		}
	}

	for _, pd := range h.pendingDeletes {
		finalize(pd, true)
	}
	for _, pd := range h.finalizingDeletes {
		finalize(pd, false)
	}
	h.pendingDeletes = nil
	h.finalizingDeletes = nil
}

// buildUndoFlashMessage builds the flash message for the undo prompt.
func (h *Home) buildUndoFlashMessage() string {
	n := len(h.pendingDeletes)
	if n == 0 {
		return ""
	}
	last := h.pendingDeletes[n-1]
	title := last.Session.Title
	if n == 1 {
		return fmt.Sprintf("Deleted %q. z to undo", title)
	}
	return fmt.Sprintf("Deleted %q. z to undo (%d pending)", title, n)
}

// countSessionsForRepo counts live sessions belonging to the given repo group.
// The input is normalised to its main repo so callers can pass either a
// worktree path or the main repo and get the same answer — sessions group by
// main repo in the sidebar.
func (h *Home) countSessionsForRepo(repoPath string) int {
	main := session.GetMainRepo(repoPath)
	count := 0
	for _, s := range h.sessions {
		if session.GetMainRepo(s.ProjectPath) == main {
			count++
		}
	}
	return count
}

// --- Tick / status ---

func (h *Home) tick() tea.Cmd {
	interval := tickInterval
	if h.cfg != nil && h.cfg.TickIntervalSec > 0 {
		interval = time.Duration(h.cfg.TickIntervalSec) * time.Second
	}
	return tea.Tick(interval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (h *Home) previewTick() tea.Cmd {
	return tea.Tick(previewTickInterval, func(t time.Time) tea.Msg {
		return previewTickMsg(t)
	})
}

// listenForHookChanges blocks until the HookWatcher signals a status change,
// then returns a hookChangedMsg. Runs as a tea.Cmd in its own goroutine.
func (h *Home) listenForHookChanges() tea.Msg {
	if h.hookWatcher == nil {
		return nil
	}
	select {
	case <-h.hookWatcher.Changes():
		return hookChangedMsg{}
	case <-h.ctx.Done():
		return nil
	}
}

func (h *Home) handleTick() (tea.Model, tea.Cmd) {
	// Trigger background worker (non-blocking).
	select {
	case h.statusTrigger <- struct{}{}:
	default: // worker busy, skip
	}

	// Read worker results under lock and rebuild.
	h.workerMu.Lock()
	h.rebuildFlatItems()
	h.workerMu.Unlock()

	// Preview is now handled by the faster previewTick, no need to fetch here.
	return h, h.tick()
}

// statusWorker runs in its own goroutine, performing all blocking I/O
// (tmux, git, gh) outside the Bubble Tea Update() loop.
func (h *Home) statusWorker() {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-h.ctx.Done():
			return
		case <-h.statusTrigger:
		case <-ticker.C:
		}

		h.statusWorkerCycle()
	}
}

// syncHookStatuses reads the latest hook statuses from the HookWatcher and applies
// them to the given sessions. Caller must ensure thread-safe access to sessions.
// Returns the IDs of sessions whose hook meaningfully changed (new status or timestamp);
// callers can forward these to priorityStatusUpdates for immediate UpdateStatus().
func (h *Home) syncHookStatuses(sessions []*session.Session) []string {
	if h.hookWatcher == nil {
		return nil
	}
	var changed []string
	for _, s := range sessions {
		hs := h.hookWatcher.GetStatus(s.ID)
		if hs != nil {
			oldClaudeSessionID := s.ClaudeSessionID
			oldFirstPrompt := s.FirstPrompt
			oldPromptCount := s.PromptCount
			if s.UpdateHookStatus(&session.HookStatus{
				Status:      hs.Status,
				SessionID:   hs.SessionID,
				UpdatedAt:   hs.UpdatedAt,
				UserPrompt:  hs.UserPrompt,
				PromptCount: hs.PromptCount,
			}) {
				changed = append(changed, s.ID)
			}
			// Persist new Claude session ID if it changed.
			if s.ClaudeSessionID != oldClaudeSessionID && s.ClaudeSessionID != "" {
				if err := h.storage.UpdateClaudeSessionID(s.ID, s.ClaudeSessionID); err != nil {
					debuglog.Logger.Error("storage: UpdateClaudeSessionID", "id", s.ID, "err", err)
				}
			}
			// Persist prompt changes and reset title on every new prompt
			// (for non-manually-renamed, non-Claude-named sessions).
			if s.PromptCount != oldPromptCount {
				h.markSessionAccessed(s)
				if err := h.storage.UpdatePromptCount(s.ID, s.PromptCount); err != nil {
					debuglog.Logger.Error("storage: UpdatePromptCount", "id", s.ID, "err", err)
				}
				if h.cfg.IsAutoNameEnabled() && s.TitleGenerated && !s.ManuallyRenamed && s.ClaudeSessionName == "" {
					s.TitleGenerated = false
					if err := h.storage.ResetTitleGenerated(s.ID); err != nil {
						debuglog.Logger.Error("storage: ResetTitleGenerated", "id", s.ID, "err", err)
					}
				}
			}
			if s.FirstPrompt != "" && s.FirstPrompt != oldFirstPrompt {
				if err := h.storage.UpdateFirstPrompt(s.ID, s.FirstPrompt); err != nil {
					debuglog.Logger.Error("storage: UpdateFirstPrompt", "id", s.ID, "err", err)
				}
			}
		}
	}
	return changed
}

// updateAndPersistStatus runs a full UpdateStatus() on the session and persists
// the result to storage if the status changed. Called from the worker goroutine.
func (h *Home) updateAndPersistStatus(s *session.Session) {
	oldStatus := s.GetStatus()
	s.UpdateStatus()
	newStatus := s.GetStatus()
	if oldStatus != newStatus {
		if err := h.storage.UpdateStatus(s.ID, string(newStatus)); err != nil {
			debuglog.Logger.Error("storage: UpdateStatus", "id", s.ID, "status", newStatus, "err", err)
		}
	}
}

// enqueuePriorityUpdates pushes session IDs into the worker's priority queue
// and kicks the worker to drain them immediately. Safe to call from any goroutine.
func (h *Home) enqueuePriorityUpdates(ids []string) {
	if len(ids) == 0 {
		return
	}
	for _, id := range ids {
		select {
		case h.priorityStatusUpdates <- id:
		default:
			// Queue full — next worker cycle's round-robin will still catch it.
		}
	}
	select {
	case h.statusTrigger <- struct{}{}:
	default:
	}
}

func (h *Home) statusWorkerCycle() {
	// Recover from panics to keep the worker alive.
	defer func() {
		if r := recover(); r != nil {
			debuglog.Logger.Error("statusWorkerCycle panic recovered", "panic", r)
		}
	}()

	// 1. Refresh tmux session cache (blocking but in background).
	tmux.RefreshSessionCache()

	// 2. Take a snapshot of sessions under lock.
	h.workerMu.Lock()
	sessions := make([]*session.Session, len(h.sessions))
	copy(sessions, h.sessions)
	h.workerMu.Unlock()

	if len(sessions) == 0 {
		return
	}

	// 3. Sync hook status (fast: in-memory map lookups).
	h.syncHookStatuses(sessions)

	// 3b. Auto-name: generate title for ONE session per cycle.
	// Priority: manual (R key) > Claude session name > last prompt heuristic.
	if h.cfg.IsAutoNameEnabled() {
		for _, s := range sessions {
			if s.ManuallyRenamed {
				continue
			}

			// Periodically re-read Claude's session name from JSONL (~every 30s per session).
			if s.ClaudeSessionID != "" && time.Since(s.ClaudeNameLastChecked) > 30*time.Second {
				s.ClaudeNameLastChecked = time.Now()
				name := session.ReadClaudeSessionName(s.ClaudeSessionID, s.ProjectPath)
				if name != "" && name != s.ClaudeSessionName {
					s.ClaudeSessionName = name
					s.Title = name
					if err := h.storage.UpdateTitle(s.ID, name); err != nil {
						debuglog.Logger.Error("storage: UpdateTitle (claude name)", "id", s.ID, "err", err)
					}
					s.TitleGenerated = true
					if err := h.storage.MarkTitleGenerated(s.ID); err != nil {
						debuglog.Logger.Error("storage: MarkTitleGenerated", "id", s.ID, "err", err)
					}
				}
			}
			if s.ClaudeSessionName != "" {
				continue
			}

			// Fallback: prompt-based title heuristic.
			if s.FirstPrompt != "" && !s.TitleGenerated {
				title := naming.GenerateTitle(s.FirstPrompt)
				if title != "" && title != s.Title {
					s.Title = title
					if err := h.storage.UpdateTitle(s.ID, title); err != nil {
						debuglog.Logger.Error("storage: UpdateTitle (auto-name)", "id", s.ID, "err", err)
					}
				}
				s.TitleGenerated = true
				if err := h.storage.MarkTitleGenerated(s.ID); err != nil {
					debuglog.Logger.Error("storage: MarkTitleGenerated", "id", s.ID, "err", err)
				}
				break // one per cycle
			}
		}
	}

	// 4. Priority updates first — sessions whose hook file just changed.
	// These bypass round-robin so the UI reflects fresh hook status within
	// ~100ms of the hook firing (vs. up to (N/statusRoundRobin)*tickInterval seconds).
	priorityIDs := make(map[string]bool)
drainPriority:
	for {
		select {
		case id := <-h.priorityStatusUpdates:
			priorityIDs[id] = true
		default:
			break drainPriority
		}
	}
	processed := make(map[string]bool, len(priorityIDs))
	for _, s := range sessions {
		if !priorityIDs[s.ID] {
			continue
		}
		h.updateAndPersistStatus(s)
		processed[s.ID] = true
	}

	// 5. Round-robin status updates (pane capture — blocking), skipping already-processed.
	count := statusRoundRobin
	if count > len(sessions) {
		count = len(sessions)
	}
	for i := 0; i < count; i++ {
		idx := (h.statusRRIndex + i) % len(sessions)
		s := sessions[idx]
		if processed[s.ID] {
			continue
		}
		h.updateAndPersistStatus(s)
	}
	h.statusRRIndex = (h.statusRRIndex + count) % len(sessions)

	// 5. Git info refresh: 1 repo per cycle (round-robin).
	repos := h.uniqueRepoPathsFromSessions(sessions)
	if len(repos) > 0 {
		idx := h.gitRRIndex % len(repos)
		repo := repos[idx]

		info := git.RefreshGitInfo(repo)

		// Preserve PR data unless TTL expired.
		h.workerMu.Lock()
		if old, ok := h.gitInfoCache[repo]; ok && old.PR != nil {
			info.PR = old.PR
			info.LastPRRefresh = old.LastPRRefresh
		}
		h.workerMu.Unlock()

		// Resolve the repo's forge once, then cache it (worker-only map).
		provider, seen := h.repoForge[repo]
		if !seen {
			provider = detectForge(repo)
			h.repoForge[repo] = provider
		}
		if provider != nil && (info.LastPRRefresh.IsZero() || time.Since(info.LastPRRefresh) > 60*time.Second) {
			git.RefreshPRInfo(info, repo, workspace.IgnorePatterns(repo), provider)
		}

		h.workerMu.Lock()
		h.gitInfoCache[repo] = info
		h.workerMu.Unlock()

		h.gitRRIndex++
	}

	// 6. Per-session git info refresh: 1 worktree-session per cycle. Skips
	// no-worktree sessions (GetRepoRoot == GetMainRepo) — those inherit the
	// group header's info and don't need their own row chrome.
	worktreeSessions := make([]*session.Session, 0, len(sessions))
	for _, s := range sessions {
		if session.GetRepoRoot(s.ProjectPath) != session.GetMainRepo(s.ProjectPath) {
			worktreeSessions = append(worktreeSessions, s)
		}
	}
	if len(worktreeSessions) > 0 {
		idx := h.sessionGitRRIndex % len(worktreeSessions)
		s := worktreeSessions[idx]
		repoPath := s.ProjectPath // worktree path — git commands run inside the worktree
		mainRepo := session.GetMainRepo(repoPath)

		info := git.RefreshGitInfo(repoPath)

		// Preserve prior PR unless TTL expired (60s) — matches the group cadence.
		h.workerMu.Lock()
		if old, ok := h.sessionGitInfo[s.ID]; ok && old.PR != nil {
			info.PR = old.PR
			info.LastPRRefresh = old.LastPRRefresh
		}
		h.workerMu.Unlock()

		// Forge provider is keyed by main repo: a worktree shares its main
		// repo's origin, so the cached lookup is correct.
		provider, seen := h.repoForge[mainRepo]
		if !seen {
			provider = detectForge(mainRepo)
			h.repoForge[mainRepo] = provider
		}
		if provider != nil && (info.LastPRRefresh.IsZero() || time.Since(info.LastPRRefresh) > 60*time.Second) {
			// IgnorePatterns reads .fleet.json — read it from the worktree so
			// per-worktree overrides work, but if absent the main-repo file
			// still applies via fleet's existing merge in ResolveConfig.
			git.RefreshPRInfo(info, repoPath, workspace.IgnorePatterns(repoPath), provider)
		}

		h.workerMu.Lock()
		h.sessionGitInfo[s.ID] = info
		h.workerMu.Unlock()

		h.sessionGitRRIndex++
	}

	// 7. Branch auto-rename: if the just-titled session is on a placeholder
	// `claude/<8hex>` branch, rename to `claude/<slug>` to make the branch
	// readable in `git log` and PR pages. The auto-name worker (above) sets
	// TitleGenerated once per session, so this block also fires at most once
	// per session — we don't keep churning the branch every prompt.
	if h.cfg.IsAutoNameEnabled() {
		for _, s := range sessions {
			if s.WorkspaceName == "" || !s.TitleGenerated || s.Title == "" || s.ManuallyRenamed {
				continue
			}
			// Need the current branch — read it from sessionGitInfo under lock.
			// If not yet populated, skip and retry next cycle: TitleGenerated is
			// sticky, so the rename eventually catches up.
			h.workerMu.Lock()
			si := h.sessionGitInfo[s.ID]
			h.workerMu.Unlock()
			if si == nil || si.Branch == "" {
				continue
			}
			if !claudeBranchExistingRE.MatchString(si.Branch) {
				continue // already renamed or never auto-created
			}
			slug := naming.BranchSlug(s.Title)
			if slug == "" {
				continue
			}
			newBranch := "claude/" + slug
			if newBranch == si.Branch {
				continue
			}
			renamed := false
			for attempt := 0; attempt < 5; attempt++ {
				candidate := newBranch
				if attempt > 0 {
					candidate = fmt.Sprintf("%s-%d", newBranch, attempt+1)
				}
				if err := git.RenameBranch(s.ProjectPath, si.Branch, candidate); err != nil {
					if strings.Contains(strings.ToLower(err.Error()), "already exists") {
						continue
					}
					debuglog.Logger.Debug("branch rename failed", "id", s.ID, "old", si.Branch, "new", candidate, "err", err)
					break
				}
				renamed = true
				debuglog.Logger.Info("branch renamed", "id", s.ID, "old", si.Branch, "new", candidate)
				break
			}
			if renamed {
				// Invalidate so next per-session tick re-fetches branch + PR mapping.
				h.workerMu.Lock()
				delete(h.sessionGitInfo, s.ID)
				h.workerMu.Unlock()
				break // one rename per cycle keeps the worker cheap
			}
		}
	}
}

// uniqueRepoPathsFromSessions returns distinct main-repo paths from the given
// sessions. The git-info round-robin uses main-repo keys so the group header
// reflects the main checkout's branch/dirty/PR — worktree-session rows show
// their own info via the per-session round-robin below.
func (h *Home) uniqueRepoPathsFromSessions(sessions []*session.Session) []string {
	seen := make(map[string]bool)
	var repos []string
	for _, s := range sessions {
		root := session.GetMainRepo(s.ProjectPath)
		if !seen[root] {
			seen[root] = true
			repos = append(repos, root)
		}
	}
	return repos
}

func (h *Home) resolveCurrentRepo() string {
	if h.cursor < 0 || h.cursor >= len(h.flatItems) {
		return ""
	}
	item := h.flatItems[h.cursor]
	if item.IsRepoHeader {
		return item.RepoPath
	}
	if item.Session != nil {
		return session.GetRepoRoot(item.Session.ProjectPath)
	}
	return ""
}

func (h *Home) fetchBranchList(repoPath string) tea.Cmd {
	return func() tea.Msg {
		branches, err := git.ListBranches(repoPath)
		isDirty := git.HasUncommittedChanges(repoPath)
		userEmail := git.GetUserEmail(repoPath)
		return branchListMsg{branches: branches, repoPath: repoPath, isDirty: isDirty, userEmail: userEmail, err: err}
	}
}

func (h *Home) fetchWorkspaceListForRepo(repoPath string) tea.Cmd {
	return func() tea.Msg {
		provider := workspace.ResolveProvider(repoPath)
		workspaces, err := provider.List(repoPath)
		defaultBranch := git.GetDefaultBranch(repoPath)
		return workspaceListMsg{workspaces: workspaces, provider: provider, repoPath: repoPath, defaultBranch: defaultBranch, err: err}
	}
}

// copyClaudeSettingsFile copies .claude/settings.local.json from srcRepo to dstRepo.
func copyClaudeSettingsFile(srcRepo, dstRepo string) {
	srcFile := filepath.Join(srcRepo, ".claude", "settings.local.json")
	data, err := os.ReadFile(srcFile)
	if err != nil {
		return // source doesn't exist, nothing to copy
	}
	dstDir := filepath.Join(dstRepo, ".claude")
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		debuglog.Logger.Error("copyClaudeSettings: failed to create .claude dir", "dst", dstDir, "err", err)
		return
	}
	if err := os.WriteFile(filepath.Join(dstDir, "settings.local.json"), data, 0600); err != nil {
		debuglog.Logger.Error("copyClaudeSettings: failed to write settings file", "dst", dstRepo, "err", err)
	}
}

func (h *Home) fetchPreview(s *session.Session) tea.Cmd {
	id := s.ID
	ts := s.GetTmuxSession()
	return func() tea.Msg {
		content, _ := ts.CapturePane()
		return previewMsg{sessionID: id, content: content}
	}
}

// fetchPreviewForSelected returns a tea.Cmd that fetches the preview for the
// currently selected session, or nil if no live session is selected.
func (h *Home) fetchPreviewForSelected() tea.Cmd {
	sel := h.selectedSession()
	if sel == nil || !sel.IsAlive() {
		return nil
	}
	return h.fetchPreview(sel)
}

// --- Rendering helpers ---

func (h *Home) renderHeader() string {
	statusCounts := make(map[session.Status]int)
	for _, s := range h.sessions {
		statusCounts[s.GetStatus()]++
	}

	bg := ColorSurface
	logo := lipgloss.NewStyle().Foreground(ColorBrand).Background(bg).Bold(true).Render(">_")
	title := logo + lipgloss.NewStyle().Background(bg).Render(" ") + TitleStyle.Background(bg).Render("fleet")

	// Build status indicators — only show non-zero.
	var indicators []string
	if n := statusCounts[session.StatusRunning] + statusCounts[session.StatusStarting]; n > 0 {
		indicators = append(indicators, StatusRunningStyle.Background(bg).Render(fmt.Sprintf("● %d running", n)))
	}
	if n := statusCounts[session.StatusWaiting]; n > 0 {
		indicators = append(indicators, StatusWaitingStyle.Background(bg).Render(fmt.Sprintf("◐ %d waiting", n)))
	}
	if n := statusCounts[session.StatusFinished]; n > 0 {
		indicators = append(indicators, StatusFinishedStyle.Background(bg).Render(fmt.Sprintf("● %d finished", n)))
	}
	if n := statusCounts[session.StatusIdle]; n > 0 {
		indicators = append(indicators, StatusIdleStyle.Background(bg).Render(fmt.Sprintf("○ %d idle", n)))
	}
	if n := statusCounts[session.StatusError]; n > 0 {
		indicators = append(indicators, StatusErrorStyle.Background(bg).Render(fmt.Sprintf("✕ %d error", n)))
	}

	sep := lipgloss.NewStyle().Foreground(ColorBorder).Background(bg).Render(" • ")
	stats := strings.Join(indicators, sep)

	sp := lipgloss.NewStyle().Background(bg).Render
	content := title + sp("  ") + stats

	// Manually pad to full width with background-styled spaces to avoid ANSI reset issues.
	if h.width > 0 {
		contentWidth := lipgloss.Width(content)
		if contentWidth < h.width {
			content += sp(strings.Repeat(" ", h.width-contentWidth))
		}
	}
	return HeaderBarStyle.Render(content)
}

func (h *Home) renderHelpBar() string {
	// Border line.
	border := lipgloss.NewStyle().Foreground(ColorBorder).Render(strings.Repeat("─", h.width))

	contextKeys, globalKeys := HelpBarBindings()

	var parts []string
	for _, kd := range contextKeys {
		parts = append(parts, HelpKeyStyle.Render(kd.Key)+" "+HelpDescStyle.Render(kd.Desc))
	}
	sep := HelpSepStyle.Render(" │ ")
	left := strings.Join(parts, "  ")

	var gparts []string
	for _, kd := range globalKeys {
		gparts = append(gparts, HelpKeyStyle.Render(kd.Key)+" "+HelpDescStyle.Render(kd.Desc))
	}
	right := strings.Join(gparts, "  ")

	return border + "\n " + left + sep + right
}

func (h *Home) layoutMode() string {
	if h.width < layoutBreakpointSingle {
		return "single"
	}
	if h.width < layoutBreakpointDual {
		return "stacked"
	}
	return "dual"
}

// previewPaneSize returns the cols × rows the fleet preview pane occupies in
// the current layout. tmux sessions are resized to match so Claude wraps to
// fit and the right side isn't chopped off by ansi.Truncate in preview.go.
// Returns (0, 0) before WindowSizeMsg has set valid dimensions; callers must
// skip resizing in that case. Mirrors the arithmetic in View() (app.go:828+).
func (h *Home) previewPaneSize() (cols, rows int) {
	if h.width <= 0 || h.height <= 0 {
		return 0, 0
	}
	contentHeight := h.height - 2 - helpBarHeight
	if contentHeight < 1 {
		contentHeight = 1
	}
	switch h.layoutMode() {
	case "single", "stacked":
		// In stacked mode the preview spans full width; in single mode no
		// preview is shown but a sensible width still helps if the user
		// switches layouts mid-session.
		return h.width, contentHeight
	default: // dual
		sidebarWidth := h.width * 35 / 100
		if sidebarWidth < 20 {
			sidebarWidth = 20
		}
		previewWidth := h.width - sidebarWidth - 3 // 3 for separator " │ "
		if previewWidth < 1 {
			previewWidth = 1
		}
		return previewWidth, contentHeight
	}
}

// dispatchSessionResize asynchronously resizes every tmux session to (cols × rows).
// Runs in a goroutine because tmux resize-window can take a few ms per session
// when the tmux server is busy, and the UI Update() loop must stay responsive.
func (h *Home) dispatchSessionResize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	h.workerMu.Lock()
	sessions := make([]*session.Session, len(h.sessions))
	copy(sessions, h.sessions)
	h.workerMu.Unlock()
	if len(sessions) == 0 {
		return
	}
	go func() {
		for _, s := range sessions {
			s.SetPreferredSize(cols, rows)
		}
	}()
}

// bindCurrentSessionToSlot persists the selected session under the given slot,
// replacing any prior binding for either the slot or the session. Re-binding
// the same session to its existing slot toggles the binding off (unbind).
func (h *Home) bindCurrentSessionToSlot(slot int) {
	s := h.selectedSession()
	if s == nil {
		h.setError(fmt.Errorf("select a session first"))
		return
	}
	if existing, ok := h.slotBindings[slot]; ok && existing == s.ID {
		h.unbindSlot(slot)
		return
	}
	if err := h.storage.BindSlot(slot, s.ID); err != nil {
		h.setError(fmt.Errorf("bind slot: %w", err))
		return
	}
	for k, v := range h.slotBindings {
		if v == s.ID {
			delete(h.slotBindings, k)
		}
	}
	h.slotBindings[slot] = s.ID
	h.actionLog.Add("bind slot", fmt.Sprintf("%d → %s", slot, s.Title), true)
	h.setInfo(fmt.Sprintf("Slot %d → %s", slot, s.Title))
	h.sidebarDirty = true
}

// unbindSlot clears the given slot's binding, if any.
func (h *Home) unbindSlot(slot int) {
	id, ok := h.slotBindings[slot]
	if !ok {
		h.setInfo(fmt.Sprintf("Slot %d already unbound", slot))
		return
	}
	title := id
	if s, ok := h.sessionByID[id]; ok {
		title = s.Title
	}
	if err := h.storage.UnbindSlot(slot); err != nil {
		h.setError(fmt.Errorf("unbind slot: %w", err))
		return
	}
	delete(h.slotBindings, slot)
	if h.lastSlotTapSlot == slot {
		h.lastSlotTapSlot = -1
	}
	h.actionLog.Add("unbind slot", fmt.Sprintf("%d (was %s)", slot, title), true)
	h.setInfo(fmt.Sprintf("Slot %d cleared", slot))
	h.sidebarDirty = true
}

// moveCursorItem reorders the item under the cursor within its parent group.
// dir is +1 for "down" (later in the sidebar) or -1 for "up". Behaviour:
//
//   - Session: swap with the adjacent session inside the same repo group.
//     Top-of-group + dir=-1 and bottom-of-group + dir=+1 are silent no-ops;
//     reorders never migrate a session across repo boundaries.
//   - Repo header: swap this repo's position with the neighbouring repo's.
//     First-repo + dir=-1 and last-repo + dir=+1 are silent no-ops.
//   - Pending phantom ("Creating…"): silent no-op.
//   - Filter active: info toast and no mutation (reordering a filtered slice
//     would rewrite keys for a partial view).
//
// All persistence happens synchronously in this handler — matches the
// BindSlot / PinRepo pattern of writing directly from the UI goroutine.
func (h *Home) moveCursorItem(dir int) {
	if h.filterText != "" {
		h.setInfo("Clear filter (Esc) to reorder")
		return
	}
	if h.cursor < 0 || h.cursor >= len(h.flatItems) {
		return
	}
	item := h.flatItems[h.cursor]
	switch {
	case item.Pending != nil:
		// Phantom workspaces aren't persisted yet; silently ignore.
		return
	case item.IsRepoHeader:
		h.moveRepoGroup(item.RepoPath, dir)
	case item.Session != nil:
		h.moveSessionInGroup(item.Session, dir)
	}
}

// moveSessionInGroup swaps a session with its same-repo neighbour in the
// given direction. Out-of-bounds moves are silent (no toast, no log) to match
// j/k navigation feel.
//
// Implementation note: the whole group is renumbered with fresh sequential keys
// (100, 200, …) in the post-swap order rather than swapping just the two
// affected sessions' keys. The "swap two, leave the rest at SortKey=0" approach
// silently broke the first reorder in any group: legacy 0-key siblings sort
// before the seeded pair in the global SortKey order, so the moved pair landed
// at the bottom of the group instead of swapping in place. Renumbering every
// group member gives every session an explicit key and keeps the swap local.
// Mirrors moveRepoGroup's renumber-every-repo approach.
func (h *Home) moveSessionInGroup(s *session.Session, dir int) {
	// GroupByRepo now keys by main repo, so look up the group via main repo
	// regardless of whether the session lives in a worktree or the main repo.
	repo := session.GetMainRepo(s.ProjectPath)
	groupSessions := session.GroupByRepo(h.sessions)[repo]
	idx := -1
	for i, gs := range groupSessions {
		if gs.ID == s.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	other := idx + dir
	if other < 0 || other >= len(groupSessions) {
		return // top/bottom of group — silent no-op, do not migrate across repos
	}

	// Build the post-swap ordering of the group, then assign fresh sequential
	// keys to every member.
	reordered := make([]*session.Session, len(groupSessions))
	copy(reordered, groupSessions)
	reordered[idx], reordered[other] = reordered[other], reordered[idx]

	// After the swap above, the originally-cursored session sits at `other`.
	movedID := reordered[other].ID
	for i, gs := range reordered {
		newKey := int64((i + 1) * 100)
		if gs.SortKey == newKey {
			continue
		}
		gs.SortKey = newKey
		if err := h.storage.UpdateSessionSortKey(gs.ID, newKey); err != nil {
			h.setError(fmt.Errorf("reorder session: %w", err))
			return
		}
	}

	// Re-sort the in-memory session list so the next rebuildFlatItems sees the
	// same order LoadSessions would produce after a restart.
	sort.SliceStable(h.sessions, func(i, j int) bool {
		if h.sessions[i].SortKey != h.sessions[j].SortKey {
			return h.sessions[i].SortKey < h.sessions[j].SortKey
		}
		return h.sessions[i].CreatedAt.Before(h.sessions[j].CreatedAt)
	})
	h.rebuildFlatItems()

	// Re-anchor the cursor to the moved session.
	for i, it := range h.flatItems {
		if !it.IsRepoHeader && it.Session != nil && it.Session.ID == movedID {
			h.cursor = i
			break
		}
	}
	h.syncViewport()
	h.actionLog.Add("reorder session", fmt.Sprintf("%s (%s)", s.Title, dirLabel(dir)), true)
}

// moveRepoGroup swaps a repo's sidebar position with the neighbour in the given
// direction. Out-of-bounds moves are silent.
//
// Implementation note: instead of swapping the two affected repos' sort_keys in
// place, we renumber every on-screen repo with a fresh sequential key (100,
// 200, …) reflecting the post-swap order. The older "swap with (idx+1)*100
// fallback seeding" approach silently no-op'd when the seeded value collided
// with an already-stored key on a third repo — e.g. moving an unseeded repo
// next to one whose stored key happened to equal (idx+1)*100 left both repos
// sharing the same key, falling through to the alphabetical tiebreaker.
func (h *Home) moveRepoGroup(repoPath string, dir int) {
	// Reproduce BuildFlatItems' repo ordering: same set (sessions ∪ pending ∪
	// pinned), same comparator. This is the authoritative "what's on screen" list.
	// BuildFlatItems groups sessions by main repo, so use the main repo here too
	// — otherwise worktree-rooted sessions would split into phantom rows.
	repoSet := make(map[string]struct{})
	for _, s := range h.sessions {
		repoSet[session.GetMainRepo(s.ProjectPath)] = struct{}{}
	}
	for _, pw := range h.pendingWorkspaces {
		repoSet[pw.RepoPath] = struct{}{}
	}
	for r := range h.pinnedRepos {
		repoSet[r] = struct{}{}
	}
	repos := make([]string, 0, len(repoSet))
	for r := range repoSet {
		repos = append(repos, r)
	}
	sort.SliceStable(repos, func(i, j int) bool {
		ki, kj := h.repoOrder[repos[i]], h.repoOrder[repos[j]]
		if ki != kj {
			return ki < kj
		}
		return repos[i] < repos[j]
	})

	idx := -1
	for i, r := range repos {
		if r == repoPath {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	other := idx + dir
	if other < 0 || other >= len(repos) {
		return // first/last repo — silent no-op
	}

	repos[idx], repos[other] = repos[other], repos[idx]
	movedRepo := repos[other]

	// Assign sequential keys to every on-screen repo in the new order. This
	// guarantees unique keys so the next move can't collide with a stale
	// fallback value.
	for i, r := range repos {
		newKey := int64((i + 1) * 100)
		if existing, ok := h.repoOrder[r]; ok && existing == newKey {
			continue
		}
		h.repoOrder[r] = newKey
		if err := h.storage.UpsertRepoOrder(r, newKey); err != nil {
			h.setError(fmt.Errorf("reorder repo: %w", err))
			return
		}
	}

	h.rebuildFlatItems()

	// Re-anchor the cursor to the moved repo header.
	for i, it := range h.flatItems {
		if it.IsRepoHeader && it.RepoPath == movedRepo {
			h.cursor = i
			break
		}
	}
	h.syncViewport()
	h.actionLog.Add("reorder repo", fmt.Sprintf("%s (%s)", filepath.Base(movedRepo), dirLabel(dir)), true)
}

func dirLabel(dir int) string {
	if dir < 0 {
		return "up"
	}
	return "down"
}

// jumpToSlot moves the cursor to the session bound at the given slot.
// A second press of the same slot within 400ms also attaches.
func (h *Home) jumpToSlot(slot int) (tea.Model, tea.Cmd) {
	sessID, ok := h.slotBindings[slot]
	if !ok {
		h.setInfo(fmt.Sprintf("Slot %d unbound", slot))
		return h, nil
	}
	s, ok := h.sessionByID[sessID]
	if !ok {
		delete(h.slotBindings, slot)
		_ = h.storage.UnbindSlot(slot)
		h.setError(fmt.Errorf("slot %d was stale, cleared", slot))
		return h, nil
	}

	// Expand the repo group if collapsed, so the session is visible and selectable.
	// The repoExpanded map is keyed by main-repo path (matches BuildFlatItems).
	repo := session.GetMainRepo(s.ProjectPath)
	if !h.repoExpanded[repo] {
		h.repoExpanded[repo] = true
		h.rebuildFlatItems()
	}

	idx := -1
	for i, item := range h.flatItems {
		if !item.IsRepoHeader && item.Session != nil && item.Session.ID == sessID {
			idx = i
			break
		}
	}
	if idx < 0 {
		// Likely hidden by an active filter.
		h.setInfo(fmt.Sprintf("Slot %d hidden by filter", slot))
		return h, nil
	}

	isDoubleTap := h.lastSlotTapSlot == slot &&
		time.Since(h.lastSlotTapAt) < 400*time.Millisecond
	h.cursor = idx
	h.syncViewport()
	if isDoubleTap {
		h.lastSlotTapSlot = -1
		h.actionLog.Add("attach via slot", fmt.Sprintf("%d", slot), true)
		return h, h.attachSelected()
	}
	h.lastSlotTapSlot = slot
	h.lastSlotTapAt = time.Now()
	return h, h.fetchPreviewForSelected()
}

func (h *Home) selectedSession() *session.Session {
	if h.cursor < 0 || h.cursor >= len(h.flatItems) || h.flatItems[h.cursor].IsRepoHeader {
		return nil
	}
	return h.flatItems[h.cursor].Session
}

func (h *Home) selectedPreview() (*session.Session, string) {
	s := h.selectedSession()
	if s == nil {
		return nil, ""
	}
	content := h.previewCache[s.ID]
	return s, content
}

// repoInfoFromSnap returns repo info for the selected session using snapshots
// of gitInfoCache (main-repo keyed) and sessionGitInfo (session-ID keyed).
// Safe to call from View() without holding workerMu.
//
// For worktree-backed sessions (GetRepoRoot != GetMainRepo) the per-session
// info wins, so the preview's branch/dirty/PR line reflects the worktree's
// own `claude/<slug>` branch rather than the main repo's current branch.
// No-worktree sessions fall back to the main-repo cache, matching the
// group-header chrome.
func (h *Home) repoInfoFromSnap(repoSnap, sessionSnap map[string]*git.RepoInfo) *git.RepoInfo {
	s := h.selectedSession()
	if s == nil {
		return nil
	}
	if session.GetRepoRoot(s.ProjectPath) != session.GetMainRepo(s.ProjectPath) {
		if info, ok := sessionSnap[s.ID]; ok {
			return info
		}
	}
	return repoSnap[session.GetMainRepo(s.ProjectPath)]
}

// --- Internal helpers ---

func (h *Home) rebuildFlatItems() {
	h.flatItems = BuildFlatItems(h.sessions, h.pendingWorkspaces, h.repoExpanded, h.filterText, h.pinnedRepos, h.repoOrder)
	h.sidebarDirty = true
}

func (h *Home) removePendingWorkspace(id string) {
	for i, pw := range h.pendingWorkspaces {
		if pw.ID == id {
			h.pendingWorkspaces = append(h.pendingWorkspaces[:i], h.pendingWorkspaces[i+1:]...)
			return
		}
	}
}

func (h *Home) rebuildSessionMap() {
	h.sessionByID = make(map[string]*session.Session, len(h.sessions))
	for _, s := range h.sessions {
		h.sessionByID[s.ID] = s
	}
}

// sidebarListHeight returns the height of the sidebar panel in the current
// layout — i.e. the value View() passes to RenderSidebar as `height`, before
// chrome rows (title + underline) and scroll indicators are subtracted.
// Stacked mode gives the sidebar ~55% of the content area; single/dual give
// it the full content area. Mirrors the arithmetic in View() (app.go:794+).
func (h *Home) sidebarListHeight() int {
	contentHeight := h.height - 2 - helpBarHeight
	if contentHeight < 1 {
		contentHeight = 1
	}
	if h.layoutMode() == "stacked" {
		sh := (contentHeight * 55) / 100
		if sh < 3 {
			sh = 3
		}
		return sh
	}
	return contentHeight
}

// sidebarMinVisibleRows is the conservative lower bound on visible session rows
// in the sidebar — it assumes both scroll indicators are drawn, so the value
// holds even mid-scroll. Used by syncViewport to anchor the cursor before
// RenderSidebar (sidebar.go) decides which indicators to draw.
// Subtracts panel chrome (title + underline = 2) and reserves 2 rows for the
// indicators RenderSidebar may add at top/bottom.
func (h *Home) sidebarMinVisibleRows() int {
	v := h.sidebarListHeight() - 4
	if v < 1 {
		v = 1
	}
	return v
}

// sidebarPanelRows is the actual sidebar panel height in rows, before any
// scroll indicators are drawn. Used as the PgUp/PgDn page step so a single
// page-jump moves a full panel; syncViewport handles anchoring if the target
// would otherwise sit on an indicator row.
func (h *Home) sidebarPanelRows() int {
	v := h.sidebarListHeight() - 2
	if v < 1 {
		v = 1
	}
	return v
}

func (h *Home) syncViewport() {
	if len(h.flatItems) == 0 {
		return
	}
	// Ensure cursor is within bounds.
	if h.cursor < 0 {
		h.cursor = 0
	}
	if h.cursor >= len(h.flatItems) {
		h.cursor = len(h.flatItems) - 1
	}
	contentHeight := h.sidebarMinVisibleRows()
	prevOffset := h.viewOffset
	// Scroll to keep cursor visible.
	if h.cursor < h.viewOffset {
		h.viewOffset = h.cursor
	}
	if h.cursor >= h.viewOffset+contentHeight {
		h.viewOffset = h.cursor - contentHeight + 1
	}
	if h.viewOffset != prevOffset {
		h.renderStats.RecordViewportDrift()
	}
}

// migrateWorktreePinsToMainRepo rewrites any pinned repo path that is itself a
// linked worktree to its main repo. Same for repoOrder. Pre-feature, fleet's
// pin path matched session.GetRepoRoot (the worktree); post-feature, sessions
// group under their main repo, so a worktree pin would either dangle (no
// sessions show under it) or split a group in two.
//
// Idempotent: runs every loadSessions, only writes when something changes.
// Both maps are de-duped so re-pinning under the main repo doesn't double-up.
func (h *Home) migrateWorktreePinsToMainRepo() {
	// Snapshot current keys so we can mutate during iteration safely.
	pins := make([]string, 0, len(h.pinnedRepos))
	for p := range h.pinnedRepos {
		pins = append(pins, p)
	}
	for _, p := range pins {
		if !git.IsWorktree(p) {
			continue
		}
		mp := session.GetMainRepo(p)
		if mp == "" || mp == p {
			continue
		}
		// Re-pin under main repo (if not already pinned), unpin worktree.
		if !h.pinnedRepos[mp] {
			h.pinnedRepos[mp] = true
			if err := h.storage.PinRepo(mp); err != nil {
				debuglog.Logger.Error("migrate pin: PinRepo main failed", "main", mp, "err", err)
			}
		}
		delete(h.pinnedRepos, p)
		if err := h.storage.UnpinRepo(p); err != nil {
			debuglog.Logger.Error("migrate pin: UnpinRepo worktree failed", "worktree", p, "err", err)
		}
		// Carry repoOrder across if the worktree had an explicit key and the
		// main repo doesn't (otherwise keep the main repo's existing key).
		if wk, hasW := h.repoOrder[p]; hasW {
			if _, hasM := h.repoOrder[mp]; !hasM {
				h.repoOrder[mp] = wk
				if err := h.storage.UpsertRepoOrder(mp, wk); err != nil {
					debuglog.Logger.Error("migrate pin: UpsertRepoOrder main failed", "main", mp, "err", err)
				}
			}
			delete(h.repoOrder, p)
			if err := h.storage.DeleteRepoOrder(p); err != nil {
				debuglog.Logger.Error("migrate pin: DeleteRepoOrder worktree failed", "worktree", p, "err", err)
			}
		}
		debuglog.Logger.Info("migrated pin from worktree to main", "from", p, "to", mp)
	}
}

func (h *Home) loadSessions() tea.Msg {
	rows, err := h.storage.LoadSessions()
	if err != nil {
		debuglog.Logger.Error("failed to load sessions from database", "err", err)
		return loadSessionsMsg{err: err}
	}

	sessions := make([]*session.Session, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, session.FromRow(row))
	}

	// Demo mode: only show sessions under the specified path prefix.
	if prefix := os.Getenv("FLEET_DEMO_PREFIX"); prefix != "" {
		filtered := make([]*session.Session, 0, len(sessions))
		for _, s := range sessions {
			if strings.HasPrefix(s.ProjectPath, prefix) {
				filtered = append(filtered, s)
			}
		}
		sessions = filtered
	}

	slotBindings, err := h.storage.LoadSlotBindings()
	if err != nil {
		debuglog.Logger.Error("failed to load slot bindings", "err", err)
		slotBindings = map[int]string{}
	}

	repoOrder, err := h.storage.LoadRepoOrder()
	if err != nil {
		debuglog.Logger.Error("failed to load repo order", "err", err)
		repoOrder = map[string]int64{}
	}

	// These block but run in the tea.Cmd goroutine, not Update().
	configDir := hooks.GetClaudeConfigDir()
	hooks.InjectClaudeHooks(configDir)
	chrome.InstallNativeMessagingHost()

	// Check for claude CLI availability.
	var warning string
	if _, err := exec.LookPath("claude"); err != nil {
		warning = "claude CLI not found — install Claude Code to create sessions"
	}

	return loadSessionsMsg{sessions: sessions, slotBindings: slotBindings, repoOrder: repoOrder, warning: warning}
}

func (h *Home) setError(err error) {
	h.err = err
	h.errTime = time.Now()
	if err != nil {
		h.errorHistory.Add(err.Error())
		h.toasts.Add(ToastError, err.Error())
		analytics.Track(analytics.EventErrorOccurred, map[string]interface{}{
			"category": strings.SplitN(err.Error(), ":", 2)[0],
		})
	}
}

func (h *Home) setInfo(msg string) {
	h.infoMsg = msg
	h.infoTime = time.Now()
	h.toasts.Add(ToastInfo, msg)
}

// buildPaletteCommands returns all available commands for the command palette.
func (h *Home) buildPaletteCommands() []PaletteCommand {
	return []PaletteCommand{
		{ID: "attach", Name: "Attach Session", Shortcut: "Enter"},
		{ID: "focus", Name: "Focus Preview", Shortcut: "Tab"},
		{ID: "jump_next", Name: "Jump to Next Waiting", Shortcut: "Space"},
		{ID: "new_session", Name: "New Session", Shortcut: "a"},
		{ID: "new_repo", Name: "New Session (Worktree)", Shortcut: "n"},
		{ID: "new_worktree", Name: "New Session in Existing Worktree"},
		{ID: "new_session_at_path", Name: "New Session at Path"},
		{ID: "fork", Name: "Fork Session", Shortcut: "f"},
		{ID: "delete", Name: "Delete Session", Shortcut: "d"},
		{ID: "restart", Name: "Restart Session", Shortcut: "r"},
		{ID: "rename", Name: "Rename Session", Shortcut: "R"},
		{ID: "editor", Name: "Open in Editor", Shortcut: "e"},
		{ID: "open_pr", Name: "Open PR / MR", Shortcut: "p"},
		{ID: "approve", Name: "Quick Approve", Shortcut: "Y"},
		{ID: "branch", Name: "Switch Branch", Shortcut: "b"},
		{ID: "filter", Name: "Filter Sessions", Shortcut: "/"},
		{ID: "settings", Name: "Settings", Shortcut: "S"},
		{ID: "bug_report", Name: "Diagnostics", Shortcut: "!"},
		{ID: "help", Name: "Help", Shortcut: "?"},
		{ID: "reload_all", Name: "Reload All Sessions"},
		{ID: "quit", Name: "Quit", Shortcut: "q"},
	}
}

// dispatchCommand executes a command selected from the palette.
func (h *Home) dispatchCommand(id string) (tea.Model, tea.Cmd) {
	switch id {
	case "attach":
		if s := h.selectedSession(); s != nil {
			h.actionLog.Add("attach session", s.Title, true)
			analytics.Track(analytics.EventSessionAttached, nil)
		}
		return h, h.attachSelected()
	case "focus":
		return h, h.enterFocusMode()
	case "jump_next":
		h.jumpToNextAttentionSession()
		analytics.Track(analytics.EventSpaceJump, nil)
		return h, h.fetchPreviewForSelected()
	case "new_session":
		repoPath := h.resolveCurrentRepo()
		if repoPath == "" {
			h.newDialog.Show()
			return h, nil
		}
		h.actionLog.Add("create session", repoPath, true)
		return h.handleSessionCreate(sessionCreateMsg{
			path:  repoPath,
			title: filepath.Base(repoPath),
		})
	case "new_repo":
		// Mirror the `n` key: instant worktree session at cursor (or path
		// picker fallback if there's no cursor target).
		return h.startWorktreeSessionAtCursor()
	case "new_worktree":
		// Existing worktrees: show the picker so the user can attach a session
		// to a worktree fleet didn't create.
		repoPath := h.resolveCurrentRepo()
		if repoPath == "" {
			h.setError(fmt.Errorf("no repo selected"))
			return h, nil
		}
		h.worktreeDialog.ShowLoading()
		return h, tea.Batch(h.fetchWorkspaceListForRepo(repoPath), spinnerTickCmd)
	case "new_session_at_path":
		// Power-user path: type a project path. Git repos route through the
		// worktree-by-default flow; non-git paths produce a plain session.
		h.newDialog.Show()
		return h, nil
	case "fork":
		return h, h.forkSelected()
	case "delete":
		if s := h.selectedSession(); s != nil {
			h.actionLog.Add("delete session", s.Title, true)
		}
		return h, h.confirmDeleteSelected()
	case "restart":
		if s := h.selectedSession(); s != nil {
			h.actionLog.Add("restart session", s.Title, true)
			analytics.Track(analytics.EventSessionRestarted, nil)
		}
		return h, h.restartSelected()
	case "rename":
		return h, h.renameSelected()
	case "editor":
		if s := h.selectedSession(); s != nil {
			h.actionLog.Add("open editor", fmt.Sprintf("%q at %s", h.cfg.GetEditor(), s.ProjectPath), true)
			analytics.Track(analytics.EventEditorOpened, map[string]interface{}{"editor": h.cfg.GetEditor()})
		}
		return h, h.openEditorSelected()
	case "open_pr":
		h.actionLog.Add("open PR", "", true)
		analytics.Track(analytics.EventPROpened, nil)
		return h, h.openPRInBrowser()
	case "approve":
		if s := h.selectedSession(); s != nil {
			h.actionLog.Add("quick approve", s.Title, true)
			analytics.Track(analytics.EventQuickApprove, nil)
		}
		return h, h.quickApproveSelected()
	case "branch":
		repoPath := h.resolveCurrentRepo()
		if repoPath == "" {
			return h, nil
		}
		h.branchDialog.ShowLoading()
		return h, tea.Batch(h.fetchBranchList(repoPath), spinnerTickCmd)
	case "filter":
		h.filterActive = true
		h.filterInput.Focus()
		analytics.Track(analytics.EventFilterUsed, nil)
		return h, nil
	case "settings":
		h.settingsDialog.Show()
		analytics.Track(analytics.EventSettingsOpened, nil)
		return h, nil
	case "bug_report":
		h.actionLog.Add("open diagnostics", "", true)
		h.bugReport.Show(h.version, len(h.sessions), h.errorHistory, h.actionLog, h.width, h.height)
		analytics.Track(analytics.EventBugReportOpened, nil)
		return h, nil
	case "help":
		h.helpOverlay.Show()
		return h, nil
	case "reload_all":
		analytics.Track(analytics.EventReloadAll, nil)
		return h, h.reloadAll()
	case "quit":
		return h, tea.Quit
	}
	return h, nil
}

// reloadAll restarts all dead/error sessions concurrently.
func (h *Home) reloadAll() tea.Cmd {
	type target struct {
		session *session.Session
		title   string
	}
	var targets []target
	for _, s := range h.sessions {
		status := s.GetStatus()
		// Skip active/healthy sessions — never kill running Claude work or idle sessions.
		if status == session.StatusRunning || status == session.StatusWaiting ||
			status == session.StatusStarting || status == session.StatusFinished ||
			status == session.StatusIdle {
			continue
		}
		targets = append(targets, target{session: s, title: s.Title})
	}

	if len(targets) == 0 {
		h.setInfo("All sessions healthy — nothing to reload")
		return nil
	}

	skipped := len(h.sessions) - len(targets)
	debuglog.Logger.Info("reload all", "eligible", len(targets), "skipped", skipped)

	return func() tea.Msg {
		var (
			mu     sync.Mutex
			errors []string
			wg     sync.WaitGroup
		)

		for _, t := range targets {
			wg.Add(1)
			go func(s *session.Session, title string) {
				defer wg.Done()
				var err error
				if s.IsAlive() && !s.GetTmuxSession().IsPaneDead() {
					err = s.RespawnClaude()
				} else {
					err = s.Restart()
				}
				if err != nil {
					mu.Lock()
					errors = append(errors, title)
					mu.Unlock()
					debuglog.Logger.Error("reload all: restart failed", "title", title, "err", err)
				}
			}(t.session, t.title)
		}

		wg.Wait()

		return reloadAllResultMsg{
			restarted: len(targets) - len(errors),
			skipped:   skipped,
			errors:    errors,
		}
	}
}

// ensureExactHeight pads or truncates content to exactly n lines.
func ensureExactHeight(content string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(content, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// ensureExactWidth pads or truncates each line to exactly the given visual width.
func ensureExactWidth(content string, width int) string {
	if width <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	result := make([]string, len(lines))
	for i, line := range lines {
		w := lipgloss.Width(line)
		if w == width {
			result[i] = line
		} else if w < width {
			result[i] = line + strings.Repeat(" ", width-w)
		} else {
			truncated := lipgloss.NewStyle().MaxWidth(width).Render(line)
			tw := lipgloss.Width(truncated)
			if tw < width {
				truncated += strings.Repeat(" ", width-tw)
			}
			result[i] = truncated
		}
	}
	return strings.Join(result, "\n")
}
