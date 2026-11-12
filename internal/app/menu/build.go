package menu

import (
	"slices"

	"github.com/volodymyrsmirnov/choix/internal/app/system"
)

// Factory builds one menu. It is the seam between the manifest and Wails:
// wails.go's factory builds native menus, the tests' records what is built.
type Factory interface {
	// Submenu appends a submenu titled title and returns its factory.
	Submenu(title string) Factory
	// Item appends a custom item; accelerator "" means none. onClick runs
	// when the item is chosen, on a goroutine of its own.
	Item(label, accelerator string, onClick func()) Handle
	// Role appends the native role item role (a manifest role name) with
	// the manifest's label.
	Role(role, label string)
	// Separator appends a separator.
	Separator()
}

// Handle is a built custom item whose state changes with the frontend's
// screen.
type Handle interface {
	SetEnabled(enabled bool)
	SetHidden(hidden bool)
}

// NativeAccelerator returns the accelerator the native item for it carries,
// "" for none. It is the one place that decides who owns a shortcut (design
// call C31): an accelerator with ⌘, ⌃ or ⌥ always goes to the menu; a single
// key — an item's display (DisplayAccelerator), or an accelerator without
// those modifiers — goes to the menu only when singleKeys is set, and is
// otherwise left to the webview. Spike S2 did not run the key presses that
// would show single keys are safe in the menu (that they do not steal
// typing in a text field, and that the page gets no keydown as well), so
// the app passes singleKeys false until the owner has.
func NativeAccelerator(it Item, singleKeys bool) string {
	acc := it.Accelerator
	if acc == "" {
		a, ok := DisplayAccelerator(it.Display)
		if !ok {
			return ""
		}
		acc = a
	}
	if singleKeys || hasChordModifier(acc) {
		return acc
	}
	return ""
}

// Build builds the manifest's menu bar with f: a submenu per menu, in order,
// holding its items, role items and separators. A chosen custom item calls
// onClick with its id. It returns the custom items' handles by id.
func Build(m Manifest, f Factory, singleKeys bool, onClick func(id string)) map[string]Handle {
	handles := map[string]Handle{}
	for _, menu := range m.Menus {
		sub := f.Submenu(menu.Title)
		for _, it := range menu.Items {
			if h := buildEntry(sub, it, NativeAccelerator(it, singleKeys), onClick); h != nil {
				handles[it.ID] = h
			}
		}
	}
	return handles
}

// BuildContext builds a context menu with f from items, leaving out what
// Visible leaves out for flags. Context items carry no accelerator. A
// chosen item calls onClick with its id.
func BuildContext(items []Item, flags []string, f Factory, onClick func(id string)) {
	for _, it := range Visible(items, flags) {
		buildEntry(f, it, "", onClick)
	}
}

// buildEntry appends it to f and returns the handle of a custom item.
func buildEntry(f Factory, it Item, accelerator string, onClick func(id string)) Handle {
	switch {
	case it.Separator:
		f.Separator()
	case it.Role != "":
		f.Role(it.Role, it.Label)
	default:
		id := it.ID
		return f.Item(it.Label, accelerator, func() { onClick(id) })
	}
	return nil
}

// Visible returns items without those Hidden for flags, then without the
// separators that would be left leading, trailing or doubled — what the
// approved replica shows (MenuPanel.tsx, visibleEntries).
func Visible(items []Item, flags []string) []Item {
	out := make([]Item, 0, len(items))
	for _, it := range items {
		if it.Separator {
			if len(out) == 0 || out[len(out)-1].Separator {
				continue
			}
		} else if Hidden(it, flags) {
			continue
		}
		out = append(out, it)
	}
	for len(out) > 0 && out[len(out)-1].Separator {
		out = out[:len(out)-1]
	}
	return out
}

// Enabled reports whether it is enabled in c: c's screen is in it.EnabledIn
// (an empty list is every screen), and it needs no selection or c has one.
func Enabled(it Item, c system.MenuContext) bool {
	if len(it.EnabledIn) > 0 && !slices.Contains(it.EnabledIn, c.Screen) {
		return false
	}
	return !it.NeedsSelection || c.HasSelection
}

// Hidden reports whether any of it.HiddenWhen is in flags.
func Hidden(it Item, flags []string) bool {
	return slices.ContainsFunc(it.HiddenWhen, func(f string) bool { return slices.Contains(flags, f) })
}
