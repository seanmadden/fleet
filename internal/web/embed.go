package web

import (
	"embed"
	"io/fs"
)

//go:embed assets
var assetsFS embed.FS

// staticFS returns the embedded asset tree rooted at "assets/", so a request
// for "/index.html" maps to "assets/index.html". Wrapped via fs.Sub so the
// "assets/" prefix doesn't leak into URL paths.
func staticFS() fs.FS {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		// Embedded FS errors are programmer bugs, not runtime conditions —
		// fail loud at startup so the regression is impossible to miss.
		panic("web: failed to sub assets FS: " + err.Error())
	}
	return sub
}
