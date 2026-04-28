// Package ui exposes the embedded React+Tailwind SPA built by
// internal/ui/web (see Makefile target `webui`). All assets are bundled
// into the binary via go:embed under web/dist/.
package ui

import (
	"embed"
	"io/fs"
)

// The committed web/dist/index.html is a development stub — it renders a
// "run make webui" message and has no hashed asset references. Running
// "make webui" overwrites the stub with the real built index.html and emits
// hashed JS/CSS files into the same directory, all of which are embedded
// here. The hashed assets are gitignored; only the stub index.html is tracked.
//
//go:embed all:web/dist
var distEmbed embed.FS

// DistFS returns a filesystem rooted at the built SPA output (web/dist).
// It contains index.html plus hashed JS/CSS that the server publishes
// under /static/*.
func DistFS() fs.FS {
	sub, err := fs.Sub(distEmbed, "web/dist")
	if err != nil {
		panic("ui: dist sub: " + err.Error())
	}
	return sub
}

// IndexHTML reads the SPA's index.html. The server returns this for every
// non-API page route so client-side routing can resolve.
func IndexHTML() ([]byte, error) {
	return fs.ReadFile(distEmbed, "web/dist/index.html")
}
