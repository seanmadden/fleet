package diagnostics

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"os/exec"
)

// Report holds collected diagnostic information.
type Report struct {
	Version       string
	GoVersion     string
	OS            string
	Arch          string
	MacOSVersion  string
	TmuxVersion   string
	ClaudeVersion string
	GhVersion     string
	GlabVersion   string
	Config        string
	SessionCount  int
	RecentErrors  []string // pre-formatted from ErrorHistory
	RecentActions []string // pre-formatted from ActionLog
	RecentLogs    string   // last 100 lines of debug.log

	// Terminal environment (helps diagnose rendering/scrolling issues).
	TerminalEnv TerminalEnv
	TUIWidth    int // Bubble Tea reported width
	TUIHeight   int // Bubble Tea reported height
}

// TerminalEnv captures terminal-related environment and settings.
type TerminalEnv struct {
	TERM               string
	TermProgram        string // $TERM_PROGRAM (e.g. iTerm2, Apple_Terminal, tmux)
	TermProgramVersion string // $TERM_PROGRAM_VERSION
	ColorTerm          string // $COLORTERM (e.g. truecolor)
	Lang               string // $LANG
	LCAll              string // $LC_ALL
	InsideTmux         bool   // $TMUX is set (nested tmux)
	InsideSSH          bool   // $SSH_TTY or $SSH_CLIENT is set
	TmuxDefaultTerm    string // tmux show-option -gv default-terminal
	TmuxMouse          string // tmux show-option -gv mouse
	SttySize           string // rows cols from stty (space-separated)
}

// Collect gathers system diagnostics.
func Collect(version string, sessionCount int) *Report {
	r := &Report{
		Version:      version,
		GoVersion:    runtime.Version(),
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		SessionCount: sessionCount,
	}

	r.MacOSVersion = runCmd("sw_vers", "-productVersion")
	r.TmuxVersion = runCmd("tmux", "-V")
	r.ClaudeVersion = runCmd("claude", "--version")
	r.GhVersion = firstLine(runCmd("gh", "--version"))
	r.GlabVersion = firstLine(runCmd("glab", "--version"))

	r.TerminalEnv = collectTerminalEnv()

	r.Config = readConfig()
	r.RecentLogs = readRecentLogs(100)

	return r
}

// collectTerminalEnv gathers terminal-related environment variables and tmux settings.
func collectTerminalEnv() TerminalEnv {
	env := TerminalEnv{
		TERM:               os.Getenv("TERM"),
		TermProgram:        os.Getenv("TERM_PROGRAM"),
		TermProgramVersion: os.Getenv("TERM_PROGRAM_VERSION"),
		ColorTerm:          os.Getenv("COLORTERM"),
		Lang:               os.Getenv("LANG"),
		LCAll:              os.Getenv("LC_ALL"),
		InsideTmux:         os.Getenv("TMUX") != "",
		InsideSSH:          os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CLIENT") != "",
	}

	// Get terminal size via stty.
	env.SttySize = runCmd("stty", "size")

	// Get tmux global settings relevant to rendering.
	env.TmuxDefaultTerm = runCmd("tmux", "show-option", "-gv", "default-terminal")
	env.TmuxMouse = runCmd("tmux", "show-option", "-gv", "mouse")

	return env
}

func runCmd(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func readConfig() string {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".config", "fleet", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func readRecentLogs(n int) string {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".config", "fleet", "debug.log")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
