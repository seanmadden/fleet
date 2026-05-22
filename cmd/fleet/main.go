package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/brizzai/fleet/internal/config"
	"github.com/brizzai/fleet/internal/debuglog"
	"github.com/brizzai/fleet/internal/migration"
	"github.com/brizzai/fleet/internal/perfwatch"
	"github.com/brizzai/fleet/internal/session"
	"github.com/brizzai/fleet/internal/tmux"
	"github.com/brizzai/fleet/internal/ui"
	"github.com/brizzai/fleet/internal/web"
	tea "github.com/charmbracelet/bubbletea"
)

// version is set via -ldflags at build time. GoReleaser populates this automatically.
var version = "dev"

func init() {
	// Aliasing must happen before any subcommand runs: hook-handler subprocesses
	// inherited BRIZZCODE_INSTANCE_ID from the legacy TUI and need it visible
	// under FLEET_INSTANCE_ID. Cheap, env-only.
	migration.AliasLegacyEnv()
}

func main() {
	args := os.Args[1:]

	if len(args) == 0 {
		runTUI()
		return
	}

	// Chrome launches native messaging hosts with chrome-extension://... as the sole argument.
	// Detect this and route to chrome-host handler.
	if strings.HasPrefix(args[0], "chrome-extension://") {
		handleChromeHost()
		return
	}

	switch args[0] {
	case "add":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: fleet add <path>")
			os.Exit(1)
		}
		runAdd(args[1])
	case "list", "ls":
		runList()
	case "remove", "rm":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: fleet remove <id>")
			os.Exit(1)
		}
		runRemove(args[1])
	case "hook-handler":
		handleHookHandler()
	case "chrome-host":
		handleChromeHost()
	case "hooks":
		handleHooksCmd(args[1:])
	case "version", "--version", "-v":
		fmt.Printf("fleet %s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", args[0])
		printUsage()
		os.Exit(1)
	}
}

func runTUI() {
	// Indirection so deferred cleanup (storage.Close, web server Shutdown)
	// still runs on the error path. Calling os.Exit directly in runTUI bypasses
	// defers; runTUIInner returns an exit code instead.
	if code := runTUIInner(); code != 0 {
		os.Exit(code)
	}
}

func runTUIInner() int {
	// Run filesystem/tmux/hook migration before debuglog.Init creates ~/.config/fleet/.
	// migration.Run is a no-op after the first successful invocation.
	migration.Run()

	debuglog.Init()
	defer debuglog.Close()
	perfwatch.Init()
	debuglog.Logger.Info("fleet TUI starting", "version", version)

	if err := tmux.IsTmuxAvailable(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	cfg := config.Load()

	storage, err := session.Open(session.DefaultDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database: %v\n", err)
		return 1
	}
	defer storage.Close()

	model := ui.NewHome(storage, cfg, version)
	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	model.SetProgram(p)

	// Optional embedded web server. Returns nil when disabled.
	webSrv := startWebServer(cfg, model)
	if webSrv != nil {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := webSrv.Shutdown(ctx); err != nil {
				debuglog.Logger.Warn("web: shutdown", "err", err)
			}
		}()
	}

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

// startWebServer constructs and launches the embedded web server when
// cfg.Web.Enabled is true. Returns nil when disabled or on construction
// error (errors are logged + surfaced via stderr but don't take the TUI
// down — the user can still use fleet without the web UI).
//
// Token bootstrapping: when web.token is empty AND web is enabled, generate
// a 32-byte hex token, persist it back to ~/.config/fleet/config.json, and
// log once at INFO. Non-loopback addr + empty token is a config error and
// the server is refused at startup.
func startWebServer(cfg *config.Config, model *ui.Home) *web.Server {
	if cfg.Web == nil || !cfg.Web.IsEnabled() {
		return nil
	}
	addr := cfg.Web.GetAddr()
	token := cfg.Web.Token

	// Auto-mint a token if empty — even on loopback so the user has one
	// they can paste into mobile Safari.
	if token == "" {
		t, err := web.GenerateToken()
		if err != nil {
			fmt.Fprintf(os.Stderr, "web: token generation failed: %v\n", err)
			debuglog.Logger.Error("web: token generation failed", "err", err)
			return nil
		}
		token = t
		cfg.Web.Token = token
		if err := cfg.Save(); err != nil {
			debuglog.Logger.Error("web: failed to persist generated token", "err", err)
			// Continue — user can copy from the log line below.
		}
		debuglog.Logger.Info("web: generated bearer token (saved to config)", "token", token)
		fmt.Fprintf(os.Stderr, "fleet web UI: generated bearer token (saved to ~/.config/fleet/config.json):\n  %s\n", token)
	}

	srv, err := web.NewServer(web.Deps{
		Source:     model,
		Dispatcher: model,
		Addr:       addr,
		Token:      token,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "web: %v\n", err)
		debuglog.Logger.Error("web: server construction failed", "err", err)
		return nil
	}

	model.SetWebPublisher(srv)
	fmt.Fprintf(os.Stderr, "fleet web UI listening on %s\n", addr)

	go func() {
		if err := srv.Start(); err != nil {
			debuglog.Logger.Error("web: server stopped with error", "err", err)
		}
	}()
	return srv
}

func runAdd(path string) {
	if err := tmux.IsTmuxAvailable(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Expand and validate path.
	path = expandPath(path)
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Invalid directory: %s\n", path)
		os.Exit(1)
	}

	storage, err := session.Open(session.DefaultDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer storage.Close()

	title := session.TitleFromPath(path)
	s := session.NewSession(title, path)

	if err := s.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start session: %v\n", err)
		os.Exit(1)
	}

	if err := storage.SaveSession(s.ToRow()); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to save session: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Created session '%s' (%s)\n", title, s.ID)
}

func runList() {
	storage, err := session.Open(session.DefaultDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer storage.Close()

	rows, err := storage.LoadSessions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load sessions: %v\n", err)
		os.Exit(1)
	}

	if len(rows) == 0 {
		fmt.Println("No sessions.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTITLE\tSTATUS\tPATH")
	for _, r := range rows {
		// Show short ID.
		shortID := r.ID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", shortID, r.Title, r.Status, r.ProjectPath)
	}
	w.Flush()
}

func runRemove(idPrefix string) {
	storage, err := session.Open(session.DefaultDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer storage.Close()

	rows, err := storage.LoadSessions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load sessions: %v\n", err)
		os.Exit(1)
	}

	// Find session by ID prefix.
	var match *session.SessionRow
	for _, r := range rows {
		if strings.HasPrefix(r.ID, idPrefix) {
			if match != nil {
				fmt.Fprintln(os.Stderr, "Ambiguous ID prefix, be more specific")
				os.Exit(1)
			}
			match = r
		}
	}

	if match == nil {
		fmt.Fprintf(os.Stderr, "No session found with ID starting with '%s'\n", idPrefix)
		os.Exit(1)
	}

	// Kill tmux session if alive.
	ts := tmux.ReconnectSession(match.TmuxSession, match.Title, match.ProjectPath)
	if ts.Exists() {
		_ = ts.Kill()
	}

	if err := storage.DeleteSession(match.ID); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to delete session: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Removed session '%s' (%s)\n", match.Title, match.ID)
}

func printUsage() {
	fmt.Printf("fleet %s - manage Claude Code sessions\n", version)
	fmt.Println(`
Usage:
  fleet              Launch TUI
  fleet add <path>   Add a new session
  fleet list         List all sessions
  fleet remove <id>  Remove a session
  fleet hooks <install|uninstall|status>  Manage Claude Code hooks
  fleet version      Show version
  fleet help         Show this help`)
}

func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, path[2:])
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}
