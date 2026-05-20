package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/brizzai/fleet/internal/debuglog"
)

// WorkspaceInfo represents a workspace from the provider.
type WorkspaceInfo struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
	Status string `json:"status,omitempty"`
}

// Provider is the interface for workspace operations.
type Provider interface {
	List(repoPath string) ([]WorkspaceInfo, error)
	Create(repoPath, name, branch, baseBranch string) (*WorkspaceInfo, error)
	Destroy(repoPath, name string) error
	CanCreate() bool
	CanDestroy() bool
	IsCustom() bool
}

// --- GitWorktreeProvider (built-in default) ---

// GitWorktreeProvider uses git worktree commands for workspace management.
type GitWorktreeProvider struct{}

func (g *GitWorktreeProvider) List(repoPath string) ([]WorkspaceInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			debuglog.Logger.Error("git worktree list failed", "repo", repoPath, "err", strings.TrimSpace(string(exitErr.Stderr)))
			return nil, fmt.Errorf("git worktree list: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		debuglog.Logger.Error("git worktree list failed", "repo", repoPath, "err", err)
		return nil, fmt.Errorf("git worktree list: %w", err)
	}

	all := parseWorktreePorcelain(string(out))

	// Filter out the main worktree (where path == repoPath).
	absRepo, _ := filepath.Abs(repoPath)
	var result []WorkspaceInfo
	for _, ws := range all {
		absWS, _ := filepath.Abs(ws.Path)
		if absWS == absRepo {
			continue
		}
		result = append(result, ws)
	}
	return result, nil
}

func (g *GitWorktreeProvider) Create(repoPath, name, branch, baseBranch string) (*WorkspaceInfo, error) {
	path := deriveWorktreePath(repoPath, name)

	debuglog.Logger.Info("git worktree create", "repo", repoPath, "name", name, "branch", branch, "baseBranch", baseBranch, "path", path)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Build args: git worktree add <path> -b <branch> [<start-point>]
	args := []string{"-C", repoPath, "worktree", "add", path, "-b", branch}
	if baseBranch != "" {
		args = append(args, baseBranch)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		debuglog.Logger.Debug("git worktree add with -b failed, retrying without -b", "name", name, "branch", branch, "err", strings.TrimSpace(string(out)))
		// Branch might already exist — retry without -b.
		args2 := []string{"-C", repoPath, "worktree", "add", path, branch}
		cmd2 := exec.CommandContext(ctx, "git", args2...)
		if out2, err2 := cmd2.CombinedOutput(); err2 != nil {
			// Return the more informative error.
			errMsg := strings.TrimSpace(string(out))
			if errMsg == "" {
				errMsg = strings.TrimSpace(string(out2))
			}
			debuglog.Logger.Error("git worktree create failed", "name", name, "branch", branch, "err", errMsg)
			return nil, fmt.Errorf("git worktree add: %s", errMsg)
		}
	}

	debuglog.Logger.Info("git worktree created", "name", name, "branch", branch, "path", path)

	// Make sure the main repo's `git status` doesn't report the in-repo worktree
	// as untracked. Per-repo, untracked-by-the-repo — uses .git/info/exclude
	// rather than .gitignore so we don't commit a fleet-ism into the user's repo.
	if err := ensureWorktreeExcluded(repoPath); err != nil {
		// Non-fatal: the worktree itself is fine, the user just sees
		// `.claude/worktrees/` in `git status`. Log and continue.
		debuglog.Logger.Warn("ensureWorktreeExcluded failed", "repo", repoPath, "err", err)
	}

	return &WorkspaceInfo{
		Name:   name,
		Path:   path,
		Branch: branch,
	}, nil
}

func (g *GitWorktreeProvider) Destroy(repoPath, name string) error {
	debuglog.Logger.Info("git worktree destroy", "repo", repoPath, "name", name)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// List ALL worktrees (unfiltered) — repoPath might be a linked worktree
	// itself, so we can't use g.List() which filters out the repoPath entry.
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			debuglog.Logger.Error("git worktree destroy: list failed", "name", name, "err", strings.TrimSpace(string(exitErr.Stderr)))
			return fmt.Errorf("git worktree list: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		debuglog.Logger.Error("git worktree destroy: list failed", "name", name, "err", err)
		return fmt.Errorf("git worktree list: %w", err)
	}
	all := parseWorktreePorcelain(string(out))
	if len(all) == 0 {
		debuglog.Logger.Error("git worktree destroy: not found", "name", name)
		return fmt.Errorf("worktree %q not found", name)
	}

	// First entry is always the main worktree — use it as base for derived path
	// and as the -C target for git worktree remove.
	mainPath, _ := filepath.Abs(all[0].Path)
	absRepo, _ := filepath.Abs(repoPath)
	derivedPath, _ := filepath.Abs(deriveWorktreePath(mainPath, name))
	legacyPath, _ := filepath.Abs(legacyDeriveWorktreePath(mainPath, name))

	// Find the worktree to remove. Try multiple matching strategies:
	// 1. Exact name match (ws.Name == name)
	// 2. Current derived path (`<mainRepo>/.claude/worktrees/<name>`)
	// 3. Legacy sibling-style derived path (`<parent>/<mainRepoBase>-<name>`),
	//    so worktrees created by pre-`.claude/worktrees/` fleet builds still
	//    destroy cleanly by name
	// 4. repoPath itself is the worktree (caller resolved GetRepoRoot to worktree path)
	var wtPath string
	for _, ws := range all {
		absWS, _ := filepath.Abs(ws.Path)
		if absWS == mainPath {
			continue // never remove the main worktree
		}
		if ws.Name == name || absWS == derivedPath || absWS == legacyPath || absWS == absRepo {
			wtPath = ws.Path
			break
		}
	}
	if wtPath == "" {
		debuglog.Logger.Error("git worktree destroy: not found after search", "name", name, "repo", repoPath)
		return fmt.Errorf("worktree %q not found", name)
	}

	cmd = exec.CommandContext(ctx, "git", "-C", mainPath, "worktree", "remove", "--force", wtPath)
	if rmOut, err := cmd.CombinedOutput(); err != nil {
		debuglog.Logger.Error("git worktree destroy failed", "name", name, "path", wtPath, "err", strings.TrimSpace(string(rmOut)))
		return fmt.Errorf("git worktree remove: %s", strings.TrimSpace(string(rmOut)))
	}
	debuglog.Logger.Info("git worktree destroyed", "name", name, "path", wtPath)
	return nil
}

func (g *GitWorktreeProvider) CanCreate() bool  { return true }
func (g *GitWorktreeProvider) CanDestroy() bool { return true }
func (g *GitWorktreeProvider) IsCustom() bool   { return false }

// --- ShellProvider (from .bc.json) ---

// ShellProvider wraps external shell commands for workspace management.
type ShellProvider struct {
	ListCmd    string
	CreateCmd  string
	DestroyCmd string
}

func (p *ShellProvider) List(repoPath string) ([]WorkspaceInfo, error) {
	if p.ListCmd == "" {
		return nil, fmt.Errorf("list command not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", p.ListCmd)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			debuglog.Logger.Error("shell workspace list failed", "repo", repoPath, "cmd", p.ListCmd, "err", strings.TrimSpace(string(exitErr.Stderr)))
			return nil, fmt.Errorf("list command failed: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		debuglog.Logger.Error("shell workspace list failed", "repo", repoPath, "cmd", p.ListCmd, "err", err)
		return nil, fmt.Errorf("list command failed: %w", err)
	}

	var workspaces []WorkspaceInfo
	if err := json.Unmarshal(out, &workspaces); err != nil {
		debuglog.Logger.Error("shell workspace list: parse output failed", "repo", repoPath, "err", err)
		return nil, fmt.Errorf("parse list output: %w", err)
	}
	return workspaces, nil
}

func (p *ShellProvider) Create(repoPath, name, branch, baseBranch string) (*WorkspaceInfo, error) {
	if p.CreateCmd == "" {
		return nil, fmt.Errorf("create command not configured")
	}

	debuglog.Logger.Info("shell workspace create", "repo", repoPath, "name", name, "branch", branch)

	cmdStr := strings.ReplaceAll(p.CreateCmd, "{{name}}", name)
	if branch == "" {
		// Strip --branch {{branch}} / -b {{branch}} patterns when branch is empty.
		cmdStr = strings.ReplaceAll(cmdStr, "--branch {{branch}}", "")
		cmdStr = strings.ReplaceAll(cmdStr, "-b {{branch}}", "")
		cmdStr = strings.ReplaceAll(cmdStr, "{{branch}}", "")
	} else {
		cmdStr = strings.ReplaceAll(cmdStr, "{{branch}}", branch)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			debuglog.Logger.Error("shell workspace create failed", "name", name, "branch", branch, "err", strings.TrimSpace(string(exitErr.Stderr)))
			return nil, fmt.Errorf("create command failed: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		debuglog.Logger.Error("shell workspace create failed", "name", name, "branch", branch, "err", err)
		return nil, fmt.Errorf("create command failed: %w", err)
	}

	// Try parsing output as JSON first.
	var info WorkspaceInfo
	if err := json.Unmarshal(out, &info); err == nil {
		debuglog.Logger.Info("shell workspace created", "name", info.Name, "path", info.Path, "branch", info.Branch)
		return &info, nil
	}

	// Output wasn't JSON — look up the new workspace via list command.
	if p.ListCmd != "" {
		workspaces, listErr := p.List(repoPath)
		if listErr == nil {
			for _, ws := range workspaces {
				if ws.Name == name {
					debuglog.Logger.Info("shell workspace created (found via list)", "name", ws.Name, "path", ws.Path)
					return &ws, nil
				}
			}
		}
	}

	// Fall back to name-only info.
	debuglog.Logger.Info("shell workspace created (name-only fallback)", "name", name, "branch", branch)
	return &WorkspaceInfo{Name: name, Branch: branch}, nil
}

func (p *ShellProvider) Destroy(repoPath, name string) error {
	if p.DestroyCmd == "" {
		return fmt.Errorf("destroy command not configured")
	}

	debuglog.Logger.Info("shell workspace destroy", "repo", repoPath, "name", name)

	cmdStr := strings.ReplaceAll(p.DestroyCmd, "{{name}}", name)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	cmd.Dir = repoPath
	if out, err := cmd.CombinedOutput(); err != nil {
		debuglog.Logger.Error("shell workspace destroy failed", "name", name, "err", strings.TrimSpace(string(out)))
		return fmt.Errorf("destroy command failed: %s", strings.TrimSpace(string(out)))
	}
	debuglog.Logger.Info("shell workspace destroyed", "name", name)
	return nil
}

func (p *ShellProvider) CanCreate() bool  { return p.CreateCmd != "" }
func (p *ShellProvider) CanDestroy() bool { return p.DestroyCmd != "" }
func (p *ShellProvider) IsCustom() bool   { return true }

// --- Helper functions ---

// parseWorktreePorcelain parses git worktree list --porcelain output.
func parseWorktreePorcelain(output string) []WorkspaceInfo {
	var result []WorkspaceInfo
	var current WorkspaceInfo
	hasWorktree := false

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "worktree ") {
			if hasWorktree {
				result = append(result, current)
			}
			path := strings.TrimPrefix(line, "worktree ")
			current = WorkspaceInfo{
				Path: path,
				Name: filepath.Base(path),
			}
			hasWorktree = true
		} else if strings.HasPrefix(line, "branch refs/heads/") {
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}
	if hasWorktree {
		result = append(result, current)
	}
	return result
}

// SanitizeBranchName converts a branch name to a safe directory name.
// e.g. "feature/login" -> "feature-login"
func SanitizeBranchName(branch string) string {
	s := strings.ReplaceAll(branch, "/", "-")
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "..", "-")
	return s
}

// SanitizeBranchInput normalizes a partial branch-name input for live display:
// space becomes '-', and chars git forbids anywhere in a ref (~ ^ : ? * [ \ `
// and ASCII control chars) are dropped. '/' is kept so users can type
// hierarchical names like "feature/login". Positional rules (leading '-',
// per-component leading '.', trailing '.lock', etc.) are left to
// ValidateBranchName so we don't eat characters mid-type.
func SanitizeBranchInput(s string) string {
	out, _ := SanitizeBranchInputWithCursor(s, 0)
	return out
}

// SanitizeBranchInputWithCursor is SanitizeBranchInput with cursor preservation:
// it returns the sanitized string and the adjusted rune-position cursor so
// callers can keep the user's edit point after live sanitation. Cursor units
// match bubbles/textinput.Position (rune index).
func SanitizeBranchInputWithCursor(s string, cursor int) (string, int) {
	runes := []rune(s)
	newRunes := make([]rune, 0, len(runes))
	newCursor := cursor
	for i, r := range runes {
		keep := true
		out := r
		switch {
		case r == ' ':
			out = '-'
		case r < 0x20 || r == 0x7f:
			keep = false
		case r == '~' || r == '^' || r == ':' || r == '?' || r == '*' || r == '[' || r == '\\' || r == '`':
			keep = false
		}
		if keep {
			newRunes = append(newRunes, out)
		} else if i < cursor {
			newCursor--
		}
	}
	if newCursor < 0 {
		newCursor = 0
	}
	if newCursor > len(newRunes) {
		newCursor = len(newRunes)
	}
	return string(newRunes), newCursor
}

// ValidateBranchName returns a user-friendly error message if branch violates
// git check-ref-format rules that SanitizeBranchInput can't repair live. An
// empty return value means the branch is acceptable.
func ValidateBranchName(branch string) string {
	if branch == "" {
		return "Branch name cannot be empty"
	}
	if branch == "@" {
		return "Branch name cannot be '@'"
	}
	switch branch[0] {
	case '-':
		return "Branch name cannot start with '-'"
	case '/':
		return "Branch name cannot start with '/'"
	}
	if branch[len(branch)-1] == '/' {
		return "Branch name cannot end with '/'"
	}
	if strings.Contains(branch, "..") {
		return "Branch name cannot contain '..'"
	}
	if strings.Contains(branch, "@{") {
		return "Branch name cannot contain '@{'"
	}
	if strings.Contains(branch, "//") {
		return "Branch name cannot contain '//'"
	}
	// Per-component rules (git check-ref-format applies these to each
	// '/'-separated component, not just the whole string).
	for _, comp := range strings.Split(branch, "/") {
		if comp == "" {
			continue
		}
		if comp[0] == '.' {
			return "Branch name cannot have a component starting with '.'"
		}
		if comp[len(comp)-1] == '.' {
			return "Branch name cannot have a component ending with '.'"
		}
		if strings.HasSuffix(comp, ".lock") {
			return "Branch name cannot have a component ending with '.lock'"
		}
	}
	return ""
}

// WorktreesSubdir is the in-repo directory under which fleet creates per-session
// worktrees. Matches Claude Code's own Agent-isolation convention so the two
// tools play nicely side-by-side and so the directory reads as obviously
// claude-related to humans browsing the repo.
const WorktreesSubdir = ".claude/worktrees"

// deriveWorktreePath computes the worktree path under the **main** repo's
// `.claude/worktrees/` directory. If repoPath is itself a linked worktree
// (e.g. fleet is invoked on a previously-created `claude/<hex>` workspace),
// `git rev-parse --git-common-dir` resolves to the main repo's `.git`, whose
// parent is the main repo — so the new worktree always lands inside the main
// checkout regardless of which worktree the user happened to be in.
// e.g. repoPath="/code/myrepo", name="claude-abc12345" -> "/code/myrepo/.claude/worktrees/claude-abc12345"
// e.g. repoPath="/code/myrepo/.claude/worktrees/claude-abc12345", name="feature-x" -> "/code/myrepo/.claude/worktrees/feature-x"
func deriveWorktreePath(repoPath, name string) string {
	absRepo, _ := filepath.Abs(repoPath)
	main := absRepo
	if commonDir := gitCommonDir(absRepo); commonDir != "" {
		main = filepath.Dir(commonDir)
	}
	return filepath.Join(main, WorktreesSubdir, name)
}

// legacyDeriveWorktreePath returns the pre-`.claude/worktrees/` sibling layout
// (`<parent>/<mainRepoBase>-<name>`). Kept solely so Destroy can still locate
// worktrees created by older fleet builds and remove them by name.
func legacyDeriveWorktreePath(repoPath, name string) string {
	absRepo, _ := filepath.Abs(repoPath)
	main := absRepo
	if commonDir := gitCommonDir(absRepo); commonDir != "" {
		main = filepath.Dir(commonDir)
	}
	parent := filepath.Dir(main)
	base := filepath.Base(main)
	return filepath.Join(parent, base+"-"+name)
}

// ensureWorktreeExcluded appends `.claude/worktrees/` to the **main** repo's
// `.git/info/exclude` file if it isn't already listed there. This stops the
// in-repo worktree directories from showing up as untracked content in
// `git status` on the main checkout. The file is per-repo, untracked, and
// invisible to teammates — exactly the right place for a tool-managed rule.
//
// Idempotent: re-running is a no-op when the entry is already present.
// Non-fatal: any I/O error is returned to the caller, which logs and
// continues — the worktree itself is still functional.
func ensureWorktreeExcluded(repoPath string) error {
	commonDir := gitCommonDir(repoPath)
	if commonDir == "" {
		return fmt.Errorf("resolve git common dir for %q", repoPath)
	}
	excludePath := filepath.Join(commonDir, "info", "exclude")

	const entry = "/" + WorktreesSubdir + "/"
	existing, err := os.ReadFile(excludePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", excludePath, err)
	}

	// Match on whole line so we don't double-add and don't false-positive on a
	// longer line that happens to contain the substring.
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == entry {
			return nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(excludePath), err)
	}

	// Prefix a newline if the existing file doesn't end with one, so we don't
	// concatenate onto an unterminated final line.
	var prefix string
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		prefix = "\n"
	}
	appended := prefix + entry + "\n"
	f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", excludePath, err)
	}
	defer f.Close()
	if _, err := f.WriteString(appended); err != nil {
		return fmt.Errorf("write %s: %w", excludePath, err)
	}
	return nil
}

// gitCommonDir is a local helper for deriveWorktreePath. We can't import
// internal/git here (workspace is imported by ui, which also imports git, so
// the dependency chain is fine the other way around — but git imports nothing
// from workspace, so we keep this independent). Returns "" on error.
func gitCommonDir(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--path-format=absolute", "--git-common-dir")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// DeriveWorktreePathPreview returns a display-friendly relative path preview.
// Mirrors deriveWorktreePath: the worktree lives inside the main repo under
// `.claude/worktrees/`, so the preview is rooted at the repo, not its parent.
func DeriveWorktreePathPreview(repoPath, name string) string {
	return WorktreesSubdir + "/" + name
}
