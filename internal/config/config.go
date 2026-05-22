package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/brizzai/fleet/internal/debuglog"
)

// Config holds user-configurable settings.
type Config struct {
	TickIntervalSec    int        `json:"tick_interval_sec,omitempty"`
	DefaultProjectPath string     `json:"default_project_path,omitempty"`
	Editor             string     `json:"editor,omitempty"`
	Theme              string     `json:"theme,omitempty"`
	AutoNameSessions   *bool      `json:"auto_name_sessions,omitempty"`
	CopyClaudeSettings *bool      `json:"copy_claude_settings,omitempty"`
	EnterMode          string     `json:"enter_mode,omitempty"` // "attach" or "split"
	FocusOnNewSession  *bool      `json:"focus_on_new_session,omitempty"`
	Telemetry          *bool      `json:"telemetry,omitempty"`
	Web                *WebConfig `json:"web,omitempty"`
}

// WebConfig configures the embedded mobile-friendly web UI server.
//
// Disabled by default — set Enabled=true to opt in. Addr defaults to
// "0.0.0.0:8765" (Tailscale-reachable; token is the protection). Token may
// be left empty on first run; the server generates a random 32-byte hex token
// and writes it back to the config file, logging once at INFO. When listening
// on a non-loopback address an empty token causes the server to refuse to
// start — the bearer token is the only auth.
type WebConfig struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Addr    string `json:"addr,omitempty"`
	Token   string `json:"token,omitempty"`
}

// IsEnabled reports whether the embedded web UI server should start.
// Nil-safe: returns false if the receiver or the Enabled pointer is nil.
func (w *WebConfig) IsEnabled() bool {
	if w == nil || w.Enabled == nil {
		return false
	}
	return *w.Enabled
}

// GetAddr returns the configured listen address, defaulting to "0.0.0.0:8765"
// when unset. Nil-safe.
func (w *WebConfig) GetAddr() string {
	if w == nil || w.Addr == "" {
		return "0.0.0.0:8765"
	}
	return w.Addr
}

// IsAutoNameEnabled returns whether auto-naming is enabled (default: true).
func (c *Config) IsAutoNameEnabled() bool {
	if c.AutoNameSessions == nil {
		return true
	}
	return *c.AutoNameSessions
}

// DefaultConfigPath returns the default config file path.
func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "fleet", "config.json")
}

// Load reads config from disk, returning defaults if missing.
func Load() *Config {
	cfg := &Config{
		TickIntervalSec: 2,
	}

	path := DefaultConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		debuglog.Logger.Info("config file not found, using defaults", "path", path)
		return cfg
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		debuglog.Logger.Error("failed to parse config file", "path", path, "error", err)
	} else {
		debuglog.Logger.Info("config loaded", "path", path)
	}

	// Enforce minimums.
	if cfg.TickIntervalSec < 1 {
		cfg.TickIntervalSec = 2
	}

	return cfg
}

// Save writes config to disk.
func (c *Config) Save() error {
	path := DefaultConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		debuglog.Logger.Error("failed to create config directory", "path", path, "error", err)
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		debuglog.Logger.Error("failed to marshal config", "error", err)
		return err
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		debuglog.Logger.Error("failed to write config file", "path", path, "error", err)
		return err
	}
	return nil
}

// IsCopyClaudeSettingsEnabled returns whether to copy .claude/settings.local.json to new worktrees (default: true).
func (c *Config) IsCopyClaudeSettingsEnabled() bool {
	if c.CopyClaudeSettings == nil {
		return true
	}
	return *c.CopyClaudeSettings
}

// GetEnterMode returns the configured Enter key mode ("attach" or "split").
func (c *Config) GetEnterMode() string {
	if c.EnterMode == "split" {
		return "split"
	}
	return "attach"
}

// IsFocusOnNewSessionEnabled returns whether to auto-focus newly created
// sessions using the configured Enter mode (default: false).
func (c *Config) IsFocusOnNewSessionEnabled() bool {
	if c.FocusOnNewSession == nil {
		return false
	}
	return *c.FocusOnNewSession
}

// IsTelemetryEnabled returns whether telemetry is enabled (default: true).
func (c *Config) IsTelemetryEnabled() bool {
	if c.Telemetry == nil {
		return true
	}
	return *c.Telemetry
}

// GetEditor returns the configured editor, falling back to $EDITOR then "code".
func (c *Config) GetEditor() string {
	if c.Editor != "" {
		return c.Editor
	}
	if editor := os.Getenv("EDITOR"); editor != "" {
		return editor
	}
	return "code"
}
