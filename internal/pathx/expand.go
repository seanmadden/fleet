// Package pathx holds small filesystem-path helpers shared across fleet
// subcommands and the embedded web server.
//
// Kept tiny on purpose — anything bigger than tilde expansion belongs in
// a dedicated package.
package pathx

import (
	"os"
	"path/filepath"
	"strings"
)

// Expand resolves "~"/"~/" prefixes against the current user's home
// directory and returns the absolute form of the resulting path. On
// failure to determine an absolute form, returns the post-tilde path
// unchanged so callers can surface a more specific error via os.Stat.
//
// Used by both the `fleet add` CLI path and the web POST /api/sessions
// handler so the two call sites agree on what an acceptable session path
// looks like.
func Expand(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, path[2:])
	} else if path == "~" {
		home, _ := os.UserHomeDir()
		path = home
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}
