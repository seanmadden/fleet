package daemonsrv

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	fleetv1 "github.com/brizzai/fleet/gen/proto/fleet/v1"
	"github.com/brizzai/fleet/internal/hooks"
	"github.com/brizzai/fleet/internal/service"
	"github.com/brizzai/fleet/internal/session"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// diagnosticsProvider is the subset of *service.SessionService that the
// diagnostics handler needs. The Service interface intentionally doesn't
// expose these — only the in-process implementation supports them, and the
// daemon is the only consumer (clients can't relay an in-memory ring buffer
// over gRPC). The handler type-asserts to this interface and returns an
// empty diagnostics blob if the assertion fails (e.g. when the daemon is
// running in a degraded mode).
type diagnosticsProvider interface {
	CycleLog() []service.CycleLogEntry
	TransitionLog() []service.StatusTransitionEntry
	HookWatcher() *hooks.HookWatcher
}

// GetDiagnostics renders a status-detection-focused snapshot of the daemon
// as markdown. Used by the Mac app's Cmd-Shift-D hotkey to dump state when
// timing feels off. The shape is deliberately verbose and human-readable —
// stable enough to scan visually, not a wire contract.
func (s *Server) GetDiagnostics(_ context.Context, _ *emptypb.Empty) (*fleetv1.DiagnosticsResponse, error) {
	now := time.Now()

	var b strings.Builder
	fmt.Fprintf(&b, "# Fleet daemon diagnostics\n\n")
	fmt.Fprintf(&b, "Captured: `%s`\n", now.Format(time.RFC3339Nano))
	fmt.Fprintf(&b, "Daemon PID: `%d`\n\n", os.Getpid())

	sessions := s.svc.Sessions()
	fmt.Fprintf(&b, "## Sessions (%d)\n\n", len(sessions))
	if len(sessions) == 0 {
		fmt.Fprintf(&b, "_no sessions_\n\n")
	} else {
		writeSessionTable(&b, sessions, now)
	}

	if dp, ok := s.svc.(diagnosticsProvider); ok {
		writeTransitionLog(&b, dp.TransitionLog(), now)
		writeHookEventLog(&b, dp.HookWatcher(), now)
		writeCycleLog(&b, dp.CycleLog(), now)
	} else {
		fmt.Fprintf(&b, "_diagnostics ring buffers unavailable in this build_\n")
	}

	return &fleetv1.DiagnosticsResponse{
		Markdown:   b.String(),
		CapturedAt: timestamppb.New(now),
	}, nil
}

func writeSessionTable(b *strings.Builder, sessions []*session.Session, now time.Time) {
	// Sort by repo, then title — same order the sidebar uses, so the snapshot
	// reads top-to-bottom like the UI.
	sort.Slice(sessions, func(i, j int) bool {
		ri := session.GetRepoRoot(sessions[i].ProjectPath)
		rj := session.GetRepoRoot(sessions[j].ProjectPath)
		if ri != rj {
			return ri < rj
		}
		return sessions[i].Title < sessions[j].Title
	})

	for _, sess := range sessions {
		diag := sess.Diagnostics()
		hookAge := "-"
		if !diag.HookUpdatedAt.IsZero() {
			hookAge = fmtAge(now.Sub(diag.HookUpdatedAt))
		}
		paneAge := "-"
		if !diag.LastContentChangeAt.IsZero() {
			paneAge = fmtAge(now.Sub(diag.LastContentChangeAt))
		}
		tmuxAge := "-"
		var tmuxAct int64
		if ts := sess.GetTmuxSession(); ts != nil {
			if act, ok := ts.GetActivity(); ok {
				tmuxAct = act
				tmuxAge = fmtAge(now.Sub(time.Unix(act, 0)))
			}
		}
		fmt.Fprintf(b, "### %s\n", sess.Title)
		fmt.Fprintf(b, "- id: `%s`\n", sess.ID)
		fmt.Fprintf(b, "- status: **%s** (alive=%v ack=%v)\n", sess.GetStatus(), sess.IsAlive(), sess.Acknowledged)
		fmt.Fprintf(b, "- repo: `%s`\n", session.GetRepoRoot(sess.ProjectPath))
		fmt.Fprintf(b, "- tmux: `%s` (activity=%d, age=%s)\n", sess.TmuxSessionName, tmuxAct, tmuxAge)
		fmt.Fprintf(b, "- hook: `%s` (updated %s ago)\n", diag.HookStatus, hookAge)
		fmt.Fprintf(b, "- anti-flicker: lastContentHash=`%s` lastContentChangeAt=%s ago\n", trimHash(diag.LastContentHash), paneAge)
		fmt.Fprintf(b, "- claude session id: `%s`\n", sess.ClaudeSessionID)
		fmt.Fprintf(b, "\n")
	}
}

func trimHash(h string) string {
	if h == "" {
		return "-"
	}
	if len(h) > 12 {
		return h[:12] + "…"
	}
	return h
}

func writeTransitionLog(b *strings.Builder, log []service.StatusTransitionEntry, now time.Time) {
	fmt.Fprintf(b, "## Recent status transitions (%d, newest last)\n\n", len(log))
	if len(log) == 0 {
		fmt.Fprintf(b, "_none_\n\n")
		return
	}
	fmt.Fprintf(b, "| age | session | old → new | source | reason |\n")
	fmt.Fprintf(b, "|---|---|---|---|---|\n")
	for _, t := range log {
		fmt.Fprintf(b, "| %s | %s | %s → %s | %s | %s |\n",
			fmtAge(now.Sub(t.At)),
			truncMid(t.Title, 24),
			t.Old, t.New, t.Source, t.Reason,
		)
	}
	fmt.Fprintf(b, "\n")
}

func writeCycleLog(b *strings.Builder, log []service.CycleLogEntry, now time.Time) {
	fmt.Fprintf(b, "## Recent worker cycles (%d, newest last)\n\n", len(log))
	if len(log) == 0 {
		fmt.Fprintf(b, "_none_\n\n")
		return
	}
	fmt.Fprintf(b, "| age | kind | duration | priority | round-robin | hook-synced | status changed |\n")
	fmt.Fprintf(b, "|---|---|---|---|---|---|---|\n")
	for _, c := range log {
		fmt.Fprintf(b, "| %s | %s | %s | %d | %d | %d | %d |\n",
			fmtAge(now.Sub(c.StartedAt)),
			c.Kind,
			c.Duration.Round(time.Millisecond),
			c.PriorityCount,
			c.RoundRobinCount,
			c.HookSyncedCount,
			c.StatusChangedCount,
		)
	}
	fmt.Fprintf(b, "\n")
}

func writeHookEventLog(b *strings.Builder, hw *hooks.HookWatcher, now time.Time) {
	if hw == nil {
		fmt.Fprintf(b, "## Hook events\n\n_hook watcher unavailable_\n\n")
		return
	}
	log := hw.EventLog()
	fmt.Fprintf(b, "## Recent hook events (%d, newest last)\n\n", len(log))
	if len(log) == 0 {
		fmt.Fprintf(b, "_none_\n\n")
		return
	}
	fmt.Fprintf(b, "| age | instance | event | status | dispatch lag |\n")
	fmt.Fprintf(b, "|---|---|---|---|---|\n")
	for _, e := range log {
		lag := "-"
		if !e.FileMTime.IsZero() {
			lag = fmtAge(e.At.Sub(e.FileMTime))
		}
		fmt.Fprintf(b, "| %s | `%s` | %s | %s | %s |\n",
			fmtAge(now.Sub(e.At)),
			truncMid(e.InstanceID, 24),
			e.Event, e.Status, lag,
		)
	}
	fmt.Fprintf(b, "\n")
}

// fmtAge prints a duration in a compact human form used throughout the
// snapshot (e.g. "180ms", "1.2s", "3m04s").
func fmtAge(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.2fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

func truncMid(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen < 4 {
		return s[:maxLen]
	}
	keep := (maxLen - 1) / 2
	return s[:keep] + "…" + s[len(s)-keep:]
}
