package menu

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// FolderRow is the manifest's context list for a Library row, and the name
// of its context menu with no flags set.
const FolderRow = "folder-row"

// screens are the design's ScreenIds an item's enabledIn may name.
var screens = []string{"library", "folder", "focus", "compare", "settings"}

// roleVocabulary are the native roles a manifest item may have, in the
// order of their Wails mapping (wails.go).
var roleVocabulary = []string{
	"about", "hide", "hideOthers", "showAll", "quit",
	"cut", "copy", "paste", "selectAll",
	"closeWindow", "minimize", "zoom", "toggleFullscreen", "front",
}

// The ways a manifest can fail Parse.
var (
	errDuplicateID          = errors.New("duplicate id")
	errDuplicateAccelerator = errors.New("duplicate accelerator")
	errBadAccelerator       = errors.New("invalid accelerator")
	errUnknownRole          = errors.New("role outside the vocabulary")
	errUnknownDisplay       = errors.New("display outside the vocabulary")
	errNoLabel              = errors.New("no label")
	errUnknownScreen        = errors.New("enabledIn value is not a ScreenId")
	errItemKind             = errors.New("not exactly one of id, separator, role")
)

// Item is one manifest entry: a custom item (ID set), a separator or a
// native role item (Role set; Label is the text macOS shows for it).
// Accelerator is a Wails accelerator ("CmdOrCtrl+Shift+P"); Display is a
// single key's glyph, which the frontend shows and DisplayAccelerator maps.
type Item struct {
	ID, Label, Accelerator, Display string
	// EnabledIn are the screens on which the item is enabled; empty means
	// every screen.
	EnabledIn []string
	// NeedsSelection: the item also needs a target folder.
	NeedsSelection bool
	// HiddenWhen are the folder flags ("offline", "scanning") that leave
	// the item out.
	HiddenWhen []string
	Separator  bool
	Role       string
}

// Menu is one menu of the menu bar.
type Menu struct {
	ID, Title string
	Items     []Item
}

// Manifest is the approved menu structure: the menu bar, in order, and the
// context menus by name.
type Manifest struct {
	Menus   []Menu
	Context map[string][]Item
}

// rawItem is an item as the manifest spells it.
type rawItem struct {
	ID             string   `json:"id"`
	Label          *string  `json:"label"`
	Accelerator    string   `json:"accelerator"`
	Display        string   `json:"display"`
	EnabledIn      []string `json:"enabledIn"`
	NeedsSelection bool     `json:"needsSelection"`
	HiddenWhen     []string `json:"hiddenWhen"`
	Separator      *bool    `json:"separator"`
	Role           string   `json:"role"`
}

// Parse parses the menu manifest (frontend/src/design/menus.json). It
// refuses a manifest the native menu could not show as approved: an unknown
// field; a duplicate menu id, or a duplicate item id or accelerator among
// "menus" (a context list may reuse a menu's id, but not repeat one of its
// own); an accelerator Wails could not parse; a role or display outside the
// vocabulary; an item or role item without a label; an enabledIn value that
// is not a ScreenId; an entry that is not exactly one of an item, a
// separator and a role.
func Parse(data []byte) (Manifest, error) {
	var raw struct {
		Menus []struct {
			ID    string    `json:"id"`
			Title string    `json:"title"`
			Items []rawItem `json:"items"`
		} `json:"menus"`
		Context map[string][]rawItem `json:"context"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return Manifest{}, fmt.Errorf("menu: parse manifest: %w", err)
	}

	m := Manifest{Context: make(map[string][]Item, len(raw.Context))}
	menuIDs := map[string]bool{}
	ids := map[string]bool{}
	accels := map[string]string{} // canonical accelerator → item id
	for _, rm := range raw.Menus {
		if menuIDs[rm.ID] {
			return Manifest{}, fmt.Errorf("menu: manifest menu %q: %w", rm.ID, errDuplicateID)
		}
		menuIDs[rm.ID] = true
		menu := Menu{ID: rm.ID, Title: rm.Title, Items: make([]Item, 0, len(rm.Items))}
		for i, r := range rm.Items {
			it, err := r.item()
			if err != nil {
				return Manifest{}, fmt.Errorf("menu: manifest menu %q entry %d: %w", rm.ID, i, err)
			}
			if err := claim(it, ids, accels); err != nil {
				return Manifest{}, fmt.Errorf("menu: manifest menu %q entry %d: %w", rm.ID, i, err)
			}
			menu.Items = append(menu.Items, it)
		}
		m.Menus = append(m.Menus, menu)
	}
	for name, list := range raw.Context {
		items := make([]Item, 0, len(list))
		ids := map[string]bool{}
		accels := map[string]string{}
		for i, r := range list {
			it, err := r.item()
			if err != nil {
				return Manifest{}, fmt.Errorf("menu: manifest context %q entry %d: %w", name, i, err)
			}
			if err := claim(it, ids, accels); err != nil {
				return Manifest{}, fmt.Errorf("menu: manifest context %q entry %d: %w", name, i, err)
			}
			items = append(items, it)
		}
		m.Context[name] = items
	}
	return m, nil
}

// item validates r and returns it as an Item.
func (r rawItem) item() (Item, error) {
	kinds := 0
	if r.ID != "" {
		kinds++
	}
	if r.Separator != nil {
		if !*r.Separator {
			return Item{}, fmt.Errorf("separator false: %w", errItemKind)
		}
		kinds++
	}
	if r.Role != "" {
		kinds++
	}
	if kinds != 1 {
		return Item{}, errItemKind
	}
	if r.Separator != nil {
		return Item{Separator: true}, nil
	}
	if r.Label == nil || strings.TrimSpace(*r.Label) == "" {
		return Item{}, fmt.Errorf("%s: %w", r.ID+r.Role, errNoLabel)
	}
	if r.Role != "" {
		if !slices.Contains(roleVocabulary, r.Role) {
			return Item{}, fmt.Errorf("%q: %w", r.Role, errUnknownRole)
		}
		return Item{Role: r.Role, Label: *r.Label}, nil
	}
	if r.Accelerator != "" {
		if _, err := canonicalAccelerator(r.Accelerator); err != nil {
			return Item{}, fmt.Errorf("%s: %w", r.ID, err)
		}
	}
	if r.Display != "" {
		if _, ok := DisplayAccelerator(r.Display); !ok {
			return Item{}, fmt.Errorf("%s: %q: %w", r.ID, r.Display, errUnknownDisplay)
		}
	}
	for _, s := range r.EnabledIn {
		if !slices.Contains(screens, s) {
			return Item{}, fmt.Errorf("%s: %q: %w", r.ID, s, errUnknownScreen)
		}
	}
	return Item{
		ID:             r.ID,
		Label:          *r.Label,
		Accelerator:    r.Accelerator,
		Display:        r.Display,
		EnabledIn:      slices.Clone(r.EnabledIn),
		NeedsSelection: r.NeedsSelection,
		HiddenWhen:     slices.Clone(r.HiddenWhen),
	}, nil
}

// claim records the item's id and accelerator in one list's sets, refusing one
// already there.
func claim(it Item, ids map[string]bool, accels map[string]string) error {
	if it.ID == "" {
		return nil
	}
	if ids[it.ID] {
		return fmt.Errorf("%q: %w", it.ID, errDuplicateID)
	}
	ids[it.ID] = true
	if it.Accelerator == "" {
		return nil
	}
	c, err := canonicalAccelerator(it.Accelerator)
	if err != nil {
		return fmt.Errorf("%s: %w", it.ID, err)
	}
	if other, ok := accels[c]; ok {
		return fmt.Errorf("%s and %s both use %q: %w", other, it.ID, it.Accelerator, errDuplicateAccelerator)
	}
	accels[c] = it.ID
	return nil
}

// displayAccelerators maps the glyphs a manifest display may use, other
// than a single capital letter, to Wails accelerators.
var displayAccelerators = map[string]string{
	"Space": "Space",
	"→":     "Right",
	"←":     "Left",
	"↩":     "Enter",
	"⇧↩":    "Shift+Enter",
	"?":     "Shift+/",
}

// DisplayAccelerator returns the Wails accelerator for a manifest display:
// Space→Space, one capital letter→itself, →→Right, ←→Left, ↩→Enter,
// ⇧↩→Shift+Enter, ?→Shift+/. Any other display gives false.
func DisplayAccelerator(display string) (string, bool) {
	if acc, ok := displayAccelerators[display]; ok {
		return acc, true
	}
	if len(display) == 1 && display[0] >= 'A' && display[0] <= 'Z' {
		return display, true
	}
	return "", false
}

// Modifier names as Wails' accelerator parser accepts them (keys.go), each
// with the macOS key it stands for.
var modifiers = map[string]string{
	"cmdorctrl":   "cmd",
	"cmd":         "cmd",
	"command":     "cmd",
	"super":       "cmd",
	"ctrl":        "ctrl",
	"optionoralt": "option",
	"alt":         "option",
	"option":      "option",
	"shift":       "shift",
}

// namedKeys are the key names Wails' accelerator parser accepts besides a
// single printable character and "plus" (keys.go; f1–f35 are checked apart).
var namedKeys = []string{
	"backspace", "tab", "return", "enter", "escape", "left", "right", "up", "down",
	"space", "delete", "forward delete", "home", "end", "page up", "page down", "numlock",
}

// parseAccelerator parses acc as Wails does and returns the macOS modifier
// keys it holds, sorted and once each, and its key, lowercased.
func parseAccelerator(acc string) (mods []string, key string, err error) {
	parts := strings.Split(acc, "+")
	key = strings.ToLower(parts[len(parts)-1])
	switch {
	case key == "plus":
		key = "+"
	case slices.Contains(namedKeys, key), isFunctionKey(key):
	case len(key) == 1 && strconv.IsPrint(rune(key[0])):
	default:
		return nil, "", fmt.Errorf("%q: key %q: %w", acc, parts[len(parts)-1], errBadAccelerator)
	}
	for _, p := range parts[:len(parts)-1] {
		mod, ok := modifiers[strings.ToLower(p)]
		if !ok {
			return nil, "", fmt.Errorf("%q: modifier %q: %w", acc, p, errBadAccelerator)
		}
		if !slices.Contains(mods, mod) {
			mods = append(mods, mod)
		}
	}
	slices.Sort(mods)
	return mods, key, nil
}

// canonicalAccelerator returns one spelling per key combination on macOS:
// the modifier keys, sorted, then the key ("cmd shift p").
func canonicalAccelerator(acc string) (string, error) {
	mods, key, err := parseAccelerator(acc)
	if err != nil {
		return "", err
	}
	return strings.Join(append(mods, key), " "), nil
}

// isFunctionKey reports whether name is f1 to f35.
func isFunctionKey(name string) bool {
	n, err := strconv.Atoi(strings.TrimPrefix(name, "f"))
	return strings.HasPrefix(name, "f") && err == nil && n >= 1 && n <= 35 && name == "f"+strconv.Itoa(n)
}

// hasChordModifier reports whether acc holds ⌘, ⌃ or ⌥ — a modifier that
// keeps the key from typing a character. Shift alone does not. An
// accelerator that does not parse has none.
func hasChordModifier(acc string) bool {
	mods, _, err := parseAccelerator(acc)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(mods, func(m string) bool { return m != "shift" })
}
