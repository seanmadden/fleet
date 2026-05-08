package hooks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/brizzai/fleet/internal/debuglog"
	"github.com/fsnotify/fsnotify"
)

const hookDebounce = 100 * time.Millisecond

// HookStatus holds the decoded status from a hook status file.
type HookStatus struct {
	Status      string
	SessionID   string
	Event       string
	UpdatedAt   time.Time
	UserPrompt  string
	PromptCount int
}

// HookWatcher watches ~/.config/fleet/hooks/ for status file changes
// and maintains a thread-safe in-memory status map.
type HookWatcher struct {
	hooksDir string
	watcher  *fsnotify.Watcher

	mu       sync.RWMutex
	statuses map[string]*HookStatus // fleet session ID -> latest status

	onChange chan struct{} // buffered(1), notifies when any status changes

	// Ring buffer of recent fsnotify-driven events; consumed by GetDiagnostics
	// so a snapshot can answer "did the daemon even see the hook fire?".
	eventLog   []HookEventLogEntry
	eventLogMu sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc
}

// HookEventLogEntry records one processed hook status file write — captured
// after the fsnotify debounce so the timestamp matches when the daemon
// actually picked it up, not when fsnotify fired.
type HookEventLogEntry struct {
	At         time.Time
	InstanceID string
	Status     string
	Event      string
	FileMTime  time.Time // for measuring debounce/processing lag
}

const hookEventLogSize = 100

// GetHooksDir returns the path to the hooks status directory.
func GetHooksDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), ".config", "fleet", "hooks")
	}
	return filepath.Join(home, ".config", "fleet", "hooks")
}

// NewHookWatcher creates a new watcher for the hooks directory.
func NewHookWatcher() (*HookWatcher, error) {
	hooksDir := GetHooksDir()

	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		debuglog.Logger.Error("hook watcher: failed to create hooks dir", "dir", hooksDir, "err", err)
		return nil, err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		debuglog.Logger.Error("hook watcher: fsnotify watcher creation failed", "err", err)
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	debuglog.Logger.Info("hook watcher created", "dir", hooksDir)
	return &HookWatcher{
		hooksDir: hooksDir,
		watcher:  watcher,
		statuses: make(map[string]*HookStatus),
		onChange: make(chan struct{}, 1),
		ctx:      ctx,
		cancel:   cancel,
	}, nil
}

// Start begins watching the hooks directory. Blocks; run in a goroutine.
func (w *HookWatcher) Start() {
	if err := w.watcher.Add(w.hooksDir); err != nil {
		debuglog.Logger.Error("hook watcher: failed to watch hooks dir", "dir", w.hooksDir, "err", err)
		return
	}

	w.loadExisting()

	// Notify after loading existing files so TUI picks up pre-existing statuses quickly.
	select {
	case w.onChange <- struct{}{}:
	default:
	}

	var debounceTimer *time.Timer
	pendingFiles := make(map[string]bool)
	var pendingMu sync.Mutex

	for {
		select {
		case <-w.ctx.Done():
			return

		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}

			if filepath.Ext(event.Name) != ".json" {
				continue
			}
			if event.Op&(fsnotify.Create|fsnotify.Write) == 0 {
				continue
			}

			pendingMu.Lock()
			pendingFiles[event.Name] = true
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(hookDebounce, func() {
				pendingMu.Lock()
				files := make([]string, 0, len(pendingFiles))
				for f := range pendingFiles {
					files = append(files, f)
				}
				pendingFiles = make(map[string]bool)
				pendingMu.Unlock()

				for _, f := range files {
					w.processFile(f)
				}
			})
			pendingMu.Unlock()

		case watchErr, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			debuglog.Logger.Error("hook watcher: fsnotify error", "err", watchErr)
		}
	}
}

// Stop shuts down the watcher.
func (w *HookWatcher) Stop() {
	w.cancel()
	if w.watcher != nil {
		_ = w.watcher.Close()
	}
}

// GetStatus returns the hook status for a session, or nil if not available.
func (w *HookWatcher) GetStatus(sessionID string) *HookStatus {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.statuses[sessionID]
}

// Changes returns a channel that receives a notification when any hook status changes.
// Buffered(1): callers may miss intermediate changes but will always see the latest state.
func (w *HookWatcher) Changes() <-chan struct{} {
	return w.onChange
}

// loadExisting reads all current status files on startup.
func (w *HookWatcher) loadExisting() {
	entries, err := os.ReadDir(w.hooksDir)
	if err != nil {
		debuglog.Logger.Error("hook watcher: loadExisting ReadDir failed", "dir", w.hooksDir, "err", err)
		return
	}

	count := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		w.processFile(filepath.Join(w.hooksDir, entry.Name()))
		count++
	}
	debuglog.Logger.Debug("hook watcher: loaded existing status files", "count", count)
}

// processFile reads a status file and updates the internal map.
func (w *HookWatcher) processFile(filePath string) {
	sf, err := ReadStatusFile(filePath)
	if err != nil {
		debuglog.Logger.Error("hook watcher: failed to parse status file", "file", filePath, "err", err)
		return
	}

	base := filepath.Base(filePath)
	instanceID := strings.TrimSuffix(base, ".json")

	hookStatus := &HookStatus{
		Status:      sf.Status,
		SessionID:   sf.SessionID,
		Event:       sf.Event,
		UpdatedAt:   time.Unix(sf.Timestamp, 0),
		UserPrompt:  sf.UserPrompt,
		PromptCount: sf.PromptCount,
	}

	w.mu.Lock()
	w.statuses[instanceID] = hookStatus
	w.mu.Unlock()

	w.recordEvent(HookEventLogEntry{
		At:         time.Now(),
		InstanceID: instanceID,
		Status:     sf.Status,
		Event:      sf.Event,
		FileMTime:  time.Unix(sf.Timestamp, 0),
	})

	// Notify listeners of the change (non-blocking).
	select {
	case w.onChange <- struct{}{}:
	default:
	}
}

func (w *HookWatcher) recordEvent(e HookEventLogEntry) {
	w.eventLogMu.Lock()
	defer w.eventLogMu.Unlock()
	w.eventLog = append(w.eventLog, e)
	if len(w.eventLog) > hookEventLogSize {
		w.eventLog = w.eventLog[len(w.eventLog)-hookEventLogSize:]
	}
}

// EventLog returns a snapshot of recent fsnotify-driven hook events
// (oldest first). Used by GetDiagnostics for status-detection debugging.
func (w *HookWatcher) EventLog() []HookEventLogEntry {
	w.eventLogMu.Lock()
	defer w.eventLogMu.Unlock()
	out := make([]HookEventLogEntry, len(w.eventLog))
	copy(out, w.eventLog)
	return out
}
