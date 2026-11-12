// Package choix holds what the module root embeds: the frontend build, the
// approved copy of the native alerts and the approved menu structure.
package choix

import (
	"embed"
	"io/fs"
)

// frontendDist is the frontend build. Until the frontend is built, a
// placeholder index.html stands in for it: `task frontend:bootstrap` copies
// one from build/frontend-stub when frontend/dist/index.html is missing, so
// every command that loads the module's packages compiles on a clean
// checkout.
//
//go:embed all:frontend/dist
var frontendDist embed.FS

// DialogManifest is the approved copy of the native alerts,
// frontend/src/design/dialogs.json. internal/app/dialogs shows the alerts
// from it, so the alert that ships is the approved one.
//
//go:embed frontend/src/design/dialogs.json
var DialogManifest []byte

// MenuManifest is the approved menu structure, frontend/src/design/
// menus.json: the menu bar and the folder row's context menu.
// internal/app/menu builds the native menus from it, so the menu that ships
// is the approved one.
//
//go:embed frontend/src/design/menus.json
var MenuManifest []byte

// Frontend returns the frontend build, rooted at frontend/dist: index.html
// is at its top.
func Frontend() fs.FS {
	sub, err := fs.Sub(frontendDist, "frontend/dist")
	if err != nil {
		// Unreachable: fs.Sub fails only on an invalid path.
		panic("choix: frontend sub-filesystem: " + err.Error())
	}
	return sub
}
