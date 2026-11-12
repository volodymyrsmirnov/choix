// Package menu is Choix's native menu bar and the Library row's context
// menu.
//
// The package is design-gated: both menus are built only from the approved
// manifest, frontend/src/design/menus.json (embedded as choix.MenuManifest),
// so the menu that ships is the approved one. A chosen item reaches the
// frontend as one event, menu:action {id, data} (design call C15), which
// runs the same action as the item's key; items are enabled from the screen
// and selection the frontend reports (system.MenuContext).
package menu

import (
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/volodymyrsmirnov/choix/internal/app/events"
)

// wailsRoles maps the manifest's role names to Wails' built-in roles.
var wailsRoles = map[string]application.Role{
	"about":            application.About,
	"hide":             application.Hide,
	"hideOthers":       application.HideOthers,
	"showAll":          application.ShowAll,
	"quit":             application.Quit,
	"cut":              application.Cut,
	"copy":             application.Copy,
	"paste":            application.Paste,
	"selectAll":        application.SelectAll,
	"closeWindow":      application.CloseWindow,
	"minimize":         application.Minimise,
	"zoom":             application.Zoom,
	"toggleFullscreen": application.ToggleFullscreen,
	"front":            application.Front,
}

// wailsMenu is the Factory that builds into a Wails menu.
type wailsMenu struct{ m *application.Menu }

var _ Factory = wailsMenu{}

func (w wailsMenu) Submenu(title string) Factory {
	return wailsMenu{w.m.AddSubmenu(title)}
}

func (w wailsMenu) Item(label, accelerator string, onClick func()) Handle {
	it := w.m.Add(label)
	if accelerator != "" {
		it.SetAccelerator(accelerator)
	}
	it.OnClick(func(*application.Context) { onClick() })
	return wailsItem{it}
}

// Role appends Wails' role item with the manifest's label: Wails' own
// labels ("Close", "Toggle Full Screen") are not the approved ones. A role
// outside the vocabulary (refused by Parse) appends nothing.
func (w wailsMenu) Role(role, label string) {
	r, ok := wailsRoles[role]
	if !ok {
		return
	}
	it := application.NewRole(r)
	if it == nil {
		return
	}
	it.SetLabel(label)
	w.m.Append(application.NewMenuFromItems(it))
}

func (w wailsMenu) Separator() { w.m.AddSeparator() }

// wailsItem is the Handle of a Wails menu item. Wails applies a change to
// a shown item on the main thread and waits for it.
type wailsItem struct{ it *application.MenuItem }

func (h wailsItem) SetEnabled(enabled bool) { h.it.SetEnabled(enabled) }
func (h wailsItem) SetHidden(hidden bool)   { h.it.SetHidden(hidden) }

// Install builds the menu bar from m, sets it as app's menu and returns the
// state the System binding drives. Clicks are emitted through emit as
// menu:action. The folder row's context menu opens over the window that
// window returns; while it returns nil, nothing is shown. singleKeys is
// NativeAccelerator's: whether the menu owns the single-key shortcuts.
// Install is called once, before app runs.
func Install(app *application.App, window func() *application.WebviewWindow, m Manifest, emit events.Emitter, singleKeys bool) *State {
	return install(app, func(name string, x, y int) {
		w := window()
		if w == nil {
			return
		}
		w.OpenContextMenu(&application.ContextMenuData{Id: name, X: x, Y: y})
	}, m, emit, singleKeys)
}

// install is Install with show, which opens the registered context menu
// called name at x, y, as a seam for the tests. The folder row's context
// menus are a fixed pool (newState), each registered once here and never
// replaced or destroyed: Wails' Menu.Destroy does not release a menu's
// root NSMenu (menu_darwin_shared.go creates it and nothing frees it), so a
// menu per popup would grow without bound, while a pooled menu is cleared
// and refilled in place each time it opens.
func install(app *application.App, show func(name string, x, y int), m Manifest, emit events.Emitter, singleKeys bool) *State {
	bar := application.NewMenu()
	s := newState(m, wailsMenu{bar},
		func(name string) Factory {
			cm := app.ContextMenu.New()
			app.ContextMenu.Add(name, cm)
			return wailsMenu{cm.Menu}
		},
		show, emit, singleKeys)
	app.Menu.Set(bar)
	return s
}
